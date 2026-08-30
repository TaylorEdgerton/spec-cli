package review

import (
	"reflect"
	"testing"

	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func TestProjectUsesNeutralPlanVsActualStatusesAndNormalizedPaths(t *testing.T) {
	plan := &state.StoredChangePlan{Plan: state.ChangePlan{
		Summary: "Disable automatic indexing",
		Files: []state.PlannedFile{
			{Path: `config\config.go`, Action: state.PlanFileModify, Reason: "setting"},
			{Path: "indexer/indexer.go", Action: state.PlanFileModify, Reason: "behavior"},
			{Path: "docs/config.md", Action: state.PlanFileDelete, Reason: "obsolete"},
		},
	}}
	changes := []gitutil.FileChange{
		{Path: "config/config.go", Kind: gitutil.ChangeModified, Additions: 10, Deletions: 2},
		{Path: "indexer/indexer.go", Kind: gitutil.ChangeModified, Additions: 4, Deletions: 1},
		{Path: "cmd/spec/config.go", Kind: gitutil.ChangeAdded, Additions: 5},
		{Path: ".spec.md", Kind: gitutil.ChangeModified, Additions: 100},
	}
	projection := Project(plan, changes, nil)
	if got, want := fileStatuses(projection.Files), []string{
		"config/config.go:matched", "indexer/indexer.go:matched", "docs/config.md:untouched", "cmd/spec/config.go:additional",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("file statuses = %v, want %v", got, want)
	}
	if projection.Drift != (state.PlanDriftSummary{Matched: 2, Additional: 1, Untouched: 1}) {
		t.Fatalf("drift = %+v", projection.Drift)
	}
	if projection.Stats.Files != 3 || projection.Stats.Additions != 19 || projection.Stats.Deletions != 3 || projection.Reviewability != ReviewabilityGood {
		t.Fatalf("summary = stats:%+v reviewability:%s", projection.Stats, projection.Reviewability)
	}
}

func TestProjectWithoutPlanSummarizesActualChanges(t *testing.T) {
	projection := Project(nil, []gitutil.FileChange{{Path: "new.go", Kind: gitutil.ChangeAdded, Additions: 20}}, nil)
	if len(projection.Files) != 1 || projection.Files[0].Status != StatusAdditional || projection.Drift != (state.PlanDriftSummary{}) {
		t.Fatalf("actual-only projection = %+v", projection)
	}
}

func TestReviewabilityThresholdsAreInclusiveAndDeterministic(t *testing.T) {
	tests := []struct {
		files, lines int
		want         Reviewability
	}{
		{6, 300, ReviewabilityGood},
		{7, 300, ReviewabilityModerate},
		{6, 301, ReviewabilityModerate},
		{12, 800, ReviewabilityModerate},
		{13, 800, ReviewabilityLow},
		{12, 801, ReviewabilityLow},
	}
	for _, test := range tests {
		if got := ReviewabilityFor(test.files, test.lines); got != test.want {
			t.Errorf("ReviewabilityFor(%d, %d) = %q, want %q", test.files, test.lines, got, test.want)
		}
	}
}

func TestIntegrationProjectionPreservesEvidencePrecisionAndDeclaredClaims(t *testing.T) {
	plan := &state.StoredChangePlan{Plan: state.ChangePlan{
		Summary: "Change indexing",
		IntegrationPoints: []state.PlannedIntegration{{
			ExistingSymbol: "runIndexCommand", PlannedChange: "manual path remains unchanged", Relationship: "declared invariant",
		}},
	}}
	discovered := []discovery.Result{{
		Path: "indexer/indexer.go",
		Symbols: []discovery.Symbol{{
			Name: "ensureIndex", Line: 84, Capability: discovery.CapabilityPrecise,
			Related: []discovery.RelatedSymbol{
				{Name: "Config.AutoIndexEnabled", Path: "config/config.go", Line: 22, Relation: "reads setting", Capability: discovery.CapabilityPrecise},
				{Name: "createIndex", Path: "indexer/indexer.go", Line: 90, Relation: "controls flow", Capability: discovery.CapabilityStructural},
			},
		}},
	}}
	projection := Project(plan, []gitutil.FileChange{{Path: "indexer/indexer.go", Kind: gitutil.ChangeModified}}, discovered)
	if len(projection.Integrations) != 3 {
		t.Fatalf("integrations = %+v", projection.Integrations)
	}
	if projection.Integrations[0].Symbol != "runIndexCommand" || projection.Integrations[0].Precision != PrecisionPlanned {
		t.Fatalf("declared integration = %+v", projection.Integrations[0])
	}
	if projection.Integrations[1].Precision != PrecisionPrecise || projection.Integrations[2].Precision != PrecisionStructural {
		t.Fatalf("discovered precision = %+v", projection.Integrations)
	}
}

func TestAssociateHunksKeepsOrderAndUsesNearestEnclosingSymbol(t *testing.T) {
	hunks := []gitutil.DiffHunk{{Header: "first", NewStart: 15}, {Header: "second", NewStart: 55}}
	symbols := []discovery.Symbol{{Name: "First", Line: 10}, {Name: "Second", Line: 50}, {Name: "Later", Line: 100}}
	reviewed := AssociateHunks(hunks, symbols)
	if len(reviewed) != 2 || reviewed[0].Hunk.Header != "first" || reviewed[0].Symbol != "First" || reviewed[1].Symbol != "Second" {
		t.Fatalf("associated hunks = %+v", reviewed)
	}
}

func fileStatuses(files []FileReview) []string {
	result := make([]string, len(files))
	for index, file := range files {
		result[index] = file.Path + ":" + string(file.Status)
	}
	return result
}
