package main

import (
	"bytes"
	"fmt"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/TaylorEdgerton/spec-cli/internal/discovery"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/x/ansi"
)

const (
	explorerWideWidth    = 100
	explorerMaxMatches   = 5
	explorerMaxFile      = 512 * 1024
	explorerTabWidth     = 4
	explorerHeaderHeight = 5
	explorerInfoHeight   = 7
	explorerFooterHeight = 3
)

var (
	explorerBlue    = lipgloss.Color("#67B7E1")
	explorerPurple  = lipgloss.Color("#C58AF9")
	explorerTeal    = lipgloss.Color("#6FD3C4")
	explorerGreen   = lipgloss.Color("#89D185")
	explorerYellow  = lipgloss.Color("#E8C547")
	explorerMuted   = lipgloss.Color("#8393A7")
	explorerBorder  = lipgloss.Color("#52647A")
	explorerSurface = lipgloss.Color("#252B35")
	explorerViolet  = lipgloss.Color("#4B3A75")

	explorerTitleStyle    = lipgloss.NewStyle().Foreground(explorerBlue).Bold(true)
	explorerSymbolStyle   = lipgloss.NewStyle().Foreground(explorerPurple)
	explorerMutedStyle    = lipgloss.NewStyle().Foreground(explorerMuted)
	explorerSelectedStyle = lipgloss.NewStyle().Background(explorerViolet).Foreground(lipgloss.BrightWhite).Bold(true)
	explorerCurrentStyle  = lipgloss.NewStyle().Background(explorerSurface)
	explorerLineStyle     = lipgloss.NewStyle().Foreground(explorerYellow).Bold(true)
	explorerKeyStyle      = lipgloss.NewStyle().Foreground(explorerBlue).Bold(true)
	explorerEvidenceStyle = lipgloss.NewStyle().Foreground(explorerGreen)

	// ponytail: lipgloss has no dashed border preset, so declare the four runes.
	explorerDashedBorder = lipgloss.Border{
		Top: "┄", Bottom: "┄", Left: "┆", Right: "┆",
		TopLeft: "╭", TopRight: "╮", BottomLeft: "╰", BottomRight: "╯",
	}

	explorerChipColors = map[string]color.Color{
		"f": explorerBlue, "fn": explorerTeal, "v": explorerPurple,
		"c": explorerYellow, "t": explorerGreen,
	}
)

type exploredSymbol struct {
	Path   string
	Symbol discovery.Symbol
}

type explorerItemKind uint8

const (
	explorerRoot explorerItemKind = iota
	explorerRelationship
	explorerMatch
)

type explorerItem struct {
	kind     explorerItemKind
	path     string
	symbol   discovery.Symbol
	relation string
	reasons  []string
}

type explorerPane uint8

const (
	explorerExplorePane explorerPane = iota
	explorerPreviewPane
)

type explorerRenderRow struct {
	text      string
	itemIndex int
}

type relationshipGroup struct {
	kind  relationshipKind
	title string
	items []int
}

type relationshipSection struct {
	title  string
	groups []relationshipGroup
}

type relationshipKind uint8

const (
	relationshipCalledBy relationshipKind = iota
	relationshipReferencedBy
	relationshipTests
	relationshipCalls
	relationshipUses
	relationshipDefined
	relationshipImplementations
	relationshipRelated
)

type explorerPreview struct {
	path        string
	plain       []string
	highlighted []string
	line        int
	column      int
	crlf        bool
	err         error
}

type contextExplorerModel struct {
	root       string
	query      string
	editor     lineEditor
	querying   bool
	results    []discovery.Result
	current    exploredSymbol
	history    []exploredSymbol
	items      []explorerItem
	cursor     int
	preview    explorerPreview
	previewTop int
	previews   map[string]explorerPreview
	width      int
	height     int
	status     string
	focus      explorerPane
	precision  discovery.PrecisionAvailability
	help       bool
	done       bool
	stopped    bool
	find       func(string, string) ([]discovery.Result, error)
	explore    func(string, string, int, int) (discovery.Symbol, error)
	open       func(string, discovery.Result) error
}

func newContextExplorer(root, query string, results []discovery.Result) *contextExplorerModel {
	model := &contextExplorerModel{
		root: root, query: strings.TrimSpace(query), editor: newLineEditor(query), results: results,
		width: 120, height: 36, previewTop: -1, previews: make(map[string]explorerPreview),
		precision: discovery.SCIPAvailability(root),
		find: func(root, query string) ([]discovery.Result, error) {
			return discovery.Find(root, discovery.Query{Intent: query})
		},
		explore: discovery.Explore,
		open:    openInVSCode,
	}
	if model.query == "" {
		model.querying = true
	} else {
		model.setResults(results)
	}
	return model
}

func runContextExplorer(root, query string, results []discovery.Result, input io.Reader, output io.Writer) (bool, error) {
	final, err := tea.NewProgram(newContextExplorer(root, query, results), tea.WithInput(input), tea.WithOutput(output)).Run()
	if err != nil {
		return false, err
	}
	return final.(*contextExplorerModel).stopped, nil
}

func (model *contextExplorerModel) Init() tea.Cmd { return nil }

