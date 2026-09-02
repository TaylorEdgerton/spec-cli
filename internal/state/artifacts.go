package state

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

const ArtifactSchemaVersion = 1

type PlanSource string

const (
	PlanSourcePaste PlanSource = "paste"
	PlanSourceCLI   PlanSource = "cli"
)

type PlanFileAction string

const (
	PlanFileCreate PlanFileAction = "create"
	PlanFileModify PlanFileAction = "modify"
	PlanFileDelete PlanFileAction = "delete"
)

type PlannedFile struct {
	Path   string         `json:"path"`
	Action PlanFileAction `json:"action"`
	Reason string         `json:"reason,omitempty"`
}

type PlannedIntegration struct {
	ExistingSymbol string `json:"existing_symbol"`
	PlannedChange  string `json:"planned_change"`
	Relationship   string `json:"relationship"`
}

type PlannedVerification struct {
	Behaviour      string `json:"behaviour"`
	LikelyLocation string `json:"likely_location,omitempty"`
}

type ChangePlan struct {
	Summary           string                `json:"summary"`
	Files             []PlannedFile         `json:"files,omitempty"`
	IntegrationPoints []PlannedIntegration  `json:"integration_points,omitempty"`
	Verification      []PlannedVerification `json:"verification,omitempty"`
	Uncertainties     []string              `json:"uncertainties,omitempty"`
}

type StoredChangePlan struct {
	SchemaVersion int        `json:"schema_version"`
	Source        PlanSource `json:"source"`
	Submitter     string     `json:"submitter,omitempty"`
	SubmittedAt   time.Time  `json:"submitted_at"`
	AcceptedAt    time.Time  `json:"accepted_at"`
	Plan          ChangePlan `json:"plan"`
}

type TimelineDetails struct {
	SpecID      string `json:"spec_id,omitempty"`
	Title       string `json:"title,omitempty"`
	BaselineSHA string `json:"baseline_sha,omitempty"`
	Summary     string `json:"summary,omitempty"`
	PromptKind  string `json:"prompt_kind,omitempty"`
	Count       int    `json:"count,omitempty"`
}

type TimelineEventType string

const (
	TimelineSpecCreated        TimelineEventType = "spec_created"
	TimelineBaselineCaptured   TimelineEventType = "baseline_captured"
	TimelineDiscoveryRefreshed TimelineEventType = "discovery_refreshed"
	TimelinePromptCopied       TimelineEventType = "prompt_copied"
	TimelinePromptPrinted      TimelineEventType = "prompt_printed"
	TimelinePlanAccepted       TimelineEventType = "plan_accepted"
	TimelineActualRefreshed    TimelineEventType = "actual_state_refreshed"
	TimelineEvidenceRecorded   TimelineEventType = "evidence_recorded"
	TimelineReviewDecision     TimelineEventType = "review_decision"
	TimelineChangesRequested   TimelineEventType = "changes_requested"
	TimelineSpecCompleted      TimelineEventType = "spec_completed"
)

type TimelineEvent struct {
	SchemaVersion int               `json:"schema_version"`
	ID            string            `json:"id"`
	Type          TimelineEventType `json:"type"`
	Actor         string            `json:"actor"`
	Source        string            `json:"source"`
	OccurredAt    time.Time         `json:"occurred_at"`
	Details       TimelineDetails   `json:"details,omitempty"`
}

type EvidenceTest struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Path         string `json:"path,omitempty"`
	SourceDigest string `json:"source_digest,omitempty"`
	Status       string `json:"status"`
}

type EvidenceRun struct {
	SchemaVersion       int            `json:"schema_version"`
	ID                  string         `json:"id"`
	Phase               string         `json:"phase"`
	Command             string         `json:"command,omitempty"`
	Passed              bool           `json:"passed"`
	Manual              bool           `json:"manual,omitempty"`
	ParserError         string         `json:"parser_error,omitempty"`
	BaselineSHA         string         `json:"baseline_sha,omitempty"`
	WorktreeFingerprint string         `json:"worktree_fingerprint,omitempty"`
	StartedAt           time.Time      `json:"started_at"`
	FinishedAt          time.Time      `json:"finished_at"`
	Tests               []EvidenceTest `json:"tests,omitempty"`
}

type AcceptanceReview struct {
	Total    int `json:"total,omitempty"`
	Reviewed int `json:"reviewed,omitempty"`
}

type ChangeStats struct {
	Files      int `json:"files,omitempty"`
	Additions  int `json:"additions,omitempty"`
	Deletions  int `json:"deletions,omitempty"`
	TestsAdded int `json:"tests_added,omitempty"`
}

type PlanDriftSummary struct {
	Matched    int `json:"matched,omitempty"`
	Additional int `json:"additional,omitempty"`
	Untouched  int `json:"untouched,omitempty"`
}

type EvidenceSummary struct {
	Existing         int `json:"existing,omitempty"`
	FailThenPass     int `json:"fail_then_pass,omitempty"`
	NewTests         int `json:"new_tests,omitempty"`
	ModifiedExisting int `json:"modified_existing,omitempty"`
	Manual           int `json:"manual,omitempty"`
}

const (
	planFilename     = "plan.json"
	timelineFilename = "timeline.jsonl"
	evidenceFilename = "evidence.jsonl"
)

func activeArtifactNames() []string {
	return []string{"prompt.md", "verification.json", planFilename, timelineFilename, evidenceFilename}
}

