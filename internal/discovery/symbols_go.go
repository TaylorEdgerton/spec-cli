package discovery

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

type goSymbolProvider struct{}

type goParsedSymbol struct {
	Symbol
	identifier string
	start      uint32
	end        uint32
	priority   int
	node       *gotreesitter.Node
}

type goResolvedMatch struct {
	symbolMatch
	node       *gotreesitter.Node
	identifier *gotreesitter.Node
	definition bool
	exact      bool
}

func (goSymbolProvider) Supports(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".go")
}

func (goSymbolProvider) Symbols(source []byte, matches []symbolMatch) (symbols []Symbol, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			symbols = nil
			err = fmt.Errorf("parse Go symbols: %v", recovered)
		}
	}()

	language := grammars.GoLanguage()
	parser := gotreesitter.NewParser(language)
	tree, err := parser.ParseStrict(source)
	if err != nil {
		return nil, err
	}
	defer tree.Release()
	if tree.RootNode() == nil || tree.RootNode().HasErrorOrMissing() {
		return nil, fmt.Errorf("parse Go symbols: incomplete syntax tree")
	}

	resolved := make([]goResolvedMatch, 0, len(matches))
	for _, match := range matches {
		offset, ok := sourceOffset(source, match.Line, match.Column)
		if !ok {
			continue
		}
		node := tree.NamedNodeAtByte(uint32(offset))
		if node == nil {
			continue
		}
		identifier := enclosingIdentifier(node, language)
		definition := identifier != nil && isGoDefinition(identifier, language)
		resolved = append(resolved, goResolvedMatch{
			symbolMatch: match,
			node:        node,
			identifier:  identifier,
			definition:  definition,
			exact:       definition && sameIdentifierText(identifier.Text(source), match.Text),
		})
	}

	var functionMatches, directMatches, related, matched []goParsedSymbol
	for _, match := range resolved {
		if !match.definition {
			continue
		}
		if declaration, ok := goDeclarationSymbol(match.identifier, language, source); ok && declaration.Kind == "function" {
			declaration.Reasons = []string{fmt.Sprintf("related to %q", match.Text)}
			functionMatches = appendUniqueGoSymbol(functionMatches, declaration)
		}
	}
	for _, match := range resolved {
		if match.definition {
			continue
		}
		if owner, ok := goBehaviourSymbol(match.node, language, source); ok {
			owner.Reasons = []string{fmt.Sprintf("related to %q", match.Text)}
			directMatches = appendUniqueGoSymbol(directMatches, owner)
			break
		}
	}

	anchors := exactDefinitionMatches(resolved)
	if len(anchors) == 0 {
		for _, match := range resolved {
			if match.definition {
				anchors = append(anchors, match)
				break
			}
		}
	}
	for _, anchor := range anchors {
		name := anchor.identifier.Text(source)
		for _, usage := range goIdentifierNodes(tree.RootNode(), language, source, name) {
			if sameNode(anchor.identifier, usage) {
				continue
			}
			if owner, ok := goBehaviourSymbol(usage, language, source); ok {
				owner.Reasons = []string{"uses " + name}
				owner.priority = goUsagePriority(usage, language)
				related = appendUniqueGoSymbol(related, owner)
			}
		}
		if declaration, ok := goDeclarationSymbol(anchor.identifier, language, source); ok {
			declaration.Reasons = []string{"matched discovery intent"}
			matched = appendUniqueGoSymbol(matched, declaration)
		}
	}
	sort.SliceStable(related, func(i, j int) bool {
		return related[i].priority > related[j].priority
	})

	var combined []goParsedSymbol
	if len(related) > 0 {
		combined = append(combined, related[0])
	}
	combined = append(combined, functionMatches...)
	combined = append(combined, directMatches...)
	combined = append(combined, matched...)
	if len(related) > 1 {
		combined = append(combined, related[1:]...)
	}
	for _, candidate := range combined {
		if len(symbols) == maxSymbolsPerFile {
			break
		}
		if !containsSymbol(symbols, candidate.Symbol) {
			candidate.Related = goDirectRelationships(candidate, tree.RootNode(), language, source)
			symbols = append(symbols, candidate.Symbol)
		}
	}
	return symbols, nil
}