func (model *contextExplorerModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		model.width, model.height = message.Width, message.Height
		return model, nil
	case tea.InterruptMsg:
		model.done, model.stopped = true, true
		return model, tea.Quit
	case tea.PasteMsg:
		if model.querying {
			model.editor.insert(message.Content)
		}
		return model, nil
	case tea.KeyPressMsg:
		if model.querying {
			return model.updateQuery(message)
		}
		return model.updateBrowse(message)
	}
	return model, nil
}

func (model *contextExplorerModel) updateQuery(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch message.Keystroke() {
	case "ctrl+c":
		model.done, model.stopped = true, true
		return model, tea.Quit
	case "esc":
		if len(model.results) == 0 {
			model.done, model.stopped = true, true
			return model, tea.Quit
		}
		model.querying = false
		model.editor = newLineEditor(model.query)
	case "enter":
		query := strings.TrimSpace(model.editor.value)
		if query == "" {
			return model, nil
		}
		results, err := model.find(model.root, query)
		if err != nil {
			model.status = "Discovery unavailable: " + err.Error()
			return model, nil
		}
		model.query, model.querying, model.status = query, false, ""
		model.setResults(results)
	default:
		model.editor.key(message)
	}
	return model, nil
}

func (model *contextExplorerModel) updateBrowse(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch message.Keystroke() {
	case "ctrl+c", "esc":
		model.done, model.stopped = true, true
		return model, tea.Quit
	case "q":
		model.done = true
		return model, tea.Quit
	case "/":
		model.querying = true
		model.editor = newLineEditor("")
		model.status = ""
	case "up", "k":
		if model.focus == explorerPreviewPane {
			model.scrollPreview(-1)
		} else {
			model.moveCursor(-1)
		}
	case "down", "j":
		if model.focus == explorerPreviewPane {
			model.scrollPreview(1)
		} else {
			model.moveCursor(1)
		}
	case "pgup":
		if model.focus == explorerPreviewPane {
			model.scrollPreview(-model.previewCapacity())
		}
	case "pgdown":
		if model.focus == explorerPreviewPane {
			model.scrollPreview(model.previewCapacity())
		}
	case "home":
		if model.focus == explorerPreviewPane {
			model.previewTop = 0
		}
	case "end":
		if model.focus == explorerPreviewPane {
			model.previewTop = max(0, len(model.preview.highlighted)-model.previewCapacity())
		}
	case "tab", "shift+tab":
		if model.focus == explorerExplorePane {
			model.focus = explorerPreviewPane
		} else {
			model.focus = explorerExplorePane
		}
	case "enter":
		model.drill()
	case "b", "backspace":
		if len(model.history) == 0 {
			model.done = true
			return model, tea.Quit
		}
		model.current = model.history[len(model.history)-1]
		model.history = model.history[:len(model.history)-1]
		model.cursor, model.status = 0, ""
		model.rebuildItems()
	case "o":
		model.openSelected()
	case "e":
		model.openSelectedInSystemEditor()
	case "c":
		if location, ok := model.selectedLocation(); ok {
			model.status = "Copied " + location
			return model, tea.SetClipboard(location)
		}
	case "?":
		model.help = !model.help
	}
	return model, nil
}

// moveCursor walks the items. Index 0 is the root symbol, which has no tree row
// of its own: it is named by the pane title and described by the Symbol Info
// panel, so landing on it simply leaves the tree unhighlighted.
func (model *contextExplorerModel) moveCursor(delta int) {
	if len(model.items) == 0 {
		return
	}
	model.cursor = wrap(model.cursor+delta, len(model.items))
	model.syncPreview()
}

func (model *contextExplorerModel) selectedLocation() (string, bool) {
	item, ok := model.selectedItem()
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%s:%d", item.path, max(1, item.symbol.Line)), true
}

func (model *contextExplorerModel) openSelectedInSystemEditor() {
	item, ok := model.selectedItem()
	if !ok {
		return
	}
	path, err := safeCodePath(model.root, item.path)
	if err != nil {
		model.status = "Could not open: " + err.Error()
		return
	}
	command, err := directoryOpenCommand(path)
	if err == nil {
		err = command.Start()
	}
	if err != nil {
		model.status = "Could not open in system editor: " + err.Error()
		return
	}
	_ = command.Process.Release()
	model.status = "Opened " + item.path
}

func (model *contextExplorerModel) drill() {
	item, ok := model.selectedItem()
	if !ok || (item.path == model.current.Path && sameSymbolLocation(item.symbol, model.current.Symbol)) {
		return
	}
	symbol, err := model.explore(model.root, item.path, item.symbol.Line, item.symbol.Column)
	if err != nil {
		symbol = item.symbol
		symbol.Related = nil
	}
	model.history = append(model.history, model.current)
	model.current = exploredSymbol{Path: item.path, Symbol: symbol}
	model.cursor, model.status = 0, ""
	model.rebuildItems()
}

func (model *contextExplorerModel) openSelected() {
	item, ok := model.selectedItem()
	if !ok {
		return
	}
	err := model.open(model.root, discovery.Result{
		Path: item.path, Line: item.symbol.Line, Column: item.symbol.Column,
	})
	if err != nil {
		model.status = "Could not open in VS Code: " + err.Error()
		return
	}
	model.status = "Opened " + discoveryLocation(discovery.Result{Path: item.path, Line: item.symbol.Line, Column: item.symbol.Column})
}