func (workspace Workspace) SavePlan(plan StoredChangePlan) error {
	if !workspace.Active {
		return fmt.Errorf("save ChangePlan: no active Spec")
	}
	if strings.TrimSpace(plan.Plan.Summary) == "" {
		return fmt.Errorf("save ChangePlan: summary is required")
	}
	if plan.SchemaVersion == 0 {
		plan.SchemaVersion = ArtifactSchemaVersion
	}
	if plan.SchemaVersion != ArtifactSchemaVersion {
		return fmt.Errorf("save ChangePlan: unsupported schema version %d", plan.SchemaVersion)
	}
	if _, err := workspace.TimelineEvents(); err != nil {
		return err
	}
	stamp := plan.AcceptedAt
	if stamp.IsZero() {
		stamp = plan.SubmittedAt
	}
	event := TimelineEvent{
		SchemaVersion: ArtifactSchemaVersion,
		ID:            fmt.Sprintf("%s:plan:%d", workspace.SpecID, stamp.UnixNano()),
		Type:          TimelinePlanAccepted,
		Actor:         plan.Submitter,
		Source:        string(plan.Source),
		OccurredAt:    stamp,
		Details:       TimelineDetails{SpecID: workspace.SpecID, Summary: plan.Plan.Summary, Count: len(plan.Plan.Files)},
	}
	existing, err := workspace.Plan()
	if err != nil {
		return err
	}
	if existing != nil {
		if reflect.DeepEqual(*existing, plan) {
			return workspace.AppendTimeline(event)
		}
		existingStamp := existing.AcceptedAt
		if existingStamp.IsZero() {
			existingStamp = existing.SubmittedAt
		}
		if existingStamp.Equal(stamp) {
			return fmt.Errorf("save ChangePlan: duplicate acceptance %s has different content", stamp.Format(time.RFC3339Nano))
		}
	}
	if err := writeJSON(filepath.Join(workspace.Dir, planFilename), plan); err != nil {
		return fmt.Errorf("save ChangePlan: %w", err)
	}
	return workspace.AppendTimeline(event)
}

func (workspace Workspace) Plan() (*StoredChangePlan, error) {
	data, err := os.ReadFile(filepath.Join(workspace.Dir, planFilename))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read ChangePlan: %w", err)
	}
	var plan StoredChangePlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("read ChangePlan: %w", err)
	}
	return &plan, nil
}

func (workspace Workspace) AppendTimeline(event TimelineEvent) error {
	if !workspace.Active {
		return fmt.Errorf("append timeline: no active Spec")
	}
	if strings.TrimSpace(event.ID) == "" {
		return fmt.Errorf("append timeline: event ID is required")
	}
	if event.SchemaVersion == 0 {
		event.SchemaVersion = ArtifactSchemaVersion
	}
	return appendJSONRecord(filepath.Join(workspace.Dir, timelineFilename), "timeline", event.ID, event,
		func(value TimelineEvent) string { return value.ID })
}

func (workspace Workspace) TimelineEvents() ([]TimelineEvent, error) {
	return readJSONRecords[TimelineEvent](filepath.Join(workspace.Dir, timelineFilename), "timeline")
}

func (workspace Workspace) AppendEvidence(run EvidenceRun) error {
	if !workspace.Active {
		return fmt.Errorf("append evidence: no active Spec")
	}
	if strings.TrimSpace(run.ID) == "" {
		return fmt.Errorf("append evidence: run ID is required")
	}
	if run.SchemaVersion == 0 {
		run.SchemaVersion = ArtifactSchemaVersion
	}
	if _, err := workspace.TimelineEvents(); err != nil {
		return err
	}
	if err := appendJSONRecord(filepath.Join(workspace.Dir, evidenceFilename), "evidence", run.ID, run,
		func(value EvidenceRun) string { return value.ID }); err != nil {
		return err
	}
	return workspace.AppendTimeline(TimelineEvent{
		SchemaVersion: ArtifactSchemaVersion,
		ID:            workspace.SpecID + ":evidence:" + run.ID,
		Type:          TimelineEvidenceRecorded,
		Actor:         "spec",
		Source:        "verification",
		OccurredAt:    run.FinishedAt,
		Details:       TimelineDetails{SpecID: workspace.SpecID, Summary: run.Phase, Count: len(run.Tests)},
	})
}

func (workspace Workspace) EvidenceRuns() ([]EvidenceRun, error) {
	return readJSONRecords[EvidenceRun](filepath.Join(workspace.Dir, evidenceFilename), "evidence")
}

func appendJSONRecord[T any](path, label, id string, value T, identity func(T) string) error {
	records, err := readJSONRecords[T](path, label)
	if err != nil {
		return err
	}
	for _, existing := range records {
		if identity(existing) != id {
			continue
		}
		if reflect.DeepEqual(existing, value) {
			return nil
		}
		return fmt.Errorf("append %s: duplicate ID %q has different content", label, id)
	}
	records = append(records, value)
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			return fmt.Errorf("append %s: %w", label, err)
		}
	}
	if err := writeFile(path, output.Bytes()); err != nil {
		return fmt.Errorf("append %s: %w", label, err)
	}
	return nil
}

func readJSONRecords[T any](path, label string) ([]T, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	defer file.Close()
	var records []T
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var record T
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, fmt.Errorf("read %s line %d: %w", label, line, err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	return records, nil
}
