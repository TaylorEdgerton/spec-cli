package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
	"github.com/charmbracelet/x/ansi"
)

func TestExplorerRendersPersistentResponsiveCodeView(t *testing.T) {
	root, results := explorerFixture(t)
	model := newContextExplorer(root, "how does uninstall work?", results)
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	view := model.View()
	plain := ansi.Strip(view.Content)
	for _, expected := range []string{
		"Codebase Context", "Query: how does uninstall work?", "Codebase › how does uninstall work? › Run",
		"Explore (relationships for Run)", "One-hop relationships", "▼ Calls (2)", " t  test",
		"Code Preview · uninstall.go", "Tab Size: 4", "LF", "func Run()",
		"Symbol Info", "fn", "Run() error", "File: uninstall.go:3",
		"Evidence", "• matched discovery intent", "Open in", "c Copy path:line",
		"enter explore", "tab focus", "o open", "/ new query", "Press ? for help",
	} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("wide explorer missing %q:\n%s", expected, plain)
		}
	}
	if !view.AltScreen || view.MouseMode != tea.MouseModeNone || view.OnMouse != nil {
		t.Fatalf("view does not own a mouse-free alternate screen: %+v", view)
	}
	if width := lipgloss.Width(view.Content); width > 120 {
		t.Fatalf("wide view width = %d", width)
	}
	if height := lipgloss.Height(view.Content); height > 34 {
		t.Fatalf("wide view height = %d", height)
	}

	model.query = strings.Repeat("long query ", 20)
	model.Update(tea.WindowSizeMsg{Width: 78, Height: 36})
	narrow := model.View()
	if width := lipgloss.Width(narrow.Content); width > 78 {
		t.Fatalf("narrow view width = %d\n%s", width, ansi.Strip(narrow.Content))
	}
	if height := lipgloss.Height(narrow.Content); height > 36 {
		t.Fatalf("narrow view height = %d", height)
	}
	if plain := ansi.Strip(narrow.Content); !strings.Contains(plain, "Explore") || !strings.Contains(plain, "Code Preview") {
		t.Fatalf("stacked view lost a pane:\n%s", plain)
	}
}

func TestExplorerSelectionPreviewDrillBreadcrumbAndBack(t *testing.T) {
	root, results := explorerFixture(t)
	model := newContextExplorer(root, "uninstall", results)
	model.explore = func(_ string, path string, line, column int) (discovery.Symbol, error) {
		if path != "uninstall.go" || line != 7 || column != 6 {
			t.Fatalf("explore target = %s:%d:%d", path, line, column)
		}
		return discovery.Symbol{
			Name: "removeBinary()", Kind: "function", Line: 7, Column: 6,
			Related: []discovery.RelatedSymbol{{Name: "removeFile()", Kind: "function", Line: 11, Column: 6, Relation: "calls removeFile()"}},
		}, nil
	}

	model.Update(key(tea.KeyDown, ""))
	if model.cursor != 1 || model.preview.line != 7 {
		t.Fatalf("selection = %d preview line = %d", model.cursor, model.preview.line)
	}
	model.Update(key(tea.KeyEnter, ""))
	if model.current.Symbol.Name != "removeBinary()" || len(model.history) != 1 || len(model.current.Symbol.Related) != 1 {
		t.Fatalf("drilled context = %+v history = %+v", model.current, model.history)
	}
	if breadcrumb := ansi.Strip(model.breadcrumb()); !strings.Contains(breadcrumb, "Run › removeBinary") {
		t.Fatalf("breadcrumb = %q", breadcrumb)
	}
	if preview := ansi.Strip(model.renderPreview(70, 14)); !strings.Contains(preview, "› 7") {
		t.Fatalf("drilled symbol line is not selected:\n%s", preview)
	}
	if containsExplorerItem(model.items, "completionMessage()") {
		t.Fatalf("drill recursively retained the previous root's relationships: %+v", model.items)
	}

	model.Update(key('b', "b"))
	if model.current.Symbol.Name != "Run()" || len(model.history) != 0 {
		t.Fatalf("back context = %+v history = %+v", model.current, model.history)
	}
}