func (model *contextExplorerModel) setResults(results []discovery.Result) {
	model.results, model.history, model.cursor = results, nil, 0
	model.previews = make(map[string]explorerPreview)
	current, ok := defaultExploredSymbol(results)
	if !ok {
		model.current = exploredSymbol{}
		model.items = nil
		model.preview = explorerPreview{err: fmt.Errorf("no likely code context found")}
		return
	}
	model.current = current
	model.rebuildItems()
}

func defaultExploredSymbol(results []discovery.Result) (exploredSymbol, bool) {
	for _, result := range results {
		if len(result.Symbols) > 0 {
			return exploredSymbol{Path: result.Path, Symbol: result.Symbols[0]}, true
		}
		if result.Path != "" {
			return exploredSymbol{Path: result.Path, Symbol: resultSymbol(result)}, true
		}
	}
	return exploredSymbol{}, false
}

func resultSymbol(result discovery.Result) discovery.Symbol {
	name := filepath.Base(result.Path)
	if result.Preview != "" {
		name = boundedSummary(result.Preview)
	}
	return discovery.Symbol{
		Name: name, Kind: "file", Line: max(1, result.Line), Column: max(1, result.Column),
		Capability: discovery.CapabilitySearch, Reasons: result.Reasons,
	}
}

func (model *contextExplorerModel) rebuildItems() {
	current := explorerItem{
		kind: explorerRoot, path: model.current.Path, symbol: model.current.Symbol,
		reasons: append([]string(nil), model.current.Symbol.Reasons...),
	}
	items := []explorerItem{current}
	var relationships []explorerItem
	for _, related := range model.current.Symbol.Related {
		path := related.Path
		if path == "" {
			path = model.current.Path
		}
		relationships = append(relationships, explorerItem{
			kind: explorerRelationship, path: path,
			symbol: discovery.Symbol{
				Name: related.Name, Kind: related.Kind, Line: related.Line, Column: related.Column, Capability: related.Capability,
			},
			relation: related.Relation, reasons: []string{related.Relation},
		})
	}
	for _, section := range relationshipSectionsFor(relationships) {
		for _, group := range section.groups {
			for _, index := range group.items {
				items = append(items, relationships[index])
			}
		}
	}
	matches := 0
	for _, result := range model.results {
		symbols := result.Symbols
		if len(symbols) == 0 {
			symbols = []discovery.Symbol{resultSymbol(result)}
		}
		for _, symbol := range symbols {
			if result.Path == model.current.Path && sameSymbolLocation(symbol, model.current.Symbol) {
				continue
			}
			if containsExplorerLocation(items, result.Path, symbol) {
				continue
			}
			reasons := append([]string(nil), symbol.Reasons...)
			reasons = append(reasons, result.Reasons...)
			items = append(items, explorerItem{kind: explorerMatch, path: result.Path, symbol: symbol, reasons: reasons})
			matches++
			if matches == explorerMaxMatches {
				break
			}
		}
		if matches == explorerMaxMatches {
			break
		}
	}
	model.items = items
	model.cursor = clamp(model.cursor, 0, len(model.items)-1)
	model.syncPreview()
}

func containsExplorerLocation(items []explorerItem, path string, symbol discovery.Symbol) bool {
	for _, item := range items {
		if item.path == path && sameSymbolLocation(item.symbol, symbol) {
			return true
		}
	}
	return false
}

func sameSymbolLocation(left, right discovery.Symbol) bool {
	return left.Name == right.Name && left.Line == right.Line && left.Column == right.Column
}

func (model *contextExplorerModel) selectedItem() (explorerItem, bool) {
	if model.cursor < 0 || model.cursor >= len(model.items) {
		return explorerItem{}, false
	}
	return model.items[model.cursor], true
}

func (model *contextExplorerModel) syncPreview() {
	item, ok := model.selectedItem()
	if !ok {
		model.preview = explorerPreview{err: fmt.Errorf("no code selected")}
		return
	}
	preview, ok := model.previews[item.path]
	if !ok {
		preview = loadExplorerPreview(model.root, item.path, item.symbol.Line, item.symbol.Column)
		model.previews[item.path] = preview
	}
	preview.line, preview.column = max(1, item.symbol.Line), max(1, item.symbol.Column)
	model.preview = preview
	model.previewTop = -1
}

func (model *contextExplorerModel) scrollPreview(delta int) {
	if model.preview.err != nil || len(model.preview.highlighted) == 0 {
		return
	}
	capacity := model.previewCapacity()
	if model.previewTop < 0 {
		model.previewTop = defaultPreviewTop(model.preview.line, len(model.preview.highlighted), capacity)
	}
	model.previewTop = clamp(model.previewTop+delta, 0, max(0, len(model.preview.highlighted)-capacity))
}

func (model *contextExplorerModel) previewCapacity() int {
	width, height := model.width, model.height
	if width <= 0 {
		width = 120
	}
	if height <= 0 {
		height = 36
	}
	bodyHeight := max(8, height-explorerHeaderHeight-explorerInfoHeight-explorerFooterHeight)
	panelHeight := bodyHeight
	if width < explorerWideWidth {
		exploreHeight := min(10, max(6, bodyHeight/3))
		panelHeight = max(6, bodyHeight-exploreHeight)
	}
	return max(1, panelHeight-3)
}

