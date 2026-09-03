package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	StatsRefreshed                                 bool
	RefreshedAt                                    time.Time
	Facts                                          overviewFacts
}
type overviewNext struct {
	Title, Message string
	Items          []screenItem
}
type overviewModel struct {
	data                overviewData
	stages              []overviewStage
	status              string
	width, height       int
	done                bool
	help                bool
	nav                 string
	cursor              int
	viewport            int
	showBaselineDetails bool

	copyPrompt func(string) error
}

const overviewLargeDriftFiles = 12

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
	return &overviewModel{data: d, stages: deriveOverviewStages(d.Facts), copyPrompt: copyImplementationPrompt}
}

func (m *overviewModel) screen() canonicalScreen {
	next := m.next()
	sections := []screenSection{{ID: "overview.next", Title: "NEXT", Items: next.Items}}
	if m.largeBaselineDrift() {
		sections = append(sections, screenSection{ID: "overview.drift", Title: "Baseline drift", Items: []screenItem{
			{ID: "overview.drift.review", Label: "Review anyway", Selectable: true, Action: screenAction(actionReview)},
			{ID: "overview.drift.details", Label: "View baseline details", Selectable: true, Action: screenAction(actionBaseline)},
		}})
	}
	return canonicalScreen{Sections: sections, Cursor: m.cursor}
}

