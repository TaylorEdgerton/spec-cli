package evidence

import (
	"sort"
	"strings"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

type Category string

const (
	CategoryExisting         Category = "existing"
	CategoryFailThenPass     Category = "fail_then_pass"
	CategoryNewTest          Category = "new_test"
	CategoryModifiedExisting Category = "modified_existing_test"
	CategoryManual           Category = "manual"
)

const (
	PhaseExisting       = "existing"
	PhasePreChange      = "pre_change"
	PhaseImplementation = "implementation"
)

type Observation struct {
	RunID               string
	Phase               string
	Status              string
	SourceDigest        string
	BaselineSHA         string
	WorktreeFingerprint string
	StartedAt           time.Time
	FinishedAt          time.Time
}

type Item struct {
	ID           string
	Name         string
	Path         string
	Command      string
	Category     Category
	Status       string
	Automated    bool
	Passing      bool
	Fresh        bool
	Reason       string
	Observations []Observation
}

type Report struct {
	Items   []Item
	Summary state.EvidenceSummary
}

func Classify(runs []state.EvidenceRun, currentFingerprint string) Report {
	ordered := append([]state.EvidenceRun(nil), runs...)
	sort.SliceStable(ordered, func(left, right int) bool {
		if ordered[left].StartedAt.Equal(ordered[right].StartedAt) {
			return ordered[left].ID < ordered[right].ID
		}
		return ordered[left].StartedAt.Before(ordered[right].StartedAt)
	})

	type trackedItem struct {
		item       Item
		firstOrder int
		pre        bool
		post       bool
		preFailed  map[string]bool
		postPassed map[string]bool
		digests    map[string]bool
		unknown    bool
	}
	tracked := make(map[string]*trackedItem)
	order := make([]string, 0)
	latestParsedPost := make(map[string]state.EvidenceRun)

	appendStandalone := func(run state.EvidenceRun, status, reason string, category Category, automated bool) {
		id := strings.TrimSpace(run.ID)
		if id == "" {
			id = run.Command
		}
		item := &trackedItem{
			item: Item{
				ID: id, Name: run.Command, Command: run.Command, Category: category, Status: status,
				Automated: automated, Passing: false, Fresh: isFresh(run.WorktreeFingerprint, currentFingerprint), Reason: reason,
				Observations: []Observation{runObservation(run, status, "")},
			},
			firstOrder: len(order), preFailed: make(map[string]bool), postPassed: make(map[string]bool), digests: make(map[string]bool),
		}
		tracked[id] = item
		order = append(order, id)
	}

	for _, run := range ordered {
		if run.Manual {
			appendStandalone(run, "claimed", "human-provided evidence", CategoryManual, false)
			continue
		}
		if run.ParserError != "" {
			appendStandalone(run, "parser_error", run.ParserError, CategoryExisting, false)
			continue
		}
		if run.Phase == PhaseImplementation && len(run.Tests) > 0 {
			latestParsedPost[run.Command] = run
		}
		for _, test := range run.Tests {
			id := strings.TrimSpace(test.ID)
			if id == "" {
				id = strings.TrimSpace(test.Name)
			}
			if id == "" {
				continue
			}
			entry := tracked[id]
			if entry == nil {
				entry = &trackedItem{
					item:       Item{ID: id, Name: test.Name, Path: test.Path, Command: run.Command, Automated: true},
					firstOrder: len(order), preFailed: make(map[string]bool), postPassed: make(map[string]bool), digests: make(map[string]bool),
				}
				tracked[id] = entry
				order = append(order, id)
			}
			status := normalizeStatus(test.Status)
			entry.item.Status = status
			entry.item.Passing = status == "passed"
			entry.item.Fresh = isFresh(run.WorktreeFingerprint, currentFingerprint)
			entry.item.Observations = append(entry.item.Observations, runObservation(run, status, test.SourceDigest))
			digest := strings.TrimSpace(test.SourceDigest)
			if digest == "" {
				entry.unknown = true
			} else {
				entry.digests[digest] = true
			}
			if run.Phase == PhaseImplementation {
				entry.post = true
				if status == "passed" && digest != "" {
					entry.postPassed[digest] = true
				}
			} else {
				entry.pre = true
				if status == "failed" && digest != "" {
					entry.preFailed[digest] = true
				}
			}
		}
	}

	report := Report{Items: make([]Item, 0, len(order))}
	for _, id := range order {
		entry := tracked[id]
		if entry.item.Category == "" {
			switch {
			case strings.HasPrefix(id, "command:"):
				entry.item.Category = CategoryExisting
			case !entry.pre:
				entry.item.Category = CategoryNewTest
			case entry.post && len(entry.digests) > 1:
				entry.item.Category = CategoryModifiedExisting
			case entry.post && !entry.unknown && hasMatchingDigest(entry.preFailed, entry.postPassed):
				entry.item.Category = CategoryFailThenPass
			default:
				entry.item.Category = CategoryExisting
			}
		}
		if entry.pre {
			if latest, ok := latestParsedPost[entry.item.Command]; ok && runAfter(latest, entry.item.Observations[len(entry.item.Observations)-1].FinishedAt) && !runContains(latest, id) {
				entry.item.Status = "absent"
				entry.item.Passing = false
				entry.item.Fresh = isFresh(latest.WorktreeFingerprint, currentFingerprint)
				entry.item.Reason = "not present in the latest structured run"
			}
		}
		addSummary(&report.Summary, entry.item)
		report.Items = append(report.Items, entry.item)
	}
	return report
}

func runObservation(run state.EvidenceRun, status, digest string) Observation {
	return Observation{
		RunID: run.ID, Phase: run.Phase, Status: status, SourceDigest: digest,
		BaselineSHA: run.BaselineSHA, WorktreeFingerprint: run.WorktreeFingerprint,
		StartedAt: run.StartedAt, FinishedAt: run.FinishedAt,
	}
}

func normalizeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pass", "passed":
		return "passed"
	case "fail", "failed":
		return "failed"
	case "skip", "skipped":
		return "skipped"
	case "absent":
		return "absent"
	default:
		return "unknown"
	}
}

func isFresh(recorded, current string) bool {
	if current == "" {
		return true
	}
	return recorded != "" && recorded == current
}

func hasMatchingDigest(failed, passed map[string]bool) bool {
	for digest := range failed {
		if passed[digest] {
			return true
		}
	}
	return false
}

func runAfter(run state.EvidenceRun, timestamp time.Time) bool {
	return run.FinishedAt.After(timestamp) || run.StartedAt.After(timestamp)
}

func runContains(run state.EvidenceRun, id string) bool {
	for _, test := range run.Tests {
		candidate := strings.TrimSpace(test.ID)
		if candidate == "" {
			candidate = strings.TrimSpace(test.Name)
		}
		if candidate == id {
			return true
		}
	}
	return false
}

func addSummary(summary *state.EvidenceSummary, item Item) {
	if item.Status == "parser_error" {
		return
	}
	switch item.Category {
	case CategoryExisting:
		summary.Existing++
	case CategoryFailThenPass:
		summary.FailThenPass++
	case CategoryNewTest:
		summary.NewTests++
	case CategoryModifiedExisting:
		summary.ModifiedExisting++
	case CategoryManual:
		summary.Manual++
	}
}