func loadExplorerPreview(root, relative string, line, column int) explorerPreview {
	preview := explorerPreview{path: relative, line: max(1, line), column: max(1, column)}
	path, err := safeCodePath(root, relative)
	if err != nil {
		preview.err = err
		return preview
	}
	data, err := os.ReadFile(path)
	if err != nil {
		preview.err = err
		return preview
	}
	if len(data) > explorerMaxFile {
		data = data[:explorerMaxFile]
	}
	if bytes.IndexByte(data, 0) >= 0 {
		preview.err = fmt.Errorf("binary file preview is unavailable")
		return preview
	}
	preview.crlf = bytes.Contains(data, []byte("\r\n"))
	source := strings.ReplaceAll(string(data), "\r\n", "\n")
	// Tabs measure as one cell but render as many, which wraps the pane.
	source = strings.ReplaceAll(source, "\t", strings.Repeat(" ", explorerTabWidth))
	preview.plain = splitPreviewLines(source)
	preview.highlighted = highlightPreview(relative, source)
	if len(preview.highlighted) != len(preview.plain) {
		preview.highlighted = append([]string(nil), preview.plain...)
	}
	return preview
}

func splitPreviewLines(source string) []string {
	lines := strings.Split(source, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func highlightPreview(path, source string) []string {
	lexer := lexers.Match(path)
	if lexer == nil {
		lexer = lexers.Analyse(source)
	}
	if lexer == nil {
		return splitPreviewLines(source)
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, source)
	if err != nil {
		return splitPreviewLines(source)
	}
	style := styles.Get("dracula")
	if style == nil {
		style = styles.Fallback
	}
	var highlighted bytes.Buffer
	if err := formatters.TTY16m.Format(&highlighted, style, iterator); err != nil {
		return splitPreviewLines(source)
	}
	return splitPreviewLines(highlighted.String())
}

func (model *contextExplorerModel) View() tea.View {
	if model.done {
		view := tea.NewView("")
		view.AltScreen = true
		return view
	}
	width, height := model.width, model.height
	if width <= 0 {
		width = 120
	}
	if height <= 0 {
		height = 36
	}
	header := model.renderHeader(width)
	footer := model.renderFooter(width)
	info := model.renderInfoRow(width)
	bodyHeight := max(8, height-lipgloss.Height(header)-lipgloss.Height(info)-lipgloss.Height(footer))
	var body string
	if width >= explorerWideWidth {
		leftWidth := max(32, width*2/5)
		rightWidth := width - leftWidth
		left := model.renderExplore(leftWidth, bodyHeight)
		right := model.renderPreview(rightWidth, bodyHeight)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	} else {
		exploreHeight := min(10, max(6, bodyHeight/3))
		previewHeight := max(6, bodyHeight-exploreHeight)
		body = lipgloss.JoinVertical(lipgloss.Left,
			model.renderExplore(width, exploreHeight),
			model.renderPreview(width, previewHeight),
		)
	}
	view := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, header, body, info, footer))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeNone
	return view
}

func (model *contextExplorerModel) renderHeader(width int) string {
	query := model.query
	if model.querying {
		query = model.editor.view()
	}
	inner := max(10, width-4)
	badge := ""
	if !model.querying && width >= explorerWideWidth {
		badge = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).BorderForeground(explorerBorder).
			Padding(0, 1).Foreground(explorerMuted).Render("One-hop relationships")
	}
	queryLine := explorerTitleStyle.Render("Query: ") + query
	breadcrumb := model.breadcrumb()
	if model.querying {
		breadcrumb = explorerMutedStyle.Render("Type a repository question and press Enter")
	}
	body := queryLine + "\n" + breadcrumb
	if badge != "" {
		body = explorerColumns(body, max(10, inner-lipgloss.Width(badge)-1), badge)
	}
	return explorerPanel(width, explorerHeaderHeight, explorerBlue,
		explorerTitleStyle.Render("Codebase Context"), "", body)
}

func (model *contextExplorerModel) breadcrumb() string {
	parts := []string{"Codebase"}
	appendPart := func(part string) {
		part = strings.TrimSpace(part)
		if part == "" {
			return
		}
		if len(parts) > 1 && strings.EqualFold(strings.TrimSuffix(parts[len(parts)-1], "()"), strings.TrimSuffix(part, "()")) {
			return
		}
		parts = append(parts, part)
	}
	appendPart(model.query)
	for _, entry := range model.history {
		appendPart(strings.TrimSuffix(entry.Symbol.Name, "()"))
	}
	if model.current.Symbol.Name != "" {
		appendPart(strings.TrimSuffix(model.current.Symbol.Name, "()"))
	}
	if len(parts) == 1 {
		return explorerMutedStyle.Render(parts[0])
	}
	prefix := explorerMutedStyle.Render(strings.Join(parts[:len(parts)-1], " › ") + " › ")
	return prefix + explorerSymbolStyle.Bold(true).Render(parts[len(parts)-1])
}