func TestExplorerExactSymbolQueryKeepsStableGroupedNavigation(t *testing.T) {
	root := t.TempDir()
	contextSource := `func newContextExplorer() {}

func runContextExplorer() {
	newContextExplorer()
}

func exploreDiscoveryContext() {
	newContextExplorer()
}

var dependency = true
`
	if err := os.WriteFile(filepath.Join(root, "context.go"), []byte(contextSource), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.go"), []byte("package main\n\nfunc searchMatch() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	results := []discovery.Result{
		{
			Path: "context.go", Line: 1, Column: 6,
			Symbols: []discovery.Symbol{{
				Name: "newContextExplorer()", Kind: "function", Line: 1, Column: 6, Capability: discovery.CapabilityPrecise,
				Related: []discovery.RelatedSymbol{
					{Name: "dependency", Kind: "variable", Path: "context.go", Line: 11, Column: 5, Relation: "uses dependency"},
					{Name: "exploreDiscoveryContext()", Kind: "function", Path: "context.go", Line: 7, Column: 6, Relation: "referenced by exploreDiscoveryContext()"},
					{Name: "runContextExplorer()", Kind: "function", Path: "context.go", Line: 3, Column: 6, Relation: "called by runContextExplorer()"},
				},
			}},
		},
		{Path: "other.go", Line: 3, Column: 6, Symbols: []discovery.Symbol{{Name: "searchMatch()", Kind: "function", Line: 3, Column: 6}}},
	}
	model := newContextExplorer(root, "newContextExplorer", results)
	if model.current.Symbol.Name != "newContextExplorer()" || ansi.Strip(model.breadcrumb()) != "Codebase › newContextExplorer" {
		t.Fatalf("initial context = %+v breadcrumb = %q", model.current, ansi.Strip(model.breadcrumb()))
	}
	wantOrder := []string{"newContextExplorer()", "runContextExplorer()", "exploreDiscoveryContext()", "dependency", "searchMatch()"}
	for index, want := range wantOrder {
		if index >= len(model.items) || model.items[index].symbol.Name != want {
			t.Fatalf("navigation order = %+v want %v", explorerItemNames(model.items), wantOrder)
		}
	}

	plain := ansi.Strip(model.renderExplore(90, 50))
	assertTextOrder(t, plain, "Called by (1)", "runContextExplorer()", "Referenced by (1)", "exploreDiscoveryContext()", "Uses (1)", "dependency", "Other matches (1)", "searchMatch()")

	model.Update(key(tea.KeyDown, ""))
	if model.current.Symbol.Name != "newContextExplorer()" || model.items[model.cursor].symbol.Name != "runContextExplorer()" || model.preview.line != 3 {
		t.Fatalf("selection rerooted context: current=%+v selected=%+v preview=%+v", model.current, model.items[model.cursor], model.preview)
	}
	if breadcrumb := ansi.Strip(model.breadcrumb()); breadcrumb != "Codebase › newContextExplorer" {
		t.Fatalf("selection changed breadcrumb = %q", breadcrumb)
	}
	model.explore = func(_ string, path string, line, column int) (discovery.Symbol, error) {
		return discovery.Symbol{Name: "runContextExplorer()", Kind: "function", Line: line, Column: column, Capability: discovery.CapabilityPrecise}, nil
	}
	model.Update(key(tea.KeyEnter, ""))
	if breadcrumb := ansi.Strip(model.breadcrumb()); breadcrumb != "Codebase › newContextExplorer › runContextExplorer" {
		t.Fatalf("drill breadcrumb = %q", breadcrumb)
	}
	model.Update(key('b', "b"))
	if model.current.Symbol.Name != "newContextExplorer()" || ansi.Strip(model.breadcrumb()) != "Codebase › newContextExplorer" {
		t.Fatalf("back context = %+v breadcrumb = %q", model.current, ansi.Strip(model.breadcrumb()))
	}
}

func TestExplorerRelationshipGroupsRenderAndNavigateInSameOrder(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "code.go"), []byte(strings.Repeat("// code\n", 12)), 0o644); err != nil {
		t.Fatal(err)
	}
	results := []discovery.Result{
		{Path: "code.go", Line: 1, Column: 1, Symbols: []discovery.Symbol{{
			Name: "root()", Kind: "function", Line: 1, Column: 1,
			Related: []discovery.RelatedSymbol{
				{Name: "dependencyC", Path: "code.go", Line: 7, Column: 1, Relation: "uses dependencyC"},
				{Name: "referenceB()", Path: "code.go", Line: 5, Column: 1, Relation: "referenced by referenceB()"},
				{Name: "callerA()", Path: "code.go", Line: 3, Column: 1, Relation: "called by callerA()"},
			},
		}}},
		{Path: "code.go", Line: 9, Column: 1, Symbols: []discovery.Symbol{{Name: "searchD()", Line: 9, Column: 1}}},
	}
	model := newContextExplorer(root, "root", results)
	want := []string{"root()", "callerA()", "referenceB()", "dependencyC", "searchD()"}
	if got := explorerItemNames(model.items); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("navigation order = %v want %v", got, want)
	}
	plain := ansi.Strip(model.renderExplore(80, 45))
	assertTextOrder(t, plain, "callerA()", "referenceB()", "dependencyC", "searchD()")
	for index := 1; index < len(want); index++ {
		model.Update(key(tea.KeyDown, ""))
		if model.items[model.cursor].symbol.Name != want[index] {
			t.Fatalf("cursor %d selected %q want %q", model.cursor, model.items[model.cursor].symbol.Name, want[index])
		}
	}
}

