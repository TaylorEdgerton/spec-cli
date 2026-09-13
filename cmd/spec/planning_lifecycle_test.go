package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/change"
	promptbuilder "github.com/TaylorEdgerton/spec-cli/internal/prompt"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
	"github.com/charmbracelet/x/ansi"
)

func TestSimplifiedLifecycleAndFirstClassPlanActions(t *testing.T) {
	for _, facts := range []overviewFacts{{}, {SetupActive: true}, {PlanAvailable: true}, {ReviewEntered: true}, {EvidenceCount: 2}, {Completed: true}} {
		var labels []string
		for _, stage := range deriveOverviewStages(facts) {
			labels = append(labels, stage.Label)
		}
		if !reflect.DeepEqual(labels, []string{"Define", "Implement", "Review", "Complete"}) {
			t.Fatalf("lifecycle = %v", labels)
		}
	}
	model := newPlanCaptureModel("")
	var labels []string
	for _, item := range model.screen().selectableItems() {
		labels = append(labels, item.Label)
	}
	if !reflect.DeepEqual(labels, []string{"Copy planning prompt", "Paste AI plan", "Continue without plan"}) {
		t.Fatalf("AI Plan actions = %v", labels)
	}
	model.cursor = 1
	model.Update(key(tea.KeyEnter, ""))
	if model.mode != planCapturePaste {
		t.Fatal("Paste AI plan must open the editor directly")
	}
}