func (model *contextExplorerModel) renderExplore(width, height int) string {
	innerWidth := max(10, width-4)
	available := max(1, height-3) // one content row is reserved for the legend
	rows := model.exploreRows()
	selectedRow := explorerSelectedRow(rows, model.cursor)
	start := visibleWindow(selectedRow, len(rows), available)
	end := min(len(rows), start+available)
	var lines []string
	for index := start; index < end; index++ {
		row := rows[index]
		line := ansi.Truncate(row.text, innerWidth, "…")
		if row.itemIndex >= 0 && row.itemIndex == model.cursor {
			line = explorerSelectedStyle.Width(innerWidth).Render(line)
		}
		lines = append(lines, line)
	}
	if len(model.items) == 0 {
		lines = append(lines, explorerMutedStyle.Render("No likely context found. Press / to try another query."))
	}
	for len(lines) < available {
		lines = append(lines, "")
	}
	lines = append(lines, explorerLegend(innerWidth))
	return explorerPanel(width, height, explorerPaneBorder(model.focus == explorerExplorePane),
		explorerPaneTitle(model.exploreTitle(), model.focus == explorerExplorePane),
		explorerMutedStyle.Render(model.capabilityLabel()), strings.Join(lines, "\n"))
}

func (model *contextExplorerModel) exploreTitle() string {
	name := strings.TrimSuffix(model.current.Symbol.Name, "()")
	if name == "" {
		return "Explore"
	}
	return "Explore (relationships for " + name + ")"
}

func (model *contextExplorerModel) capabilityLabel() string {
	capability := string(model.current.Symbol.Capability)
	if capability == "" {
		capability = string(discovery.CapabilitySearch)
	}
	if model.current.Symbol.Capability == discovery.CapabilityPrecise {
		return capability
	}
	switch model.precision {
	case discovery.PrecisionUnavailable:
		return capability + " · no SCIP"
	case discovery.PrecisionInvalid:
		return capability + " · SCIP invalid"
	case discovery.PrecisionStale:
		return capability + " · SCIP stale"
	case discovery.PrecisionAvailable:
		return capability + " · SCIP unmatched"
	}
	return capability
}

// explorerLegend drops detail as the pane narrows: labelled, then plain, then
// abbreviated, rather than letting an ellipsis eat the last chip.
func explorerLegend(width int) string {
	build := func(labels []string) string {
		chips := make([]string, 0, len(labels))
		for index, kind := range []string{"file", "function", "variable", "test"} {
			chips = append(chips, explorerChip(kind)+explorerMutedStyle.Render(" "+labels[index]))
		}
		return strings.Join(chips, "  ")
	}
	full := build([]string{"file", "function", "variable", "test"})
	for _, candidate := range []string{
		explorerMutedStyle.Render("Legend: ") + full,
		full,
		build([]string{"file", "func", "var", "test"}),
	} {
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
	}
	return ansi.Truncate(build([]string{"", "", "", ""}), width, "…")
}

func (model *contextExplorerModel) exploreRows() []explorerRenderRow {
	if len(model.items) == 0 {
		return nil
	}
	var rows []explorerRenderRow
	addGroup := func(title string, indexes []int) {
		if len(rows) > 0 {
			rows = append(rows, explorerRenderRow{itemIndex: -1})
		}
		rows = append(rows, explorerRenderRow{
			text:      explorerMutedStyle.Render("▼ ") + explorerTitleStyle.Render(title) + explorerMutedStyle.Render(fmt.Sprintf(" (%d)", len(indexes))),
			itemIndex: -1,
		})
		for position, itemIndex := range indexes {
			connector, continuation := "├─• ", "│     "
			if position == len(indexes)-1 {
				connector, continuation = "└─• ", "      "
			}
			item := model.items[itemIndex]
			rows = append(rows,
				explorerRenderRow{text: explorerMutedStyle.Render(connector) + item.path, itemIndex: itemIndex},
				explorerRenderRow{
					text: explorerMutedStyle.Render(continuation) + explorerSymbolStyle.Render(item.symbol.Name) +
						explorerLineStyle.Render(fmt.Sprintf(" :%d", max(1, item.symbol.Line))),
					itemIndex: itemIndex,
				},
			)
		}
	}
	for _, section := range model.relationshipSections() {
		for _, group := range section.groups {
			addGroup(group.title, group.items)
		}
	}
	var matches []int
	for index, item := range model.items {
		if item.kind == explorerMatch {
			matches = append(matches, index)
		}
	}
	if len(matches) > 0 {
		addGroup("Other matches", matches)
	}
	return rows
}

func (model *contextExplorerModel) relationshipSections() []relationshipSection {
	return relationshipSectionsFor(model.items)
}