func TestExplorerOpensSelectedLocationAndStartsNewQuery(t *testing.T) {
	root, results := explorerFixture(t)
	model := newContextExplorer(root, "uninstall", results)
	var opened discovery.Result
	model.open = func(_ string, result discovery.Result) error {
		opened = result
		return nil
	}
	model.Update(key(tea.KeyDown, ""))
	model.Update(key('o', "o"))
	if opened.Path != "uninstall.go" || opened.Line != 7 || opened.Column != 6 {
		t.Fatalf("opened = %+v", opened)
	}

	model.Update(key('/', "/"))
	if !model.querying {
		t.Fatal("slash did not enter query mode")
	}
	model.find = func(_ string, query string) ([]discovery.Result, error) {
		if query != "permissions" {
			t.Fatalf("query = %q", query)
		}
		return results, nil
	}
	model.Update(tea.PasteMsg{Content: "permissions"})
	model.Update(key(tea.KeyEnter, ""))
	if model.querying || model.query != "permissions" || len(model.history) != 0 {
		t.Fatalf("query state = querying:%v query:%q history:%v", model.querying, model.query, model.history)
	}
}

func TestExplorerSearchOnlyFallbackShowsFilesAndPlainRelationships(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"health.py", "database.rb"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("def health_check\nend\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	results := []discovery.Result{
		{Path: "health.py", Line: 1, Column: 5, Preview: "def health_check", Reasons: []string{"strong intent match"}},
		{Path: "database.rb", Line: 1, Column: 5, Preview: "def health_check", Reasons: []string{"related search match"}},
	}
	model := newContextExplorer(root, "health check", results)
	if len(model.current.Symbol.Related) != 0 || len(model.items) != 2 {
		t.Fatalf("search fallback items = %+v", model.items)
	}
	model.explore = func(string, string, int, int) (discovery.Symbol, error) {
		return discovery.Symbol{}, errors.New("unsupported")
	}
	model.Update(key(tea.KeyDown, ""))
	model.Update(key(tea.KeyEnter, ""))
	if model.current.Path != "database.rb" || model.current.Symbol.Kind != "file" || len(model.current.Symbol.Related) != 0 {
		t.Fatalf("fallback drill = %+v", model.current)
	}
	if !strings.Contains(ansi.Strip(model.View().Content), "def health_check") {
		t.Fatalf("fallback preview missing:\n%s", ansi.Strip(model.View().Content))
	}
}

func TestExplorerPreviewUsesSyntaxHighlightingAndLineNumbers(t *testing.T) {
	root, results := explorerFixture(t)
	model := newContextExplorer(root, "uninstall", results)
	if len(model.preview.highlighted) == 0 || !strings.Contains(strings.Join(model.preview.highlighted, "\n"), "\x1b[") {
		t.Fatalf("Go preview was not syntax highlighted: %q", model.preview.highlighted)
	}
	plain := ansi.Strip(model.renderPreview(70, 14))
	if !strings.Contains(plain, " 3 ") || !strings.Contains(plain, "› 3") {
		t.Fatalf("preview line numbers or selected line missing:\n%s", plain)
	}
}

