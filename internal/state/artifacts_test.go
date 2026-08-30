package state

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSpecIDIsStableAcrossLifecycleAndMonotonic(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	workspace, err := Register(root)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 8, 30, 1, 2, 3, 0, time.UTC)
	if err := workspace.BeginSetup("abc123", started, Setup{Stage: "change", Title: "First"}); err != nil {
		t.Fatal(err)
	}
	firstID := workspace.SpecID
	if firstID != "SPEC-001" {
		t.Fatalf("first Spec ID = %q", firstID)
	}
	startEvents, err := workspace.TimelineEvents()
	if err != nil || !containsTimelineID(startEvents, firstID+":created") || !containsTimelineID(startEvents, firstID+":baseline") {
		t.Fatalf("start timeline = %+v, %v", startEvents, err)
	}
	loaded, err := Load(root)
	if err != nil || loaded.SpecID != firstID {
		t.Fatalf("resumed ID = %q, %v", loaded.SpecID, err)
	}
	if err := loaded.CompleteSetup("First"); err != nil {
		t.Fatal(err)
	}
	if err := loaded.BeginEdit(Setup{Stage: "review", Title: "First"}); err != nil {
		t.Fatal(err)
	}
	if err := loaded.CompleteSetup("First edited"); err != nil || loaded.SpecID != firstID {
		t.Fatalf("edited ID = %q, %v", loaded.SpecID, err)
	}
	activePath := filepath.Join(root, ".spec-active.md")
	if err := os.WriteFile(activePath, []byte("# First\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	finished, err := loaded.Finish(History{Title: "First", StartedAt: started, FinishedAt: started.Add(5 * time.Minute)}, []byte("# First\n"), activePath)
	if err != nil || finished.SpecID != firstID {
		t.Fatalf("finished ID = %q, %v", finished.SpecID, err)
	}
	if err := loaded.Start("Second", "def456", started.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if loaded.SpecID != "SPEC-002" {
		t.Fatalf("second Spec ID = %q", loaded.SpecID)
	}
}

func TestPlanTimelineAndEvidenceRoundTrip(t *testing.T) {
	workspace := artifactWorkspace(t)
	now := time.Date(2026, 8, 30, 2, 3, 4, 0, time.UTC)
	plan := StoredChangePlan{
		SchemaVersion: ArtifactSchemaVersion, Source: PlanSourcePaste, Submitter: "Codex", SubmittedAt: now, AcceptedAt: now.Add(time.Minute),
		Plan: ChangePlan{
			Summary:           "Disable automatic indexing",
			Files:             []PlannedFile{{Path: "config/config.go", Action: PlanFileModify, Reason: "add setting"}},
			IntegrationPoints: []PlannedIntegration{{ExistingSymbol: "ensureIndex", PlannedChange: "read setting", Relationship: "controls automatic indexing"}},
			Verification:      []PlannedVerification{{Behaviour: "manual indexing remains available", LikelyLocation: "indexer/indexer_test.go"}},
			Uncertainties:     []string{"CLI flag location"},
		},
	}
	if err := workspace.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	loadedPlan, err := workspace.Plan()
	if err != nil || loadedPlan == nil || !reflect.DeepEqual(*loadedPlan, plan) {
		t.Fatalf("plan = %+v, %v", loadedPlan, err)
	}

	event := TimelineEvent{SchemaVersion: 1, ID: "review-1", Type: "review_opened", Actor: "human", Source: "spec", OccurredAt: now, Details: TimelineDetails{SpecID: workspace.SpecID, Count: 2}}
	if err := workspace.AppendTimeline(event); err != nil {
		t.Fatal(err)
	}
	if err := workspace.AppendTimeline(event); err != nil {
		t.Fatalf("idempotent event append: %v", err)
	}
	events, err := workspace.TimelineEvents()
	if err != nil || countTimelineID(events, event.ID) != 1 {
		t.Fatalf("events = %+v, %v", events, err)
	}

	run := EvidenceRun{
		SchemaVersion: 1, ID: "evidence-1", Phase: "pre_change", Command: "go test ./...", BaselineSHA: "abc123", WorktreeFingerprint: "fingerprint", StartedAt: now, FinishedAt: now.Add(time.Second),
		Tests: []EvidenceTest{{ID: "test-1", Name: "TestAutoIndexDisabled", Path: "indexer/indexer_test.go", SourceDigest: "sha256:test-source", Status: "failed"}},
	}
	if err := workspace.AppendEvidence(run); err != nil {
		t.Fatal(err)
	}
	if err := workspace.AppendEvidence(run); err != nil {
		t.Fatalf("idempotent evidence append: %v", err)
	}
	runs, err := workspace.EvidenceRuns()
	if err != nil || len(runs) != 1 || !reflect.DeepEqual(runs[0], run) {
		t.Fatalf("evidence = %+v, %v", runs, err)
	}
	for _, name := range []string{planFilename, timelineFilename, evidenceFilename} {
		info, err := os.Stat(filepath.Join(workspace.Dir, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("artifact %s permissions = %v, %v", name, info, err)
		}
	}
}

func TestArtifactDuplicatesAndCorruptionAreRejected(t *testing.T) {
	workspace := artifactWorkspace(t)
	now := time.Now().UTC()
	plan := StoredChangePlan{SchemaVersion: 1, Source: PlanSourceCLI, AcceptedAt: now, Plan: ChangePlan{Summary: "original"}}
	if err := workspace.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	changedPlan := plan
	changedPlan.Plan.Summary = "conflicting"
	if err := workspace.SavePlan(changedPlan); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("conflicting duplicate plan error = %v", err)
	}
	persistedPlan, err := workspace.Plan()
	if err != nil || persistedPlan == nil || persistedPlan.Plan.Summary != plan.Plan.Summary {
		t.Fatalf("conflicting plan replaced original: %+v, %v", persistedPlan, err)
	}
	event := TimelineEvent{SchemaVersion: 1, ID: "same", Type: "first", Actor: "human", Source: "spec", OccurredAt: now}
	if err := workspace.AppendTimeline(event); err != nil {
		t.Fatal(err)
	}
	event.Type = "different"
	if err := workspace.AppendTimeline(event); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("conflicting duplicate event error = %v", err)
	}
	run := EvidenceRun{SchemaVersion: 1, ID: "same", Phase: "existing", StartedAt: now, FinishedAt: now}
	if err := workspace.AppendEvidence(run); err != nil {
		t.Fatal(err)
	}
	run.Phase = "changed"
	if err := workspace.AppendEvidence(run); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("conflicting duplicate evidence error = %v", err)
	}

	if err := os.WriteFile(filepath.Join(workspace.Dir, "timeline.jsonl"), []byte("{partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.TimelineEvents(); err == nil || !strings.Contains(err.Error(), "timeline") {
		t.Fatalf("corrupt timeline error = %v", err)
	}
	if err := workspace.AppendTimeline(TimelineEvent{SchemaVersion: 1, ID: "after-corruption"}); err == nil {
		t.Fatal("append silently replaced corrupt timeline")
	}
}

func TestFinishSnapshotsArtifactsBeforeCleanup(t *testing.T) {
	workspace := artifactWorkspace(t)
	now := time.Date(2026, 8, 30, 3, 4, 5, 0, time.UTC)
	plan := StoredChangePlan{SchemaVersion: 1, Source: PlanSourceCLI, SubmittedAt: now, AcceptedAt: now, Plan: ChangePlan{Summary: "Plan"}}
	event := TimelineEvent{SchemaVersion: 1, ID: "custom", Type: "prompt_copied", Actor: "human", Source: "spec", OccurredAt: now}
	run := EvidenceRun{SchemaVersion: 1, ID: "run", Phase: "implementation", Passed: true, StartedAt: now, FinishedAt: now.Add(time.Second)}
	if err := workspace.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := workspace.AppendTimeline(event); err != nil {
		t.Fatal(err)
	}
	if err := workspace.AppendEvidence(run); err != nil {
		t.Fatal(err)
	}
	activePath := filepath.Join(workspace.Root, ".spec-active.md")
	content := []byte("# Intent\n")
	if err := os.WriteFile(activePath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	record := History{
		Title: "Intent", Intent: "Intent", Scope: "Scope", StartedAt: now, FinishedAt: now.Add(10 * time.Minute),
		AcceptanceReview: AcceptanceReview{Total: 2, Reviewed: 1}, Stats: ChangeStats{Files: 3, Additions: 10, Deletions: 2, TestsAdded: 1},
		PlanDrift: PlanDriftSummary{Matched: 1, Additional: 2}, EvidenceSummary: EvidenceSummary{NewTests: 1}, CompletionAcknowledged: true,
	}
	finished, err := workspace.Finish(record, content, activePath)
	if err != nil {
		t.Fatal(err)
	}
	if finished.SpecID == "" || finished.Plan == nil || finished.Plan.Plan.Summary != "Plan" || len(finished.Evidence) != 1 || !containsTimelineID(finished.Timeline, "custom") {
		t.Fatalf("finished artifacts = %+v", finished)
	}
	if finished.DurationSeconds != 600 || !finished.CompletionAcknowledged || finished.AcceptanceReview.Reviewed != 1 || finished.PlanDrift.Additional != 2 {
		t.Fatalf("finished summary = %+v", finished)
	}
	for _, name := range []string{"plan.json", "timeline.jsonl", "evidence.jsonl"} {
		if _, err := os.Stat(filepath.Join(workspace.Dir, name)); !os.IsNotExist(err) {
			t.Fatalf("active artifact %s remains: %v", name, err)
		}
	}
	records, err := workspace.HistoryRecords()
	if err != nil || len(records) != 1 || records[0].Plan == nil || len(records[0].Evidence) != 1 {
		t.Fatalf("history snapshot = %+v, %v", records, err)
	}
	// The History screen reads these fields back, so they must survive the archive.
	stored := records[0]
	if stored.SpecID != finished.SpecID || stored.Scope != "Scope" || stored.BaseSHA != "abc123" {
		t.Fatalf("history identity = %+v", stored)
	}
	if !stored.FinishedAt.Equal(now.Add(10*time.Minute)) || stored.DurationSeconds != 600 {
		t.Fatalf("history completion time = %+v", stored)
	}
	if stored.Stats.Files != 3 || stored.PlanDrift.Matched != 1 || stored.EvidenceSummary.NewTests != 1 || !containsTimelineID(stored.Timeline, "custom") {
		t.Fatalf("history review facts = %+v", stored)
	}
	archived, err := os.ReadFile(filepath.Join(workspace.Dir, filepath.FromSlash(stored.SpecArchive)))
	if err != nil || string(archived) != string(content) {
		t.Fatalf("archived Spec = %q, %v", archived, err)
	}
}

func TestFinishLeavesActiveArtifactsWhenSnapshotIsCorrupt(t *testing.T) {
	workspace := artifactWorkspace(t)
	activePath := filepath.Join(workspace.Root, ".spec-active.md")
	if err := os.WriteFile(activePath, []byte("# Active\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Dir, "evidence.jsonl"), []byte("{partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Finish(History{Title: "Active", FinishedAt: time.Now()}, []byte("# Active\n"), activePath); err == nil {
		t.Fatal("finish accepted corrupt evidence")
	}
	loaded, err := Load(workspace.Root)
	if err != nil || !loaded.Active {
		t.Fatalf("workspace was cleared after failed snapshot: %+v, %v", loaded, err)
	}
	if _, err := os.Stat(activePath); err != nil {
		t.Fatalf("active spec removed after failed snapshot: %v", err)
	}
}

func TestLegacyMetadataAndHistoryLoadWithoutNewFields(t *testing.T) {
	root := t.TempDir()
	stateHome := filepath.Join(t.TempDir(), "state")
	t.Setenv("SPEC_STATE_HOME", stateHome)
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	workspace, err := Register(root)
	if err != nil {
		t.Fatal(err)
	}
	legacyMetadata := `{"root":` + quoteJSON(root) + `,"id":"legacy","active":false}`
	if err := os.WriteFile(filepath.Join(workspace.Dir, "metadata.json"), []byte(legacyMetadata), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyHistory := `{"title":"Old","started_at":"2026-08-30T00:00:00Z","base_sha":"abc","finished_at":"2026-08-30T00:01:00Z","spec_archive":"specs/old.md"}` + "\n"
	if err := os.WriteFile(filepath.Join(workspace.Dir, "history.jsonl"), []byte(legacyHistory), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil || loaded.SpecID != "" || loaded.NextSpecNumber != 0 {
		t.Fatalf("legacy metadata = %+v, %v", loaded.Metadata, err)
	}
	records, err := loaded.HistoryRecords()
	if err != nil || len(records) != 1 || records[0].Title != "Old" || records[0].Plan != nil {
		t.Fatalf("legacy history = %+v, %v", records, err)
	}
}

func artifactWorkspace(t *testing.T) Workspace {
	t.Helper()
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	workspace, err := Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.BeginSetup("abc123", time.Now(), Setup{Stage: "change", Title: "Artifacts"}); err != nil {
		t.Fatal(err)
	}
	if err := workspace.CompleteSetup("Artifacts"); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func countTimelineID(events []TimelineEvent, id string) int {
	count := 0
	for _, event := range events {
		if event.ID == id {
			count++
		}
	}
	return count
}

func containsTimelineID(events []TimelineEvent, id string) bool {
	return countTimelineID(events, id) > 0
}

func quoteJSON(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