func relationshipSectionsFor(items []explorerItem) []relationshipSection {
	sections := []relationshipSection{
		{title: "Incoming", groups: []relationshipGroup{
			{kind: relationshipCalledBy, title: "Called by"},
			{kind: relationshipReferencedBy, title: "Referenced by"},
			{kind: relationshipTests, title: "Tests"},
		}},
		{title: "Outgoing", groups: []relationshipGroup{
			{kind: relationshipCalls, title: "Calls"},
			{kind: relationshipUses, title: "Uses"},
		}},
		{title: "Related", groups: []relationshipGroup{
			{kind: relationshipDefined, title: "Defined"},
			{kind: relationshipImplementations, title: "Implementations"},
			{kind: relationshipRelated, title: "Related"},
		}},
	}
	for index, item := range items {
		if item.kind != explorerRelationship {
			continue
		}
		kind := relationshipKindFor(item)
		for sectionIndex := range sections {
			for groupIndex := range sections[sectionIndex].groups {
				if sections[sectionIndex].groups[groupIndex].kind == kind {
					sections[sectionIndex].groups[groupIndex].items = append(sections[sectionIndex].groups[groupIndex].items, index)
				}
			}
		}
	}
	var present []relationshipSection
	for _, section := range sections {
		var groups []relationshipGroup
		for _, group := range section.groups {
			if len(group.items) > 0 {
				groups = append(groups, group)
			}
		}
		if len(groups) > 0 {
			section.groups = groups
			present = append(present, section)
		}
	}
	return present
}

func relationshipKindFor(item explorerItem) relationshipKind {
	relation := strings.ToLower(strings.TrimSpace(item.relation))
	path := strings.ToLower(filepath.ToSlash(item.path))
	name := strings.ToLower(item.symbol.Name)
	switch {
	case item.symbol.Kind == "test", strings.Contains(path, "_test."), strings.HasPrefix(name, "test"), strings.HasPrefix(relation, "tests "):
		return relationshipTests
	case strings.HasPrefix(relation, "defined "):
		return relationshipDefined
	case strings.HasPrefix(relation, "referenced by "), strings.HasPrefix(relation, "references "):
		return relationshipReferencedBy
	case strings.HasPrefix(relation, "implemented by "), strings.HasPrefix(relation, "implements "):
		return relationshipImplementations
	case strings.HasPrefix(relation, "called by "):
		return relationshipCalledBy
	case strings.HasPrefix(relation, "calls "):
		return relationshipCalls
	case strings.HasPrefix(relation, "uses "), strings.HasPrefix(relation, "receives "):
		return relationshipUses
	default:
		return relationshipRelated
	}
}

func explorerSelectedRow(rows []explorerRenderRow, cursor int) int {
	for index, row := range rows {
		if row.itemIndex == cursor {
			return index
		}
	}
	return 0
}

func (model *contextExplorerModel) renderPreview(width, height int) string {
	innerWidth := max(10, width-4)
	focused := model.focus == explorerPreviewPane
	border := explorerPaneBorder(focused)
	title := model.renderPreviewTitle(innerWidth - 8)
	if model.preview.err != nil {
		return explorerPanel(width, height, border, title, "", explorerMutedStyle.Render(model.preview.err.Error()))
	}
	available := max(1, height-3) // one content row is reserved for the status bar
	start := model.previewTop
	if start < 0 {
		start = defaultPreviewTop(model.preview.line, len(model.preview.highlighted), available)
	}
	start = clamp(start, 0, max(0, len(model.preview.highlighted)-available))
	end := min(len(model.preview.highlighted), start+available)
	digits := len(fmt.Sprintf("%d", max(1, end)))
	var lines []string
	for index := start; index < end; index++ {
		number := fmt.Sprintf("%*d", digits, index+1)
		marker := " "
		current := index+1 == model.preview.line
		if current {
			marker, number = explorerLineStyle.Render("›"), explorerLineStyle.Render(number)
		} else {
			number = explorerMutedStyle.Render(number)
		}
		gutter := marker + " " + number + " " + explorerMutedStyle.Render("│") + " "
		codeWidth := max(1, innerWidth-lipgloss.Width(gutter))
		line := gutter + ansi.Truncate(model.preview.highlighted[index], codeWidth, "…")
		if current {
			line = explorerCurrentStyle.Width(innerWidth).Render(line)
		}
		lines = append(lines, line)
	}
	for len(lines) < available {
		lines = append(lines, "")
	}
	lines = append(lines, model.renderPreviewStatus(innerWidth, start+1, end))
	return explorerPanel(width, height, border, title,
		explorerMutedStyle.Render(fmt.Sprintf("%d:%d", model.preview.line, model.preview.column)),
		strings.Join(lines, "\n"))
}

func (model *contextExplorerModel) renderPreviewTitle(width int) string {
	title := explorerPaneTitle("Code Preview", model.focus == explorerPreviewPane)
	if model.preview.path != "" {
		title += explorerMutedStyle.Render(" · ") + explorerSymbolStyle.Render(model.preview.path)
	}
	return ansi.Truncate(title, max(1, width), "…")
}

func (model *contextExplorerModel) renderPreviewStatus(width, first, last int) string {
	separator := explorerMutedStyle.Render(" │ ")
	var fields []string
	if language := previewLanguage(model.preview.path); language != "" {
		fields = append(fields, language)
	}
	eol := "LF"
	if model.preview.crlf {
		eol = "CRLF"
	}
	fields = append(fields, fmt.Sprintf("Tab Size: %d", explorerTabWidth), eol)
	for index := range fields {
		fields[index] = explorerMutedStyle.Render(fields[index])
	}
	left := strings.Join(fields, separator)
	right := explorerMutedStyle.Render(fmt.Sprintf("%d-%d of %d", first, last, len(model.preview.highlighted)))
	return explorerSplit(left, right, width)
}

