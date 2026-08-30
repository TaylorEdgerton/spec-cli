package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/change"
	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	promptbuilder "github.com/TaylorEdgerton/spec-cli/internal/prompt"
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
	Root                                           string
	Title, SpecID, Branch, Baseline, Intent, Scope string
	StartedAt, Now                                 time.Time
	Stats                                          state.ChangeStats
	Facts                                          overviewFacts
}
type overviewModel struct {
	data          overviewData
	stages        []overviewStage
	status        string
	width, height int
	done          bool
	help          bool
	nav           string
	cursor        int
	viewport      int

	copyPrompt func(string) error
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
	model := &overviewModel{data: d, stages: deriveOverviewStages(d.Facts), copyPrompt: copyImplementationPrompt}
	for index, stage := range model.stages {
		if stage.Status == stageCurrent {
			model.cursor = index
			break
		}
	}
	return model
}

func (m *overviewModel) screen() canonicalScreen {
	items := make([]screenItem, 0, len(m.stages))
	for _, stage := range m.stages {
		action := map[string]string{"intent": actionDefinition, "plan": actionPlan, "review": actionReview, "evidence": actionEvidence, "complete": actionHistory}[stage.ID]
		if stage.ID == "plan" && !m.data.Facts.PlanAvailable {
			action = actionPlanCapture
		}
		items = append(items, screenItem{ID: "overview.stage." + stage.ID, Label: stage.Label, Selectable: true, Action: screenAction(action), Preview: stage})
	}
	return canonicalScreen{Sections: []screenSection{{ID: "progress", Title: "Progress", Items: items}}, Cursor: m.cursor}
}
func (m *overviewModel) Init() tea.Cmd { return nil }
func (m *overviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
	case tea.InterruptMsg:
		m.leave(actionQuit)
		return m, tea.Quit
	case tea.KeyPressMsg:
		return m, m.key(v.Keystroke())
	}
	return m, nil
}

func (m *overviewModel) key(keystroke string) tea.Cmd {
	switch keystroke {
	case "up", "k":
		m.cursor = wrap(m.cursor-1, len(m.screen().selectableItems()))
	case "down", "j":
		m.cursor = wrap(m.cursor+1, len(m.screen().selectableItems()))
	case "?":
		m.help = !m.help
	case "p":
		m.copySelectedPrompt()
	case "l":
		if !m.data.Facts.PlanAvailable {
			m.status = "No implementation plan has been submitted for this Spec."
			return nil
		}
		m.leave(actionPlan)
		return tea.Quit
	case "r":
		m.leave(actionReview)
		return tea.Quit
	case "e":
		m.leave(actionEvidence)
		return tea.Quit
	case "h":
		m.leave(actionHistory)
		return tea.Quit
	case "enter":
		return m.openCurrentStage()
	case "b", "esc":
		m.leave(actionBack)
		return tea.Quit
	case "ctrl+c":
		m.leave(actionQuit)
		return tea.Quit
	}
	return nil
}

func (m *overviewModel) leave(action string) { m.done, m.nav = true, action }

func (m *overviewModel) openCurrentStage() tea.Cmd {
	screen := m.screen()
	items := screen.selectableItems()
	if len(items) == 0 {
		return nil
	}
	stage, ok := items[clamp(m.cursor, 0, len(items)-1)].Preview.(overviewStage)
	if !ok {
		return nil
	}
	action := string(screen.activate())
	if action == "" {
		m.status = stage.Label + " happens outside Spec; return with `spec` when it is done."
		return nil
	}
	m.leave(action)
	return tea.Quit
}

func (m *overviewModel) copySelectedPrompt() {
	if m.copyPrompt == nil {
		m.copyPrompt = copyImplementationPrompt
	}
	if err := m.copyPrompt(m.data.Root); err != nil {
		m.status = "Clipboard unavailable: " + err.Error() + " (run `spec prompt`)"
		return
	}
	m.status = "Implementation prompt copied to the clipboard."
}

func (m *overviewModel) hints() [][2]string {
	return [][2]string{{"enter", "open stage"}, {"p", "prompt"}, {"r", "review"},
		{"h", "history"}, {"?", "help"}, {"b", "back"}, {"g", "home"}}
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
	completed, total := 0, 0
	for _, stage := range m.stages {
		if stage.Status != stageOmitted {
			total++
		}
		if stage.Status == stageComplete {
			completed++
		}
	}
	percent := 0
	if total > 0 {
		percent = completed * 100 / total
	}
	filled := percent * 20 / 100
	bar := strings.Repeat("█", filled) + strings.Repeat("░", 20-filled)
	lines := []string{uiTitleStyle.Render("Intent"), m.data.Intent, "", uiTitleStyle.Render("Expected behaviour"), emptyAs(m.data.Scope, "No scope was provided."), "", uiTitleStyle.Render("Progress"), fmt.Sprintf("  %s  %d%%", bar, percent)}
	progress := m.screen().Sections[0].Items
	for i, item := range progress {
		s := item.Preview.(overviewStage)
		mark := "○"
		switch s.Status {
		case stageComplete:
			mark = "✓"
		case stageCurrent:
			mark = "●"
		case stageOmitted:
			mark = "–"
		}
		row := fmt.Sprintf("  %s %d  %s", mark, i+1, s.Label)
		if i == m.cursor {
			row = uiSelectedRow("> "+strings.TrimSpace(row), 0)
		}
		lines = append(lines, row)
	}
	lines = append(lines, "", uiTitleStyle.Render("At a glance"), fmt.Sprintf("  Changed files     %d", m.data.Stats.Files), fmt.Sprintf("  + lines           %d", m.data.Stats.Additions), fmt.Sprintf("  - lines           %d", m.data.Stats.Deletions), fmt.Sprintf("  Tests             %d", m.data.Stats.TestsAdded))
	if m.status != "" {
		lines = append(lines, "", uiMutedStyle.Render(m.status))
	}
	header := fmt.Sprintf("%s · %s  OPEN\nGit: %s · baseline %s  %d min", m.data.SpecID, m.data.Title, m.data.Branch, base, elapsed)
	body := strings.Join(lines, "\n")
	if m.help {
		body = uiHelpOverlay(m.hints())
	}
	anchor := ""
	if items := m.screen().selectableItems(); len(items) > 0 {
		anchor = items[clamp(m.cursor, 0, len(items)-1)].Label
	}
	body, m.viewport = uiViewportBody(body, uiWorkflowBodyHeight(h, header), m.viewport, anchor)
	return tea.NewView(uiAppShell(w, h, header, body, uiKeyHints(m.hints(), "    ")))
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
	d := overviewData{Root: root, Title: w.Title, SpecID: w.SpecID, Baseline: w.BaseSHA, StartedAt: w.StartedAt, Now: now, Facts: overviewFacts{SetupActive: w.Setup != nil, BaselineReady: w.BaseSHA != ""}}
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
	return d, nil
}
func copyImplementationPrompt(root string) error {
	content, _, err := promptbuilder.Build(root, false)
	if err != nil {
		return err
	}
	if err := copyText(content); err != nil {
		return err
	}
	recordPromptDelivery(root, state.TimelinePromptCopied)
	return nil
}
func emptyAs(v, f string) string {
	if strings.TrimSpace(v) == "" {
		return uiMutedStyle.Render(f)
	}
	return v
}