func TestExplorerRelationshipTreeUsesStructuralSemantics(t *testing.T) {
	root, results := explorerFixture(t)
	results[0].Symbols[0].Related = []discovery.RelatedSymbol{
		{Name: "cmdUninstall()", Kind: "function", Path: "cmd/uninstall.go", Line: 10, Column: 6, Relation: "called by cmdUninstall()"},
		{Name: "removeBinary()", Kind: "function", Line: 7, Column: 6, Relation: "calls removeBinary()"},
		{Name: "completionMessage()", Kind: "function", Line: 8, Column: 6, Relation: "calls completionMessage()"},
		{Name: "configPath", Kind: "variable", Line: 2, Column: 5, Relation: "uses configPath"},
		{Name: "TestRun()", Kind: "test", Path: "uninstall_test.go", Line: 13, Column: 6, Relation: "calls Run()"},
		{Name: "Options", Kind: "type", Line: 1, Column: 6, Relation: "related declaration"},
	}
	results = append(results, discovery.Result{
		Path: "notes.txt", Line: 4, Column: 1, Preview: "uninstall notes", Reasons: []string{"lexical intent match"},
	})
	model := newContextExplorer(root, "uninstall", results)
	plain := ansi.Strip(model.renderExplore(80, 40))
	for _, expected := range []string{
		"Called by (1)", "└─• cmd/uninstall.go", "cmdUninstall() :10",
		"Calls (2)", "├─• uninstall.go", "removeBinary() :7", "completionMessage() :8",
		"Uses (1)", "configPath :2",
		"Tests (1)", "TestRun() :13",
		"Related (1)", "Options :1",
		"Other matches (1)", "└─• notes.txt", "uninstall notes :4",
	} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("relationship tree missing %q:\n%s", expected, plain)
		}
	}
	if strings.Contains(plain, "Related (2)") {
		t.Fatalf("lexical match was rendered as a structural edge:\n%s", plain)
	}
}

func TestExplorerRelationshipTreeOmitsEmptyGroups(t *testing.T) {
	root, results := explorerFixture(t)
	model := newContextExplorer(root, "uninstall", results)
	plain := ansi.Strip(model.renderExplore(70, 24))
	for _, unwanted := range []string{"Called by", "Uses", "Tests", "Related", "Other matches"} {
		if strings.Contains(plain, unwanted) {
			t.Fatalf("empty group %q was rendered:\n%s", unwanted, plain)
		}
	}
	if !strings.Contains(plain, "Calls (2)") || !strings.Contains(plain, "removeBinary() :7") || !strings.Contains(plain, "completionMessage() :8") {
		t.Fatalf("call group missing:\n%s", plain)
	}
}

func TestExplorerShowsPreciseSCIPRelationshipGroups(t *testing.T) {
	root, results := explorerFixture(t)
	results[0].Symbols[0].Capability = discovery.CapabilityPrecise
	results[0].Symbols[0].Related = []discovery.RelatedSymbol{
		{Name: "Run()", Kind: "function", Path: "definition.go", Line: 8, Column: 6, Capability: discovery.CapabilityPrecise, Relation: "defined as Run()"},
		{Name: "main()", Kind: "function", Path: "main.go", Line: 12, Column: 6, Capability: discovery.CapabilityPrecise, Relation: "referenced by main()"},
		{Name: "worker", Kind: "type", Path: "worker.go", Line: 4, Column: 6, Capability: discovery.CapabilityPrecise, Relation: "implemented by worker"},
	}
	model := newContextExplorer(root, "uninstall", results)
	plain := ansi.Strip(model.renderExplore(80, 28))
	for _, expected := range []string{
		"Explore (relationships for Run)", "precise", "Defined (1)", "Run() :8",
		"Referenced by (1)", "main() :12", "Implementations (1)", "worker :4",
	} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("precise explorer missing %q:\n%s", expected, plain)
		}
	}
}