func (m *overviewModel) next() overviewNext {
	switch {
	case m.data.Facts.SetupActive:
		return overviewNext{Title: "Define change", Message: "Finish the intent and expected behaviour before implementation starts.", Items: []screenItem{
			{ID: "overview.next.definition", Label: "Continue defining change", Selectable: true, Action: screenAction(actionDefinition)},
		}}
	case m.data.Facts.Completed:
		return overviewNext{Title: "Complete", Message: "This Spec is complete. Its retained change record is available in History.", Items: []screenItem{
			{ID: "overview.next.history", Label: "Open completed Spec history", Selectable: true, Action: screenAction(actionHistory)},
		}}
	case m.data.Facts.EvidenceCount > 0 || m.data.Facts.ReviewEntered:
		message := "Open Review to refresh current workspace changes before deciding whether the available evidence is convincing."
		if m.data.StatsRefreshed {
			message = "Review the explicitly refreshed changes and decide whether the available evidence is convincing."
		}
		return overviewNext{Title: "Review", Message: message, Items: []screenItem{
			{ID: "overview.next.review", Label: "Review current workspace changes", Selectable: true, Action: screenAction(actionReview)},
			{ID: "overview.next.evidence", Label: "Review tests and evidence", Selectable: true, Action: screenAction(actionEvidence)},
			{ID: "overview.next.summary", Label: "Review change summary", Selectable: true, Action: screenAction(actionSummary)},
		}}
	default:
		plan := screenItem{ID: "overview.next.plan.capture", Label: "Capture AI plan (optional)", Selectable: true, Action: screenAction(actionPlanCapture)}
		if m.data.Facts.PlanAvailable {
			plan = screenItem{ID: "overview.next.plan.view", Label: "View accepted AI plan", Selectable: true, Action: screenAction(actionPlan)}
		}
		return overviewNext{Title: "Implementation", Message: "Prompt is ready. Implement the change with your preferred AI.", Items: []screenItem{
			{ID: "overview.next.prompt", Label: "Copy implementation prompt", Selectable: true, Action: screenAction(actionPrompt)},
			plan,
			{ID: "overview.next.review", Label: "Review current workspace changes", Selectable: true, Action: screenAction(actionReview)},
		}}
	}
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
		return m.openSelectedAction()
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

func (m *overviewModel) openSelectedAction() tea.Cmd {
	item, ok := m.screen().selectedItem()
	if !ok {
		return nil
	}
	action := string(item.Action)
	switch action {
	case actionPrompt:
		m.copySelectedPrompt()
		return nil
	case actionBaseline:
		m.showBaselineDetails = true
		m.status = "Showing the baseline and refresh provenance used by this Overview."
		return nil
	}
	if action == "" {
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
	return [][2]string{{"↑/↓", "select"}, {"enter", "open"}, {"p", "prompt"}, {"r", "review"},
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
	lines := m.bodyLines(max(20, w-8), h < 30)
	header := fmt.Sprintf("%s · %s  OPEN\n%s · baseline %s · %s", m.data.SpecID, m.data.Title, m.data.Branch, base, homeElapsed(m.data.StartedAt, m.data.Now))
	body := strings.Join(lines, "\n")
	if m.help {
		body = uiHelpOverlayWidth(m.hints(), max(20, w-8))
	}
	anchor := ""
	if item, ok := m.screen().selectedItem(); ok {
		anchor = item.Label
	}
	body, m.viewport = uiViewportBody(body, uiWorkflowBodyHeight(h, header), m.viewport, anchor)
	return tea.NewView(uiAppShell(w, h, header, body, uiKeyHints(m.hints(), "    ")))
}

func (m *overviewModel) bodyLines(width int, compact bool) []string {
	var lines []string
	if compact {
		lines = append(lines,
			uiLabelledProse("Intent", emptyAs(m.data.Intent, "No intent was provided."), width),
			uiLabelledProse("Expected behaviour", emptyAs(m.data.Scope, "No expected behaviour was provided."), width),
		)
	} else {
		lines = append(lines, uiTitleStyle.Render("Intent"))
		lines = append(lines, strings.Split(uiProse(emptyAs(m.data.Intent, "No intent was provided."), width), "\n")...)
		lines = append(lines, uiTitleStyle.Render("Expected behaviour"))
		lines = append(lines, strings.Split(uiProse(emptyAs(m.data.Scope, "No expected behaviour was provided."), width), "\n")...)
	}
	next := m.next()
	nextMessage := next.Message
	if m.status != "" {
		nextMessage = m.status
	}
	var nextLines []string
	if compact {
		nextLines = append(nextLines, uiLabelledProse(next.Title, nextMessage, max(10, width-4)))
	} else {
		nextLines = append(nextLines, uiTitleStyle.Render(next.Title))
		nextLines = append(nextLines, strings.Split(uiProse(nextMessage, max(10, width-4)), "\n")...)
	}
	selected, _ := m.screen().selectedItem()
	for _, item := range next.Items {
		row := "  " + item.Label
		if item.ID == selected.ID {
			row = uiSelectedRow("> "+item.Label, 0)
		}
		nextLines = append(nextLines, row)
	}
	nextBody := strings.Join(nextLines, "\n")
	lines = append(lines, uiPanel(width, lipgloss.Height(nextBody)+2, uiPurple, "NEXT", "", nextBody))
	if compact {
		lines = append(lines, uiTitleStyle.Render("Change lifecycle"))
		lines = append(lines, m.compactLifecycleLines()...)
	} else {
		lines = append(lines, uiTitleStyle.Render("Change lifecycle"))
		lines = append(lines, m.lifecycleLines()...)
	}
	lines = append(lines, uiTitleStyle.Render("Since baseline"))
	base := shortSHA(m.data.Baseline)
	if m.largeBaselineDrift() {
		lines = append(lines, uiLineStyle.Render(fmt.Sprintf("! %d files have changed since baseline %s.", m.data.Stats.Files, base)), "  This change may contain unrelated work.", m.driftActionLine(selected))
	} else if !m.data.StatsRefreshed {
		lines = append(lines, "  Not refreshed in this session.", "  Open Review to refresh actual Git state since baseline "+base+".")
	} else {
		lines = append(lines,
			fmt.Sprintf("  Files %d   Lines +%d -%d   Tests changed %d", m.data.Stats.Files, m.data.Stats.Additions, m.data.Stats.Deletions, m.data.Stats.TestsAdded),
			"  Cached from explicit refresh "+m.data.RefreshedAt.UTC().Format("15:04 UTC"))
	}
	if m.showBaselineDetails {
		lines = append(lines, uiTitleStyle.Render("Baseline details"), "  Commit   "+emptyDash(m.data.Baseline), "  Started  "+homeElapsed(m.data.StartedAt, m.data.Now))
		if m.data.StatsRefreshed {
			lines = append(lines, "  Facts    cached from explicit Review refresh at "+m.data.RefreshedAt.UTC().Format(time.RFC3339))
		}
	}
	return lines
}

func (m *overviewModel) lifecycleLines() []string {
	base := shortSHA(m.data.Baseline)
	lines := make([]string, 0, len(m.stages))
	for _, stage := range m.stages {
		mark := map[stageStatus]string{stageComplete: "✓", stageCurrent: "●", stagePending: "○", stageOmitted: "–"}[stage.Status]
		detail := ""
		switch stage.ID {
		case "baseline":
			detail = base
		case "plan":
			if stage.Status == stageOmitted {
				detail = "optional"
			}
		}
		if stage.Status == stageCurrent {
			detail = "CURRENT"
		}
		lines = append(lines, fmt.Sprintf("  %s %-28s %s", mark, stage.Label, detail))
	}
	return lines
}

func (m *overviewModel) compactLifecycleLines() []string {
	stageText := func(id, label string) string {
		for _, stage := range m.stages {
			if stage.ID == id {
				mark := map[stageStatus]string{stageComplete: "✓", stageCurrent: "●", stagePending: "○", stageOmitted: "–"}[stage.Status]
				detail := ""
				if stage.Status == stageCurrent {
					detail = " CURRENT"
				}
				return mark + " " + label + detail
			}
		}
		return "○ " + label
	}
	planDetail := "optional"
	if m.data.Facts.PlanAvailable {
		planDetail = "accepted"
	}
	return []string{
		fmt.Sprintf("  %s   %s %s   %s %s", stageText("intent", "Intent & Scope"), stageText("baseline", "Baseline"), shortSHA(m.data.Baseline), stageText("plan", "Implementation Plan"), planDetail),
		fmt.Sprintf("  %s   %s   %s   %s", stageText("implementation", "Implementation"), stageText("review", "Review"), stageText("evidence", "Evidence"), stageText("complete", "Complete")),
	}
}

func (m *overviewModel) driftActionLine(selected screenItem) string {
	items := m.screen().Sections[1].Items
	labels := make([]string, len(items))
	for index, item := range items {
		labels[index] = "[ " + item.Label + " ]"
		if item.ID == selected.ID {
			labels[index] = uiSelectedRow("> [ "+item.Label+" ]", 0)
		}
	}
	return "  " + strings.Join(labels, "   ")
}

func (m *overviewModel) largeBaselineDrift() bool {
	return m.data.StatsRefreshed && m.data.Stats.Files > overviewLargeDriftFiles
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
	recordPromptDelivery(root, state.TimelinePromptCopied, promptbuilder.Implementation, "clipboard")
	return nil
}
func copyPlanningPrompt(root string) error {
	content, _, err := promptbuilder.BuildKind(root, false, promptbuilder.Plan)
	if err != nil {
		return err
	}
	if err := copyText(content); err != nil {
		return err
	}
	recordPromptDelivery(root, state.TimelinePromptCopied, promptbuilder.Plan, "clipboard")
	return nil
}
func emptyAs(v, f string) string {
	if strings.TrimSpace(v) == "" {
		return uiMutedStyle.Render(f)
	}
	return v
}
