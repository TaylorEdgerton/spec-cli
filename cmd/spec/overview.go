package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

type stageStatus string

const (
	stagePending  stageStatus = "pending"
	stageCurrent  stageStatus = "current"
	stageComplete stageStatus = "complete"
	stageOmitted  stageStatus = "omitted"
)

type overviewStage struct {
	ID, Label string
	Status    stageStatus
}
type overviewFacts struct {
	SetupActive, BaselineReady, PlanAvailable, WorkspaceDirty, ReviewEntered, Completed bool
	EvidenceCount                                                                       int
}
type overviewData struct {
	Title, SpecID, Branch, Baseline, Intent, Scope string
	StartedAt, Now                                 time.Time
	Stats                                          state.ChangeStats
	Facts                                          overviewFacts
}
type overviewModel struct {
	data          overviewData
	stages        []overviewStage
	width, height int
	done          bool
}

func deriveOverviewStages(f overviewFacts) []overviewStage {
	s := []overviewStage{{"intent", "Intent & Scope", stageComplete}, {"baseline", "Baseline", stageComplete}, {"plan", "Implementation Plan", stageOmitted}, {"implementation", "Implementation", stagePending}, {"review", "Review", stagePending}, {"evidence", "Evidence", stagePending}, {"complete", "Complete", stagePending}}
	current := "implementation"
	if f.SetupActive {
		current = "intent"
		s[0].Status = stageCurrent
		s[1].Status = stagePending
	} else if f.Completed {
		current = "complete"
	} else if f.EvidenceCount > 0 {
		current = "evidence"
	} else if f.ReviewEntered {
		current = "review"
	}
	if f.PlanAvailable {
		s[2].Status = stageComplete
	}
	order := map[string]int{"intent": 0, "baseline": 1, "plan": 2, "implementation": 3, "review": 4, "evidence": 5, "complete": 6}
	for i := range s {
		if s[i].ID == current {
			s[i].Status = stageCurrent
		} else if s[i].Status != stageOmitted && s[i].Status != stageComplete && i < order[current] {
			s[i].Status = stageComplete
		}
	}
	return s
}
func newOverviewModel(d overviewData) *overviewModel {
	return &overviewModel{data: d, stages: deriveOverviewStages(d.Facts)}
}
func (m *overviewModel) Init() tea.Cmd { return nil }
func (m *overviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
	case tea.InterruptMsg:
		m.done = true
		return m, tea.Quit
	case tea.KeyPressMsg:
		k := v.Keystroke()
		if k == "q" || k == "esc" || k == "ctrl+c" {
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}
func (m *overviewModel) View() tea.View {
	if m.done {
		return tea.NewView("")
	}
	w, h := m.width, m.height
	if w <= 0 {
		w = 100
	}
	if h <= 0 {
		h = 32
	}
	base := m.data.Baseline
	if len(base) > 7 {
		base = base[:7]
	}
	elapsed := max(0, int(m.data.Now.Sub(m.data.StartedAt)/time.Minute))
	lines := []string{uiTitleStyle.Render("Intent"), m.data.Intent, "", uiTitleStyle.Render("Expected behaviour"), emptyAs(m.data.Scope, "No scope was provided."), "", uiTitleStyle.Render("Progress")}
	for i, s := range m.stages {
		mark := "○"
		switch s.Status {
		case stageComplete:
			mark = "✓"
		case stageCurrent:
			mark = "●"
		case stageOmitted:
			mark = "–"
		}
		lines = append(lines, fmt.Sprintf("  %s %d  %s", mark, i+1, s.Label))
	}
	lines = append(lines, "", uiTitleStyle.Render("At a glance"), fmt.Sprintf("  Changed files     %d", m.data.Stats.Files), fmt.Sprintf("  + lines           %d", m.data.Stats.Additions), fmt.Sprintf("  - lines           %d", m.data.Stats.Deletions), fmt.Sprintf("  Tests             %d", m.data.Stats.TestsAdded))
	header := fmt.Sprintf("%s · %s  OPEN\nGit: %s · baseline %s  %d min", m.data.SpecID, m.data.Title, m.data.Branch, base, elapsed)
	footer := uiKeyHints([][2]string{{"enter", "open stage"}, {"p", "prompt"}, {"r", "review"}, {"h", "history"}, {"q", "exit"}}, "    ")
	return tea.NewView(uiAppShell(w, h, header, strings.Join(lines, "\n"), footer))
}
func (m *overviewModel) currentStage() (overviewStage, bool) {
	for _, s := range m.stages {
		if s.Status == stageCurrent {
			return s, true
		}
	}
	return overviewStage{}, false
}

func loadOverview(root string, now time.Time) (overviewData, error) {
	w, err := state.Load(root)
	if err != nil {
		return overviewData{}, err
	}
	if !w.Active {
		return overviewData{}, fmt.Errorf("no active Spec")
	}
	d := overviewData{Title: w.Title, SpecID: w.SpecID, Baseline: w.BaseSHA, StartedAt: w.StartedAt, Now: now, Facts: overviewFacts{SetupActive: w.Setup != nil, BaselineReady: w.BaseSHA != ""}}
	d.Branch, _ = gitutil.Branch(root)
	if md, e := os.ReadFile(change.ActivePath(root)); e == nil {
		if setup, e := change.SetupFromMarkdown(string(md)); e == nil {
			d.Intent, d.Scope = setup.Title, setup.Outcome
		}
	}
	plan, e := w.Plan()
	if e != nil {
		return d, e
	}
	d.Facts.PlanAvailable = plan != nil
	runs, e := w.EvidenceRuns()
	if e != nil {
		return d, e
	}
	d.Facts.EvidenceCount = len(runs)
	seen := map[string]bool{}
	for _, run := range runs {
		for _, test := range run.Tests {
			if test.ID != "" {
				seen[test.ID] = true
			}
		}
	}
	d.Stats.TestsAdded = len(seen)
	events, e := w.TimelineEvents()
	if e != nil {
		return d, e
	}
	for _, event := range events {
		if event.Type == state.TimelineActualRefreshed || event.Type == state.TimelineReviewDecision {
			d.Facts.ReviewEntered = true
		}
	}
	if w.BaseSHA != "" {
		if changes, e := gitutil.Changes(root, w.BaseSHA); e == nil {
			for _, c := range changes {
				d.Stats.Files++
				d.Stats.Additions += c.Additions
				d.Stats.Deletions += c.Deletions
			}
			d.Facts.WorkspaceDirty = len(changes) > 0
		}
	}
	return d, nil
}
func runOverview(root string, input io.Reader, output io.Writer) error {
	d, e := loadOverview(root, time.Now())
	if e != nil {
		return e
	}
	_, e = tea.NewProgram(newOverviewModel(d), tea.WithInput(input), tea.WithOutput(output)).Run()
	return e
}
func emptyAs(v, f string) string {
	if strings.TrimSpace(v) == "" {
		return uiMutedStyle.Render(f)
	}
	return v
}
