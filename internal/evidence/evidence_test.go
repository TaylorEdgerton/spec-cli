package evidence

import (
	"reflect"
	"testing"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func TestClassifyDerivesFiveEvidenceCategoriesWithoutAScore(t *testing.T) {
	start := time.Date(2026, 8, 30, 1, 0, 0, 0, time.UTC)
	runs := []state.EvidenceRun{
		automatedRun("before", PhasePreChange, "baseline", "worktree-before", start, []state.EvidenceTest{
			testFact("stable", "TestStable", "stable-digest", "passed"),
			testFact("repro", "TestReproduction", "repro-digest", "failed"),
			testFact("modified", "TestModified", "old-digest", "passed"),
		}),
		automatedRun("after", PhaseImplementation, "baseline", "worktree-after", start.Add(time.Minute), []state.EvidenceTest{
			testFact("stable", "TestStable", "stable-digest", "passed"),
			testFact("repro", "TestReproduction", "repro-digest", "passed"),
			testFact("modified", "TestModified", "new-digest", "passed"),
			testFact("new", "TestNew", "new-test-digest", "passed"),
		}),
		{ID: "manual", Phase: PhaseImplementation, Command: "checked interactive flow", Manual: true, Passed: true, StartedAt: start.Add(2 * time.Minute), FinishedAt: start.Add(3 * time.Minute)},
	}

	report := Classify(runs, "worktree-after")
	if got, want := categoriesByID(report.Items), map[string]Category{
		"stable": CategoryExisting, "repro": CategoryFailThenPass, "modified": CategoryModifiedExisting,
		"new": CategoryNewTest, "manual": CategoryManual,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("categories = %v, want %v", got, want)
	}
	if report.Summary != (state.EvidenceSummary{Existing: 1, FailThenPass: 1, NewTests: 1, ModifiedExisting: 1, Manual: 1}) {
		t.Fatalf("summary = %+v", report.Summary)
	}
	manual := itemByID(t, report.Items, "manual")
	if manual.Automated || manual.Passing || manual.Status != "claimed" {
		t.Fatalf("manual evidence = %+v", manual)
	}
	if _, exists := reflect.TypeOf(report).FieldByName("Score"); exists {
		t.Fatal("evidence report must not expose a synthetic score")
	}
}

func TestFailThenPassRequiresTheSameNonEmptySourceDigest(t *testing.T) {
	start := time.Date(2026, 8, 30, 1, 0, 0, 0, time.UTC)
	runs := []state.EvidenceRun{
		automatedRun("red", PhasePreChange, "base", "red-tree", start, []state.EvidenceTest{testFact("test", "TestChanged", "before", "failed")}),
		automatedRun("green", PhaseImplementation, "base", "green-tree", start.Add(time.Minute), []state.EvidenceTest{testFact("test", "TestChanged", "after", "passed")}),
	}
	item := itemByID(t, Classify(runs, "green-tree").Items, "test")
	if item.Category != CategoryModifiedExisting {
		t.Fatalf("changed red/green test category = %q", item.Category)
	}

	runs[0].Tests[0].SourceDigest = ""
	runs[1].Tests[0].SourceDigest = ""
	item = itemByID(t, Classify(runs, "green-tree").Items, "test")
	if item.Category == CategoryFailThenPass {
		t.Fatalf("digest-free red/green was upgraded: %+v", item)
	}
}

func TestClassifyKeepsProvenanceAndMarksStaleAbsentAndParserFailuresHonestly(t *testing.T) {
	start := time.Date(2026, 8, 30, 1, 0, 0, 0, time.UTC)
	before := automatedRun("red", PhasePreChange, "base-sha", "red-tree", start, []state.EvidenceTest{testFact("missing", "TestMissing", "same", "failed")})
	after := automatedRun("after", PhaseImplementation, "base-sha", "old-tree", start.Add(time.Minute), []state.EvidenceTest{testFact("other", "TestOther", "new", "passed")})
	broken := state.EvidenceRun{ID: "parse", Phase: PhaseImplementation, Command: "go test -json ./...", ParserError: "invalid JSON", Passed: true, BaselineSHA: "base-sha", WorktreeFingerprint: "old-tree", StartedAt: start.Add(2 * time.Minute), FinishedAt: start.Add(3 * time.Minute)}

	report := Classify([]state.EvidenceRun{before, after, broken}, "current-tree")
	missing := itemByID(t, report.Items, "missing")
	if missing.Status != "absent" || missing.Passing || missing.Fresh {
		t.Fatalf("absent evidence = %+v", missing)
	}
	if len(missing.Observations) != 1 || missing.Observations[0].BaselineSHA != "base-sha" || missing.Observations[0].WorktreeFingerprint != "red-tree" || !missing.Observations[0].StartedAt.Equal(start) {
		t.Fatalf("provenance = %+v", missing.Observations)
	}
	parser := itemByID(t, report.Items, "parse")
	if parser.Status != "parser_error" || parser.Passing || parser.Automated {
		t.Fatalf("parser evidence = %+v", parser)
	}
}

func automatedRun(id, phase, baseline, fingerprint string, at time.Time, tests []state.EvidenceTest) state.EvidenceRun {
	passed := true
	for _, test := range tests {
		if test.Status == "failed" {
			passed = false
		}
	}
	return state.EvidenceRun{
		ID: id, Phase: phase, Command: "go test -json ./...", Passed: passed, BaselineSHA: baseline,
		WorktreeFingerprint: fingerprint, StartedAt: at, FinishedAt: at.Add(time.Second), Tests: tests,
	}
}

func testFact(id, name, digest, status string) state.EvidenceTest {
	return state.EvidenceTest{ID: id, Name: name, Path: "feature_test.go", SourceDigest: digest, Status: status}
}

func categoriesByID(items []Item) map[string]Category {
	result := make(map[string]Category, len(items))
	for _, item := range items {
		result[item.ID] = item.Category
	}
	return result
}

func itemByID(t *testing.T, items []Item, id string) Item {
	t.Helper()
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("item %q not found in %+v", id, items)
	return Item{}
}
