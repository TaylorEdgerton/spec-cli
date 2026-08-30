package review

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

type FileStatus string

const (
	StatusMatched    FileStatus = "matched"
	StatusAdditional FileStatus = "additional"
	StatusUntouched  FileStatus = "untouched"
)

type FileReview struct {
	Path          string
	PlannedAction state.PlanFileAction
	ActualAction  gitutil.ChangeKind
	Reason        string
	Status        FileStatus
	Change        *gitutil.FileChange
}

type Reviewability string

const (
	ReviewabilityGood     Reviewability = "good"
	ReviewabilityModerate Reviewability = "moderate"
	ReviewabilityLow      Reviewability = "low"
)

type IntegrationPrecision string

const (
	PrecisionPrecise    IntegrationPrecision = "precise"
	PrecisionStructural IntegrationPrecision = "structural"
	PrecisionPlanned    IntegrationPrecision = "planned"
)

type Integration struct {
	Symbol       string
	Path         string
	Line         int
	Relationship string
	Change       string
	Precision    IntegrationPrecision
}

type Projection struct {
	Files         []FileReview
	Stats         state.ChangeStats
	Drift         state.PlanDriftSummary
	Reviewability Reviewability
	Integrations  []Integration
}

type HunkReview struct {
	Hunk   gitutil.DiffHunk
	Symbol string
}

func Project(plan *state.StoredChangePlan, changes []gitutil.FileChange, discoveries []discovery.Result) Projection {
	actual := make([]gitutil.FileChange, 0, len(changes))
	byPath := make(map[string]int, len(changes)*2)
	for _, change := range changes {
		change.Path = normalizePath(change.Path)
		change.OldPath = normalizePath(change.OldPath)
		if change.Path == "" || change.Path == gitutil.ActiveSpecPattern {
			continue
		}
		index := len(actual)
		actual = append(actual, change)
		byPath[change.Path] = index
		if change.OldPath != "" {
			byPath[change.OldPath] = index
		}
	}
	sort.SliceStable(actual, func(left, right int) bool { return actual[left].Path < actual[right].Path })
	byPath = make(map[string]int, len(actual)*2)
	for index := range actual {
		byPath[actual[index].Path] = index
		if actual[index].OldPath != "" {
			byPath[actual[index].OldPath] = index
		}
	}

	projection := Projection{}
	matchedActual := make([]bool, len(actual))
	if plan != nil {
		for _, planned := range plan.Plan.Files {
			path := normalizePath(planned.Path)
			if path == "" || path == gitutil.ActiveSpecPattern {
				continue
			}
			file := FileReview{
				Path:          path,
				PlannedAction: planned.Action,
				Reason:        planned.Reason,
				Status:        StatusUntouched,
			}
			if index, ok := byPath[path]; ok {
				change := actual[index]
				file.Path = change.Path
				file.ActualAction = change.Kind
				file.Status = StatusMatched
				file.Change = &change
				matchedActual[index] = true
				projection.Drift.Matched++
			} else {
				projection.Drift.Untouched++
			}
			projection.Files = append(projection.Files, file)
		}
	}
	for index := range actual {
		change := actual[index]
		projection.Stats.Files++
		projection.Stats.Additions += change.Additions
		projection.Stats.Deletions += change.Deletions
		if matchedActual[index] {
			continue
		}
		projection.Files = append(projection.Files, FileReview{
			Path:         change.Path,
			ActualAction: change.Kind,
			Status:       StatusAdditional,
			Change:       &change,
		})
		if plan != nil {
			projection.Drift.Additional++
		}
	}
	projection.Reviewability = ReviewabilityFor(
		projection.Stats.Files,
		projection.Stats.Additions+projection.Stats.Deletions,
	)
	projection.Integrations = projectIntegrations(plan, actual, discoveries)
	return projection
}

func ReviewabilityFor(files, changedLines int) Reviewability {
	if files <= 6 && changedLines <= 300 {
		return ReviewabilityGood
	}
	if files <= 12 && changedLines <= 800 {
		return ReviewabilityModerate
	}
	return ReviewabilityLow
}

func AssociateHunks(hunks []gitutil.DiffHunk, symbols []discovery.Symbol) []HunkReview {
	reviewed := make([]HunkReview, 0, len(hunks))
	for _, hunk := range hunks {
		nearestLine := -1
		nearestSymbol := ""
		for _, symbol := range symbols {
			if symbol.Line <= hunk.NewStart && symbol.Line > nearestLine {
				nearestLine = symbol.Line
				nearestSymbol = symbol.Name
			}
		}
		reviewed = append(reviewed, HunkReview{Hunk: hunk, Symbol: nearestSymbol})
	}
	return reviewed
}

func projectIntegrations(plan *state.StoredChangePlan, changes []gitutil.FileChange, discoveries []discovery.Result) []Integration {
	integrations := make([]Integration, 0)
	if plan != nil {
		for _, declared := range plan.Plan.IntegrationPoints {
			integrations = append(integrations, Integration{
				Symbol:       declared.ExistingSymbol,
				Relationship: declared.Relationship,
				Change:       declared.PlannedChange,
				Precision:    PrecisionPlanned,
			})
		}
	}
	changeByPath := make(map[string]gitutil.ChangeKind, len(changes))
	for _, change := range changes {
		changeByPath[change.Path] = change.Kind
	}
	for _, result := range discoveries {
		for _, symbol := range result.Symbols {
			for _, related := range symbol.Related {
				path := normalizePath(related.Path)
				if path == "" {
					path = normalizePath(result.Path)
				}
				integrations = append(integrations, Integration{
					Symbol:       related.Name,
					Path:         path,
					Line:         related.Line,
					Relationship: related.Relation,
					Change:       string(changeByPath[path]),
					Precision:    integrationPrecision(related.Capability),
				})
			}
		}
	}
	return integrations
}

func integrationPrecision(capability discovery.Capability) IntegrationPrecision {
	if capability == discovery.CapabilityPrecise {
		return PrecisionPrecise
	}
	return PrecisionStructural
}

func normalizePath(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, `\`, "/"))
	if path == "" {
		return ""
	}
	path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if path == "." {
		return ""
	}
	return strings.TrimPrefix(path, "./")
}