func previewLanguage(path string) string {
	lexer := lexers.Match(path)
	if lexer == nil || lexer.Config() == nil {
		return ""
	}
	name := lexer.Config().Name
	if strings.EqualFold(name, "plaintext") || strings.EqualFold(name, "text") {
		return ""
	}
	return name
}

func (model *contextExplorerModel) renderInfoRow(width int) string {
	leftWidth := max(24, width/2)
	return lipgloss.JoinHorizontal(lipgloss.Top,
		model.renderSymbolInfo(leftWidth, explorerInfoHeight),
		model.renderEvidence(width-leftWidth, explorerInfoHeight),
	)
}

func (model *contextExplorerModel) renderSymbolInfo(width, height int) string {
	inner := max(10, width-4)
	item, ok := model.selectedItem()
	if !ok {
		return explorerPanel(width, height, explorerYellow, explorerTitleStyle.Render("Symbol Info"), "",
			explorerMutedStyle.Render("No symbol selected"))
	}
	signature, doc := model.symbolDetail(item)
	if signature == "" {
		signature = item.symbol.Name
	}
	lines := []string{
		explorerChip(item.symbol.Kind) + " " + explorerSymbolStyle.Bold(true).Render(signature),
		explorerMutedStyle.Render("File: ") + fmt.Sprintf("%s:%d", item.path, max(1, item.symbol.Line)),
	}
	if doc != "" {
		lines = append(lines, explorerMutedStyle.Render("Doc: ")+doc)
	}
	if model.status != "" {
		lines = append(lines, explorerLineStyle.Render(model.status))
	}
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], inner, "…")
	}
	return explorerPanel(width, height, explorerYellow, explorerTitleStyle.Render("Symbol Info"), "", strings.Join(lines, "\n"))
}

// symbolDetail reads the signature and doc comment straight out of the preview
// that is already in memory, so no provider has to carry them.
// ponytail: comment-above-definition only; revisit if Python matters.
func (model *contextExplorerModel) symbolDetail(item explorerItem) (string, string) {
	if model.preview.err != nil || len(model.preview.plain) == 0 || model.preview.path != item.path {
		return "", ""
	}
	index := clamp(max(1, item.symbol.Line)-1, 0, len(model.preview.plain)-1)
	signature := strings.TrimSpace(model.preview.plain[index])
	if body := strings.Index(signature, "{"); body > 0 {
		signature = strings.TrimSpace(signature[:body])
	}
	for _, keyword := range []string{"func ", "def ", "function ", "class ", "type ", "fn "} {
		signature = strings.TrimPrefix(signature, keyword)
	}
	var doc []string
	for above := index - 1; above >= 0; above-- {
		text := strings.TrimSpace(model.preview.plain[above])
		if !isCommentLine(text) {
			break
		}
		doc = append([]string{strings.TrimSpace(strings.TrimLeft(text, "/#*-;! "))}, doc...)
	}
	return signature, strings.TrimSpace(strings.Join(doc, " "))
}

func isCommentLine(text string) bool {
	for _, prefix := range []string{"//", "#", "/*", "*", "--", ";;", "\"\"\""} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func (model *contextExplorerModel) renderEvidence(width, height int) string {
	inner := max(10, width-4)
	hints := explorerKeyHints([][2]string{{"o", "VS Code"}, {"e", "System Editor"}, {"c", "Copy path:line"}}, "\n")
	openIn := explorerPanelWith(explorerDashedBorder, lipgloss.Width(hints)+4, explorerInfoHeight-2,
		explorerBorder, explorerMutedStyle.Render("Open in"), "", hints)
	if lipgloss.Width(openIn) > inner*2/3 {
		openIn = ""
	}
	var bullets []string
	if item, ok := model.selectedItem(); ok {
		for _, reason := range uniqueReasons(item.reasons) {
			bullets = append(bullets, explorerEvidenceStyle.Render("• ")+reason)
		}
	}
	if len(bullets) == 0 {
		bullets = []string{explorerMutedStyle.Render("No evidence recorded")}
	}
	body := strings.Join(bullets, "\n")
	if openIn != "" {
		body = explorerColumns(body, max(6, inner-lipgloss.Width(openIn)-1), openIn)
	}
	return explorerPanel(width, height, explorerYellow, explorerTitleStyle.Render("Evidence"), "", body)
}

func explorerKeyHints(pairs [][2]string, separator string) string {
	hints := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		hints = append(hints, explorerKeyStyle.Render(pair[0])+" "+explorerMutedStyle.Render(pair[1]))
	}
	return strings.Join(hints, separator)
}

func (model *contextExplorerModel) renderFooter(width int) string {
	inner := max(10, width-4)
	hints := [][2]string{
		{"↑/↓", "navigate"}, {"enter", "explore"}, {"tab", "focus"},
		{"o", "open"}, {"/", "new query"}, {"b", "back"}, {"q", "quit"},
	}
	switch {
	case model.querying:
		hints = [][2]string{{"enter", "search"}, {"esc", "cancel"}, {"ctrl+c", "exit"}}
		if len(model.results) == 0 {
			hints = [][2]string{{"enter", "search"}, {"esc", "exit"}, {"ctrl+c", "exit"}}
		}
	case model.help:
		hints = [][2]string{
			{"↑/↓", "navigate"}, {"enter", "explore"}, {"tab", "focus"}, {"o", "VS Code"},
			{"e", "system editor"}, {"c", "copy path:line"}, {"/", "new query"}, {"b", "back"}, {"q", "quit"},
		}
	case model.focus == explorerPreviewPane:
		hints = [][2]string{
			{"↑/↓", "scroll"}, {"PgUp/PgDn", "page"}, {"home/end", "jump"},
			{"tab", "focus"}, {"o", "open"}, {"b", "back"}, {"q", "quit"},
		}
	}
	right := explorerMutedStyle.Render("Press ? for help")
	if model.help {
		right = explorerMutedStyle.Render("Press ? to close")
	}
	return explorerPanel(width, explorerFooterHeight, explorerBorder, "", "",
		explorerSplit(explorerKeyHints(hints, "   "), right, inner))
}