func TestPlanningBoundaryUsesStartingContentAndSurvivesDefinitionEditing(t *testing.T) {
	root, workspace, now := definitionRepository(t, false)
	if err := workspace.Abandon(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(path, []byte("pre-existing work"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := change.BeginSetup(root, "Boundary", now); err != nil {
		t.Fatal(err)
	}
	workspace, _ = state.Load(root)
	if workspace.StartingFingerprint == "" {
		t.Fatal("starting state not captured")
	}
	if amend, err := workspace.PlanNeedsAmendment(); err != nil || amend {
		t.Fatalf("pre-existing work misclassified: %v, %v", amend, err)
	}
	if _, err := saveDefinitionContract(root, *workspace.Setup, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := change.BeginEdit(root); err != nil {
		t.Fatal(err)
	}
	loaded, _ := state.Load(root)
	if loaded.StartingFingerprint != workspace.StartingFingerprint {
		t.Fatal("definition edit recaptured starting state")
	}
	if err := os.WriteFile(path, []byte("implementation"), 0600); err != nil {
		t.Fatal(err)
	}
	if amend, err := loaded.PlanNeedsAmendment(); err != nil || !amend {
		t.Fatalf("changed content not detected: %v, %v", amend, err)
	}
}

func TestPlanBoundaryDetectsRepositoryOperations(t *testing.T) {
	for _, operation := range []string{"modify", "delete", "rename", "untracked", "commit"} {
		t.Run(operation, func(t *testing.T) {
			root, workspace := planningRepository(t)
			files, err := exec.Command("git", "-C", root, "ls-files").Output()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, strings.Split(strings.TrimSpace(string(files)), "\n")[0])
			switch operation {
			case "delete":
				err = os.Remove(path)
			case "rename":
				err = os.Rename(path, path+".moved")
			case "untracked":
				err = os.WriteFile(filepath.Join(root, "extra.txt"), []byte("new"), 0600)
			default:
				err = os.WriteFile(path, []byte("changed"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if operation == "commit" {
				for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=Spec Test", "-c", "user.email=spec@example.invalid", "commit", "-m", "implementation"}} {
					if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
						t.Fatalf("%s: %v", out, err)
					}
				}
			}
			if amend, err := workspace.PlanNeedsAmendment(); err != nil || !amend {
				t.Fatalf("%s not detected: %v %v", operation, amend, err)
			}
		})
	}
}

func TestReviewPlanAmendmentShowsBothComparisonsAndExplicitRefresh(t *testing.T) {
	root, workspace := planningRepository(t)
	now := time.Now().UTC()
	if err := runPlanSubmit(root, strings.NewReader(`{"summary":"original","files":[{"path":"new.go","action":"create"}]}`), io.Discard, now); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.go"), []byte("package base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	app := newWorkflowApp(root, screenReview)
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	before := app.reviewed.RefreshedAt
	app.navigate(actionPlan)
	if m := app.active.(*planModel); !m.amend || !m.received {
		t.Fatal("Review did not open received plan with Amend plan")
	}
	if err := runPlanSubmit(root, strings.NewReader(`{"summary":"amended","files":[{"path":"other.go","action":"create"}]}`), io.Discard, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	app.navigate(actionSummary)
	plain := ansi.Strip(app.View().Content)
	if !strings.Contains(plain, "Plan changed") || !app.reviewed.RefreshedAt.Equal(before) {
		t.Fatalf("missing stale comparison state: %s", plain)
	}
	app.Update(key('r', "r"))
	plain = ansi.Strip(app.View().Content)
	if !strings.Contains(plain, "Original plan vs actual") || !strings.Contains(plain, "Current plan vs actual") {
		t.Fatalf("comparison missing: %s", plain)
	}
	stored, _ := workspace.Plan()
	if stored.Original == nil || len(stored.Amendments) != 1 {
		t.Fatalf("plan history missing: %+v", stored)
	}
	app.active.(*reviewModel).selectTab(tabChanges)
	app.Update(key('v', "v"))
	if plain := ansi.Strip(app.View().Content); !strings.Contains(plain, "Comparing original plan") || !strings.Contains(plain, "Matched") {
		t.Fatalf("original file comparison unavailable: %s", plain)
	}
	if !reflect.DeepEqual(app.reviewed.Plan.Plan, stored.Plan) {
		t.Fatal("comparison toggle changed the stored snapshot plan")
	}
}

func TestPlanErrorRetryPreservesPastedResponse(t *testing.T) {
	m := newPlanCaptureModel("")
	raw := "```spec-plan\n{broken}\n```"
	m.previewRaw(raw)
	m.Update(key(tea.KeyEnter, ""))
	if m.editor.value != raw {
		t.Fatalf("retry lost input: %q", m.editor.value)
	}
}

func planningRepository(t *testing.T) (string, state.Workspace) {
	t.Helper()
	root, workspace, _ := definitionRepository(t, false)
	setup := *workspace.Setup
	setup.Title, setup.Outcome = "Improve planning", "Humans can edit and amend plans"
	if _, err := saveDefinitionContract(root, setup, io.Discard); err != nil {
		t.Fatal(err)
	}
	workspace, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, workspace
}

func TestPlanAmendmentPreservesOriginalAcrossCLIAndPaste(t *testing.T) {
	root, workspace := planningRepository(t)
	now := time.Now().UTC()
	for i, summary := range []string{"Original intention", "Corrected before coding"} {
		if err := runPlanSubmit(root, strings.NewReader(`{"summary":"`+summary+`"}`), io.Discard, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "new.go"), []byte("package base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := capturePlanDecision(root, "```spec-plan\n{\"summary\":\"Amended after coding\"}\n```", planAccept, "human", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := workspace.Plan()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(stored)
	var record map[string]json.RawMessage
	_ = json.Unmarshal(data, &record)
	if !bytes.Contains(record["original"], []byte("Original intention")) || !bytes.Contains(record["amendments"], []byte("Amended after coding")) {
		t.Fatalf("original/amendment lost: %s", data)
	}
	if stored.Plan.Summary != "Amended after coding" {
		t.Fatalf("current plan = %s", stored.Plan.Summary)
	}
}

func TestImplementAndReviewUseStartingStateLanguage(t *testing.T) {
	m := newOverviewModel(overviewData{Baseline: "abc123", Facts: overviewFacts{BaselineReady: true}})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	plain := ansi.Strip(m.View().Content)
	if strings.Contains(strings.ToLower(plain), "baseline") {
		t.Fatalf("internal term leaked: %s", plain)
	}
	if !strings.Contains(plain, "Implement") {
		t.Fatal("Implement destination missing")
	}
}

func TestAgentPromptExamplesAreExecutableContracts(t *testing.T) {
	root, workspace := planningRepository(t)
	content, _, err := promptbuilder.BuildKind(root, false, promptbuilder.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Choose exactly one", "required non-empty", "duplicate normalized paths", "three backticks", "Paste AI plan", "do not also emit", "Stop after", "Starting state"} {
		if !strings.Contains(content, text) {
			t.Fatalf("prompt missing %q", text)
		}
	}
	block, err := extractPlanBlock(content)
	if err != nil {
		t.Fatal(err)
	}
	pasted, err := validateChangePlan(block)
	if err != nil {
		t.Fatalf("generated fence invalid: %v", err)
	}
	start := strings.Index(content, "<<'SPEC_PLAN'\n")
	if start < 0 {
		t.Fatal("stdin example missing")
	}
	raw := strings.SplitN(content[start+len("<<'SPEC_PLAN'\n"):], "\nSPEC_PLAN", 2)[0]
	if err := runPlanSubmit(root, strings.NewReader(raw), io.Discard, time.Now()); err != nil {
		t.Fatal(err)
	}
	stored, _ := workspace.Plan()
	if !reflect.DeepEqual(stored.Plan, pasted) {
		t.Fatal("stdin and fenced examples differ")
	}
	content, _, err = promptbuilder.BuildKind(root, false, promptbuilder.Plan)
	if err != nil || !strings.Contains(content, "## Current AI plan") || !strings.Contains(content, "complete replacement") {
		t.Fatalf("edit prompt lost current plan: %v", err)
	}
	for _, invalid := range []string{`{"summary":"x"} }`, `{"summary":"x"} garbage`, `{"summary":"x"} {"summary":"y"}`} {
		if _, err := validateChangePlan([]byte(invalid)); err == nil {
			t.Fatalf("trailing data accepted: %s", invalid)
		}
	}
}

func TestPlanAmendmentArchiveAndLateSubmission(t *testing.T) {
	root, workspace := planningRepository(t)
	if err := os.WriteFile(filepath.Join(root, "late.go"), []byte("package base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := runPlanSubmit(root, strings.NewReader(`{"summary":"late original"}`), io.Discard, now); err != nil {
		t.Fatal(err)
	}
	if err := runPlanSubmit(root, strings.NewReader(`{"summary":"amendment"}`), io.Discard, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	stored, _ := workspace.Plan()
	if !stored.AfterChanges || stored.Original == nil || !stored.Original.AfterChanges {
		t.Fatal("late timing lost")
	}
	if err := runPlanSubmit(root, strings.NewReader(`{"summary":"amendment"}`), io.Discard, now.Add(time.Second)); err != nil {
		t.Fatalf("identical submission not idempotent: %v", err)
	}
	content, err := os.ReadFile(change.ActivePath(root))
	if err != nil {
		t.Fatal(err)
	}
	record, err := workspace.Finish(state.History{Title: "Planning", FinishedAt: now.Add(time.Minute), CompletionAcknowledged: true}, content, change.ActivePath(root))
	if err != nil {
		t.Fatal(err)
	}
	if record.StartingFingerprint == "" || record.Plan.Original.Plan.Summary != "late original" || len(record.Plan.Amendments) != 1 || record.Plan.Plan.Summary != "amendment" {
		t.Fatalf("archive lost planning facts: %+v", record)
	}
	model := newHistoryModel(root, workspace.Dir, []state.History{record}, false)
	model.openSelected()
	if !strings.Contains(model.spec, "late original") || !strings.Contains(model.spec, "amendment") {
		t.Fatal("archived plans are not inspectable")
	}
}

func TestAIPlanningScreensStayBoundedAndActionsRemainVisible(t *testing.T) {
	root, workspace := planningRepository(t)
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		app := newWorkflowApp(root, screenOverview)
		app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		app.navigate(actionPlan)
		capture := app.active.(*planCaptureModel)
		for _, mode := range []planCaptureMode{planCaptureChoice, planCaptureClipboardError, planCapturePaste, planCaptureClipboardPreview} {
			capture.mode = mode
			capture.plan = state.ChangePlan{Summary: "Example", Files: []state.PlannedFile{{Path: "new.go", Action: state.PlanFileCreate}}}
			view := ansi.Strip(app.View().Content)
			if len(strings.Split(view, "\n")) > size[1] {
				t.Fatalf("mode %d too tall: %s", mode, view)
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("mode %d too wide: %s", mode, line)
				}
			}
		}
	}
	if err := runPlanSubmit(root, strings.NewReader(`{"summary":"original"}`), io.Discard, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.go"), []byte("package base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runPlanSubmit(root, strings.NewReader(`{"summary":"amendment"}`), io.Discard, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		app := newWorkflowApp(root, screenPlan)
		app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		if view := ansi.Strip(app.View().Content); !strings.Contains(view, "Amend plan") || !strings.Contains(view, "Continue implementation") {
			t.Fatalf("received actions missing: %s", view)
		}
		app.navigate(actionReview)
		review := app.active.(*reviewModel)
		for _, tab := range []reviewTab{tabSummary, tabChanges, tabIntegration, tabEvidence, tabDiff} {
			review.selectTab(tab)
			view := ansi.Strip(app.View().Content)
			if len(strings.Split(view, "\n")) > size[1] {
				t.Fatalf("review too tall: %s", view)
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("review too wide: %s", line)
				}
			}
			if !strings.Contains(view, "AI Plan") {
				t.Fatalf("plan access hidden: %s", view)
			}
		}
	}
	plan, _ := workspace.Plan()
	if len(plan.Amendments) != 1 {
		t.Fatal("navigation mutated plan")
	}
}