func TestExplorerDrillShowsCrossFileGoConnectors(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "code.go"), []byte(`package main

func vsCodeCommand() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "code_test.go"), []byte(`package main

func TestVSCodeCommandFallsBackToInsiders() {
	vsCodeCommand()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	results := []discovery.Result{
		{Path: "code.go", Line: 3, Column: 6, Symbols: []discovery.Symbol{{Name: "vsCodeCommand()", Kind: "function", Line: 3, Column: 6}}},
		{Path: "code_test.go", Line: 3, Column: 6, Symbols: []discovery.Symbol{{Name: "TestVSCodeCommandFallsBackToInsiders()", Kind: "function", Line: 3, Column: 6}}},
	}
	model := newContextExplorer(root, "test", results)
	model.Update(key(tea.KeyDown, ""))
	model.Update(key(tea.KeyEnter, ""))

	plain := ansi.Strip(model.renderExplore(80, 24))
	if !strings.Contains(plain, "Calls (1)") || !strings.Contains(plain, "vsCodeCommand() :3") {
		t.Fatalf("cross-file call connector missing:\n%s", plain)
	}
}

func TestExplorerShowsScriptLanguageConnectors(t *testing.T) {
	tests := []struct {
		name       string
		mainPath   string
		helperPath string
		main       string
		helper     string
	}{
		{
			name: "Python", mainPath: "main.py", helperPath: "helper.py",
			main:   "from helper import helper\n\ndef run():\n    return helper()\n",
			helper: "def helper():\n    return True\n",
		},
		{
			name: "TSX", mainPath: "panel.tsx", helperPath: "helper.ts",
			main:   "import { helper } from './helper';\n\nexport function Panel() { return <button onClick={() => helper()} />; }\n",
			helper: "export function helper(): boolean { return true; }\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, test.mainPath), []byte(test.main), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, test.helperPath), []byte(test.helper), 0o644); err != nil {
				t.Fatal(err)
			}
			position := strings.LastIndex(test.main, "helper()")
			line := strings.Count(test.main[:position], "\n") + 1
			lineStart := strings.LastIndex(test.main[:position], "\n") + 1
			column := len([]rune(test.main[lineStart:position])) + 1
			symbol, err := discovery.Explore(root, test.mainPath, line, column)
			if err != nil {
				t.Fatal(err)
			}
			model := newContextExplorer(root, "helper", []discovery.Result{{
				Path: test.mainPath, Line: symbol.Line, Column: symbol.Column, Symbols: []discovery.Symbol{symbol},
			}})
			plain := ansi.Strip(model.renderExplore(80, 24))
			if !strings.Contains(plain, "Calls (1)") || !strings.Contains(plain, "helper()") {
				t.Fatalf("script connector missing:\n%s", plain)
			}
		})
	}
}

func TestExplorerPreviewHeaderUsesKnownLanguageOnly(t *testing.T) {
	if language := previewLanguage("internal/uninstall/uninstall.go"); language != "Go" {
		t.Fatalf("Go language label = %q", language)
	}
	if language := previewLanguage("notes.unknown-extension"); language != "" {
		t.Fatalf("unsupported language label = %q", language)
	}

	root, results := explorerFixture(t)
	model := newContextExplorer(root, "uninstall", results)
	header := ansi.Strip(model.renderPreviewTitle(64))
	if !strings.Contains(header, "Code Preview · uninstall.go") {
		t.Fatalf("preview header = %q", header)
	}
	if status := ansi.Strip(model.renderPreviewStatus(64, 1, 8)); !strings.HasPrefix(status, "Go │ Tab Size: 4 │ LF") || !strings.HasSuffix(status, "1-8 of 8") {
		t.Fatalf("preview status = %q", status)
	}
}

func TestExplorerBreadcrumbTracksDrillHistoryAndBack(t *testing.T) {
	root, results := explorerFixture(t)
	model := newContextExplorer(root, "uninstall", results)
	model.query = ""
	model.history = []exploredSymbol{
		{Path: "uninstall.go", Symbol: discovery.Symbol{Name: "A()", Kind: "function", Line: 3, Column: 6}},
		{Path: "uninstall.go", Symbol: discovery.Symbol{Name: "B()", Kind: "function", Line: 7, Column: 6}},
	}
	model.current = exploredSymbol{Path: "uninstall.go", Symbol: discovery.Symbol{Name: "C()", Kind: "function", Line: 8, Column: 6}}
	model.rebuildItems()
	if breadcrumb := ansi.Strip(model.breadcrumb()); breadcrumb != "Codebase › A › B › C" {
		t.Fatalf("breadcrumb = %q", breadcrumb)
	}

	model.Update(key('b', "b"))
	if breadcrumb := ansi.Strip(model.breadcrumb()); breadcrumb != "Codebase › A › B" {
		t.Fatalf("breadcrumb after back = %q", breadcrumb)
	}
}

func TestExplorerPaneFocusChangesWithoutMovingSelection(t *testing.T) {
	root, results := explorerFixture(t)
	model := newContextExplorer(root, "uninstall", results)
	model.cursor = 1
	beforeExplore := model.renderExplore(60, 20)
	beforePreview := model.renderPreview(60, 20)
	model.Update(key(tea.KeyTab, ""))
	if model.focus != explorerPreviewPane || model.cursor != 1 {
		t.Fatalf("focus = %v cursor = %d", model.focus, model.cursor)
	}
	if beforeExplore == model.renderExplore(60, 20) || beforePreview == model.renderPreview(60, 20) {
		t.Fatal("switching focus did not change both pane focus states")
	}
}