func defaultPreviewTop(line, total, available int) int {
	start := max(0, line-1-available/3)
	if start+available > total {
		start = max(0, total-available)
	}
	return start
}

func explorerPaneTitle(title string, focused bool) string {
	if focused {
		return explorerTitleStyle.Render(title)
	}
	return explorerMutedStyle.Render(title)
}

func explorerPaneBorder(focused bool) color.Color {
	if focused {
		return explorerBlue
	}
	return explorerBorder
}

func symbolKindLabel(kind string) string {
	switch strings.ToLower(kind) {
	case "function", "method":
		return "fn"
	case "variable", "field", "parameter":
		return "v"
	case "constant":
		return "c"
	case "test":
		return "t"
	case "file", "":
		return "f"
	default:
		return kind
	}
}

func explorerChip(kind string) string {
	label := symbolKindLabel(kind)
	foreground, ok := explorerChipColors[label]
	if !ok {
		foreground = explorerMuted
	}
	return lipgloss.NewStyle().Background(explorerSurface).Foreground(foreground).Bold(true).Render(" " + label + " ")
}

// explorerColumns lays two blocks side by side at a fixed left width. Doing it
// by hand keeps every row exactly leftWidth+1+rightWidth cells, which is what
// stops the panel from soft-wrapping a row and shunting the layout down.
func explorerColumns(left string, leftWidth int, right string) string {
	leftLines, rightLines := strings.Split(left, "\n"), strings.Split(right, "\n")
	rows := make([]string, max(len(leftLines), len(rightLines)))
	for index := range rows {
		text := ""
		if index < len(leftLines) {
			text = ansi.Truncate(leftLines[index], leftWidth, "…")
		}
		rows[index] = text + strings.Repeat(" ", max(0, leftWidth-lipgloss.Width(text)))
		if index < len(rightLines) {
			rows[index] += " " + rightLines[index]
		}
	}
	return strings.Join(rows, "\n")
}

// explorerSplit pins right against the far edge of width, left against the near one.
func explorerSplit(left, right string, width int) string {
	right = ansi.Truncate(right, max(1, width/2), "…")
	left = ansi.Truncate(left, max(1, width-lipgloss.Width(right)-1), "…")
	return left + strings.Repeat(" ", max(1, width-lipgloss.Width(left)-lipgloss.Width(right))) + right
}

// explorerPanel draws a bordered panel whose title and right-hand label sit in
// the top border rule, which lipgloss has no primitive for.
func explorerPanel(width, height int, border color.Color, title, right, body string) string {
	return explorerPanelWith(lipgloss.RoundedBorder(), width, height, border, title, right, body)
}

func explorerPanelWith(runes lipgloss.Border, width, height int, border color.Color, title, right, body string) string {
	rendered := lipgloss.NewStyle().
		Border(runes).
		BorderForeground(border).
		Padding(0, 1).
		Width(max(1, width)).   // lipgloss counts the border in Width…
		Height(max(1, height)). // …and in Height, so these are the outer size.
		MaxWidth(max(1, width)).
		Render(body)
	if title == "" && right == "" {
		return rendered
	}
	lines := strings.Split(rendered, "\n")
	inner := max(2, lipgloss.Width(lines[0])-2)
	rule := lipgloss.NewStyle().Foreground(border)
	if title != "" {
		title = " " + title + " "
	}
	if right != "" {
		right = " " + right + " "
	}
	if lipgloss.Width(title)+lipgloss.Width(right)+2 > inner {
		right = ""
	}
	if lipgloss.Width(title)+2 > inner {
		title = ansi.Truncate(title, max(0, inner-2), "…")
	}
	gap := max(0, inner-lipgloss.Width(title)-lipgloss.Width(right)-1)
	lines[0] = rule.Render(runes.TopLeft+runes.Top) + title +
		rule.Render(strings.Repeat(runes.Top, gap)) + right + rule.Render(runes.TopRight)
	return strings.Join(lines, "\n")
}

func visibleWindow(cursor, total, available int) int {
	if total <= available || cursor < available {
		return 0
	}
	start := cursor - available + 1
	return min(start, total-available)
}

func uniqueReasons(reasons []string) []string {
	seen := make(map[string]bool)
	var unique []string
	for _, reason := range reasons {
		reason = strings.TrimSpace(reason)
		if reason != "" && !seen[reason] {
			seen[reason] = true
			unique = append(unique, reason)
		}
	}
	if len(unique) > 3 {
		unique = unique[:3]
	}
	return unique
}