func (goSymbolProvider) Explore(source []byte, location symbolMatch) (symbol Symbol, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			symbol = Symbol{}
			err = fmt.Errorf("parse Go symbol context: %v", recovered)
		}
	}()

	language := grammars.GoLanguage()
	parser := gotreesitter.NewParser(language)
	tree, err := parser.ParseStrict(source)
	if err != nil {
		return Symbol{}, err
	}
	defer tree.Release()
	if tree.RootNode() == nil || tree.RootNode().HasErrorOrMissing() {
		return Symbol{}, fmt.Errorf("parse Go symbol context: incomplete syntax tree")
	}
	offset, ok := sourceOffset(source, location.Line, location.Column)
	if !ok {
		return Symbol{}, fmt.Errorf("symbol location is outside the file")
	}
	node := tree.NamedNodeAtByte(uint32(offset))
	if node == nil {
		return Symbol{}, fmt.Errorf("no symbol at the selected location")
	}
	identifier := enclosingIdentifier(node, language)
	var root goParsedSymbol
	if identifier != nil && isGoDefinition(identifier, language) {
		root, ok = goDeclarationSymbol(identifier, language, source)
	} else {
		root, ok = goBehaviourSymbol(node, language, source)
	}
	if !ok {
		return Symbol{}, fmt.Errorf("no enclosing Go symbol at the selected location")
	}
	root.Related = goDirectRelationships(root, tree.RootNode(), language, source)
	return root.Symbol, nil
}

func goDirectRelationships(root goParsedSymbol, treeRoot *gotreesitter.Node, language *gotreesitter.Language, source []byte) []RelatedSymbol {
	definitions := goDefinitions(treeRoot, language, source)
	if root.Kind != "function" {
		return goSymbolUsages(root, treeRoot, language, source)
	}
	declaration := root.node
	if declaration != nil {
		declaration = declaration.Parent()
	}
	if declaration == nil {
		return nil
	}

	var related []RelatedSymbol
	for _, identifier := range goAllIdentifiers(declaration, language) {
		if sameNode(identifier, root.node) || isGoDefinition(identifier, language) {
			continue
		}
		name := identifier.Text(source)
		targets := definitions[name]
		if len(targets) != 1 {
			continue
		}
		target := targets[0]
		relation := "uses " + target.Name
		if call := goDirectCall(identifier, language); call != nil {
			relation = "calls " + target.Name
			if receiver := goAssignedName(call, language, source); receiver != "" {
				relation = "receives " + receiver + " from " + target.Name
			}
		}
		related = appendUniqueRelationship(related, RelatedSymbol{
			Name: target.Name, Kind: target.Kind, Line: target.Line, Column: target.Column, Relation: relation,
		})
	}
	related = orderGoRelationships(root, related)
	if len(related) > maxSymbolsPerFile {
		related = related[:maxSymbolsPerFile]
	}
	return related
}

func goDefinitions(root *gotreesitter.Node, language *gotreesitter.Language, source []byte) map[string][]goParsedSymbol {
	definitions := make(map[string][]goParsedSymbol)
	for _, identifier := range goAllIdentifiers(root, language) {
		if !isTopLevelGoDefinition(identifier, language) {
			continue
		}
		if symbol, ok := goDeclarationSymbol(identifier, language, source); ok {
			definitions[symbol.identifier] = append(definitions[symbol.identifier], symbol)
		}
	}
	return definitions
}

func isTopLevelGoDefinition(identifier *gotreesitter.Node, language *gotreesitter.Language) bool {
	if !isGoDefinition(identifier, language) {
		return false
	}
	for current := identifier.Parent(); current != nil; current = current.Parent() {
		switch current.Type(language) {
		case "function_declaration", "method_declaration":
			return sameNode(identifier, current.ChildByFieldName("name", language))
		}
	}
	return true
}

func goSymbolUsages(root goParsedSymbol, treeRoot *gotreesitter.Node, language *gotreesitter.Language, source []byte) []RelatedSymbol {
	type usage struct {
		related  RelatedSymbol
		priority int
	}
	var usages []usage
	for _, identifier := range goIdentifierNodes(treeRoot, language, source, root.identifier) {
		if sameNode(identifier, root.node) || isGoDefinition(identifier, language) {
			continue
		}
		owner, ok := goBehaviourSymbol(identifier, language, source)
		if !ok {
			continue
		}
		usages = append(usages, usage{
			related: RelatedSymbol{
				Name: owner.Name, Kind: owner.Kind, Line: owner.Line, Column: owner.Column,
				Relation: "used by " + owner.Name,
			},
			priority: goUsagePriority(identifier, language),
		})
	}
	sort.SliceStable(usages, func(i, j int) bool { return usages[i].priority > usages[j].priority })
	var related []RelatedSymbol
	for _, usage := range usages {
		related = appendUniqueRelationship(related, usage.related)
		if len(related) == maxSymbolsPerFile {
			break
		}
	}
	return related
}