func TestExplorerPreviewFocusScrollsCodeWithoutMovingSelection(t *testing.T) {
	root := t.TempDir()
	var source strings.Builder
	for line := 1; line <= 80; line++ {
		fmt.Fprintf(&source, "// line %d\n", line)
	}
	if err := os.WriteFile(filepath.Join(root, "long.go"), []byte(source.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	results := []discovery.Result{{
		Path: "long.go", Line: 30, Column: 1,
		Symbols: []discovery.Symbol{{Name: "Run()", Kind: "function", Line: 30, Column: 1, Capability: discovery.CapabilityStructural}},
	}}
	model := newContextExplorer(root, "run", results)
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	selected := model.cursor
	model.Update(key(tea.KeyTab, ""))

	initial := defaultPreviewTop(model.preview.line, len(model.preview.highlighted), model.previewCapacity())
	model.Update(key(tea.KeyDown, ""))
	if model.cursor != selected || model.previewTop != initial+1 {
		t.Fatalf("down = cursor:%d previewTop:%d want cursor:%d previewTop:%d", model.cursor, model.previewTop, selected, initial+1)
	}
	beforePage := model.previewTop
	model.Update(key(tea.KeyPgDown, ""))
	if model.previewTop <= beforePage {
		t.Fatalf("page down did not scroll: before=%d after=%d", beforePage, model.previewTop)
	}
	model.Update(key(tea.KeyHome, ""))
	if model.previewTop != 0 {
		t.Fatalf("home previewTop = %d", model.previewTop)
	}
	model.Update(key(tea.KeyEnd, ""))
	if want := len(model.preview.highlighted) - model.previewCapacity(); model.previewTop != want {
		t.Fatalf("end previewTop = %d want %d", model.previewTop, want)
	}
	if footer := ansi.Strip(model.renderFooter(100)); !strings.Contains(footer, "↑/↓ scroll") || !strings.Contains(footer, "PgUp/PgDn") {
		t.Fatalf("preview footer = %q", footer)
	}
}

func TestExplorerExplainsSCIPFallbackStatusWithoutWrappingFilename(t *testing.T) {
	root, results := explorerFixture(t)
	model := newContextExplorer(root, "uninstall", results)
	plain := ansi.Strip(model.renderExplore(72, 20))
	if !strings.Contains(plain, "search · no SCIP") || strings.Contains(plain, "\nindex.scip") {
		t.Fatalf("missing-index capability =\n%s", plain)
	}

	if err := os.WriteFile(filepath.Join(root, "index.scip"), []byte("invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	model = newContextExplorer(root, "uninstall", results)
	if plain := ansi.Strip(model.renderExplore(72, 20)); !strings.Contains(plain, "search · SCIP invalid") {
		t.Fatalf("invalid-index capability =\n%s", plain)
	}
}

func explorerFixture(t *testing.T) (string, []discovery.Result) {
	t.Helper()
	root := t.TempDir()
	content := `package uninstall

func Run() error {
	completionMessage()
	return removeBinary()
}
func removeBinary() error { return nil }
func completionMessage() {}
`
	if err := os.WriteFile(filepath.Join(root, "uninstall.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, []discovery.Result{{
		Path: "uninstall.go", Line: 3, Column: 6, Reasons: []string{"strong intent match"},
		Symbols: []discovery.Symbol{{
			Name: "Run()", Kind: "function", Line: 3, Column: 6, Reasons: []string{"matched discovery intent"},
			Related: []discovery.RelatedSymbol{
				{Name: "removeBinary()", Kind: "function", Line: 7, Column: 6, Relation: "calls removeBinary()"},
				{Name: "completionMessage()", Kind: "function", Line: 8, Column: 6, Relation: "calls completionMessage()"},
			},
		}},
	}}
}

func containsExplorerItem(items []explorerItem, name string) bool {
	for _, item := range items {
		if item.symbol.Name == name {
			return true
		}
	}
	return false
}

func explorerItemNames(items []explorerItem) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.symbol.Name)
	}
	return names
}

func assertTextOrder(t *testing.T, text string, values ...string) {
	t.Helper()
	position := -1
	for _, value := range values {
		next := strings.Index(text[position+1:], value)
		if next < 0 {
			t.Fatalf("%q missing after offset %d:\n%s", value, position, text)
		}
		position += next + 1
	}
}