func goAllIdentifiers(root *gotreesitter.Node, language *gotreesitter.Language) []*gotreesitter.Node {
	var identifiers []*gotreesitter.Node
	var walk func(*gotreesitter.Node)
	walk = func(node *gotreesitter.Node) {
		if node == nil {
			return
		}
		switch node.Type(language) {
		case "identifier", "field_identifier", "type_identifier":
			identifiers = append(identifiers, node)
		}
		for index := 0; index < node.NamedChildCount(); index++ {
			walk(node.NamedChild(index))
		}
	}
	walk(root)
	return identifiers
}

func goDirectCall(identifier *gotreesitter.Node, language *gotreesitter.Language) *gotreesitter.Node {
	parent := identifier.Parent()
	if parent == nil || parent.Type(language) != "call_expression" {
		return nil
	}
	if !sameNode(identifier, parent.ChildByFieldName("function", language)) {
		return nil
	}
	return parent
}

func goAssignedName(call *gotreesitter.Node, language *gotreesitter.Language, source []byte) string {
	for current := call.Parent(); current != nil; current = current.Parent() {
		switch current.Type(language) {
		case "short_var_declaration", "assignment_statement":
			for _, identifier := range goAllIdentifiers(current, language) {
				if identifier.StartByte() >= call.StartByte() {
					break
				}
				return identifier.Text(source)
			}
			return ""
		case "function_declaration", "method_declaration":
			return ""
		}
	}
	return ""
}

func appendUniqueRelationship(relationships []RelatedSymbol, candidate RelatedSymbol) []RelatedSymbol {
	for _, relationship := range relationships {
		if relationship.Name == candidate.Name && relationship.Line == candidate.Line && relationship.Relation == candidate.Relation {
			return relationships
		}
	}
	return append(relationships, candidate)
}

func orderGoRelationships(root goParsedSymbol, relationships []RelatedSymbol) []RelatedSymbol {
	var ordered, calls, receives, uses, types []RelatedSymbol
	for _, relationship := range relationships {
		anchored := false
		for _, reason := range root.Reasons {
			if reason == relationship.Relation {
				ordered = appendUniqueRelationship(ordered, relationship)
				anchored = true
				break
			}
		}
		if anchored {
			continue
		}
		switch {
		case strings.HasPrefix(relationship.Relation, "calls "):
			calls = append(calls, relationship)
		case strings.HasPrefix(relationship.Relation, "receives "):
			receives = append(receives, relationship)
		case relationship.Kind == "type":
			types = append(types, relationship)
		default:
			uses = append(uses, relationship)
		}
	}
	if len(calls) > 0 {
		ordered = appendUniqueRelationship(ordered, calls[0])
		calls = calls[1:]
	}
	if len(receives) > 0 {
		ordered = appendUniqueRelationship(ordered, receives[0])
		receives = receives[1:]
	}
	for _, group := range [][]RelatedSymbol{calls, uses, receives, types} {
		for _, relationship := range group {
			ordered = appendUniqueRelationship(ordered, relationship)
		}
	}
	return ordered
}

func exactDefinitionMatches(matches []goResolvedMatch) []goResolvedMatch {
	var exact []goResolvedMatch
	for _, match := range matches {
		if match.definition && match.exact {
			exact = append(exact, match)
		}
	}
	return exact
}

func sameIdentifierText(identifier, match string) bool {
	canonical := func(value string) string {
		return strings.NewReplacer("_", "", " ", "").Replace(strings.ToLower(value))
	}
	return canonical(identifier) == canonical(match)
}

func sourceOffset(source []byte, line, column int) (int, bool) {
	if line < 1 || column < 1 {
		return 0, false
	}
	lineStart := 0
	for current := 1; current < line; current++ {
		next := bytes.IndexByte(source[lineStart:], '\n')
		if next < 0 {
			return 0, false
		}
		lineStart += next + 1
	}
	offset := lineStart
	for current := 1; current < column; current++ {
		if offset >= len(source) || source[offset] == '\n' {
			return 0, false
		}
		_, size := utf8.DecodeRune(source[offset:])
		offset += size
	}
	return offset, offset < len(source)
}

func enclosingIdentifier(node *gotreesitter.Node, language *gotreesitter.Language) *gotreesitter.Node {
	for current := node; current != nil; current = current.Parent() {
		switch current.Type(language) {
		case "identifier", "field_identifier", "type_identifier":
			return current
		}
	}
	return nil
}

func isGoDefinition(identifier *gotreesitter.Node, language *gotreesitter.Language) bool {
	parent := identifier.Parent()
	if parent == nil {
		return false
	}
	switch parent.Type(language) {
	case "const_spec", "var_spec", "type_spec", "function_declaration", "method_declaration":
		name := parent.ChildByFieldName("name", language)
		return sameNode(identifier, name)
	default:
		return false
	}
}

func goIdentifierNodes(root *gotreesitter.Node, language *gotreesitter.Language, source []byte, name string) []*gotreesitter.Node {
	var nodes []*gotreesitter.Node
	var walk func(*gotreesitter.Node)
	walk = func(node *gotreesitter.Node) {
		if node == nil {
			return
		}
		switch node.Type(language) {
		case "identifier", "field_identifier", "type_identifier":
			if node.Text(source) == name {
				nodes = append(nodes, node)
			}
		}
		for index := 0; index < node.NamedChildCount(); index++ {
			walk(node.NamedChild(index))
		}
	}
	walk(root)
	return nodes
}

func goBehaviourSymbol(node *gotreesitter.Node, language *gotreesitter.Language, source []byte) (goParsedSymbol, bool) {
	for current := node; current != nil; current = current.Parent() {
		switch current.Type(language) {
		case "function_declaration", "method_declaration":
			name := current.ChildByFieldName("name", language)
			if name == nil {
				return goParsedSymbol{}, false
			}
			return parsedGoSymbol(name.Text(source)+"()", "function", name.Text(source), name), true
		}
	}
	return goParsedSymbol{}, false
}

func goDeclarationSymbol(identifier *gotreesitter.Node, language *gotreesitter.Language, source []byte) (goParsedSymbol, bool) {
	parent := identifier.Parent()
	if parent == nil {
		return goParsedSymbol{}, false
	}
	kind := "symbol"
	switch parent.Type(language) {
	case "const_spec":
		kind = "constant"
	case "var_spec":
		kind = "variable"
	case "type_spec":
		kind = "type"
	case "function_declaration", "method_declaration":
		return goBehaviourSymbol(identifier, language, source)
	default:
		return goParsedSymbol{}, false
	}
	name := identifier.Text(source)
	return parsedGoSymbol(name, kind, name, identifier), true
}

func parsedGoSymbol(name, kind, identifier string, node *gotreesitter.Node) goParsedSymbol {
	point := node.StartPoint()
	return goParsedSymbol{
		Symbol:     Symbol{Name: name, Kind: kind, Line: int(point.Row) + 1, Column: int(point.Column) + 1},
		identifier: identifier,
		start:      node.StartByte(),
		end:        node.EndByte(),
		node:       node,
	}
}

func appendUniqueGoSymbol(symbols []goParsedSymbol, candidate goParsedSymbol) []goParsedSymbol {
	for index, symbol := range symbols {
		if symbol.start == candidate.start && symbol.end == candidate.end && symbol.Name == candidate.Name {
			if candidate.priority > symbol.priority {
				symbols[index] = candidate
			}
			return symbols
		}
	}
	return append(symbols, candidate)
}

func goUsagePriority(node *gotreesitter.Node, language *gotreesitter.Language) int {
	priority := 0
	for current := node.Parent(); current != nil; current = current.Parent() {
		switch current.Type(language) {
		case "keyed_element", "composite_literal":
			return 3
		case "argument_list", "assignment_statement", "short_var_declaration":
			if priority < 2 {
				priority = 2
			}
		case "binary_expression":
			if priority < 1 {
				priority = 1
			}
		case "function_declaration", "method_declaration":
			return priority
		}
	}
	return priority
}

func containsSymbol(symbols []Symbol, candidate Symbol) bool {
	for _, symbol := range symbols {
		if symbol.Line == candidate.Line && symbol.Column == candidate.Column && symbol.Name == candidate.Name {
			return true
		}
	}
	return false
}

func sameNode(left, right *gotreesitter.Node) bool {
	return left != nil && right != nil && left.StartByte() == right.StartByte() && left.EndByte() == right.EndByte()
}
