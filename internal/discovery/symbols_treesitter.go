package discovery

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/odvcencio/gotreesitter"

	discoverygrammars "github.com/TaylorEdgerton/spec-cli/internal/discovery/grammars"
)

type treeSitterSymbolProvider struct{}

type treeDefinition struct {
	Symbol
	identifier string
	rangeStart uint32
	rangeEnd   uint32
	nameStart  uint32
	nameEnd    uint32
	node       *gotreesitter.Node
}

type treeReference struct {
	kind      string
	name      string
	qualifier string
	start     uint32
	end       uint32
	nameStart uint32
	nameEnd   uint32
	node      *gotreesitter.Node
	owner     RelatedSymbol
}

type treeImport struct {
	source   string
	imported string
}

type treeDocument struct {
	path        string
	grammar     discoverygrammars.Language
	language    *gotreesitter.Language
	tree        *gotreesitter.Tree
	source      []byte
	packageName string
	definitions []treeDefinition
	byName      map[string][]RelatedSymbol
	imports     map[string]treeImport
	namespaces  map[string]string
	references  []treeReference
}

type treeRepository struct {
	family string
	files  map[string]*treeDocument
}

func (treeSitterSymbolProvider) Supports(path string) bool {
	_, ok := discoverygrammars.ForPath(path)
	return ok
}

func (treeSitterSymbolProvider) Symbols(root, path string, source []byte, matches []symbolMatch) (symbols []Symbol, err error) {
	defer treeSitterRecovery(path, &symbols, nil, &err)
	document, err := parseTreeDocument(path, source)
	if err != nil {
		return nil, err
	}
	defer document.release()
	repository := buildTreeRepository(root, document)
	defer repository.releaseExcept(path)

	var exactFunctions, direct, usages, declarations []treeDefinition
	for _, match := range matches {
		offset, ok := sourceOffset(source, match.Line, match.Column)
		if !ok {
			continue
		}
		definition, definitionOK := document.definitionAt(uint32(offset))
		if definitionOK && definition.nameContains(uint32(offset)) {
			exact := sameIdentifierText(definition.identifier, match.Text)
			definition.Reasons = []string{fmt.Sprintf("related to %q", match.Text)}
			if exact {
				definition.Reasons = []string{"matched discovery intent"}
			}
			if definition.isBehaviour() && exact && match.Kind == kindIdentifier {
				exactFunctions = appendUniqueTreeDefinition(exactFunctions, definition)
			} else {
				declarations = appendUniqueTreeDefinition(declarations, definition)
				if usage, ok := document.bestUsageOwner(definition.identifier); ok {
					usage.Reasons = []string{"uses " + definition.identifier}
					usages = appendUniqueTreeDefinition(usages, usage)
				}
			}
			continue
		}
		if definitionOK {
			definition.Reasons = []string{fmt.Sprintf("related to %q", match.Text)}
			direct = appendUniqueTreeDefinition(direct, definition)
		}
	}

	combined := make([]treeDefinition, 0, len(exactFunctions)+len(direct)+len(usages)+len(declarations))
	if len(exactFunctions) > 0 {
		combined = append(combined, exactFunctions...)
		combined = append(combined, direct...)
		combined = append(combined, usages...)
	} else {
		combined = append(combined, usages...)
		combined = append(combined, declarations...)
		combined = append(combined, direct...)
	}
	if len(exactFunctions) > 0 {
		combined = append(combined, declarations...)
	}

	for _, candidate := range combined {
		if len(symbols) == maxSymbolsPerFile {
			break
		}
		if containsSymbol(symbols, candidate.Symbol) {
			continue
		}
		candidate.Related = repository.relationships(document, candidate)
		symbols = append(symbols, candidate.Symbol)
	}
	return symbols, nil
}

func (treeSitterSymbolProvider) Explore(root, path string, source []byte, location symbolMatch) (symbol Symbol, err error) {
	defer treeSitterRecovery(path, nil, &symbol, &err)
	document, err := parseTreeDocument(path, source)
	if err != nil {
		return Symbol{}, err
	}
	defer document.release()
	offset, ok := sourceOffset(source, location.Line, location.Column)
	if !ok {
		return Symbol{}, fmt.Errorf("symbol location is outside the file")
	}
	definition, ok := document.definitionAt(uint32(offset))
	if !ok {
		return Symbol{}, fmt.Errorf("no enclosing %s symbol at the selected location", document.grammar.Name)
	}
	repository := buildTreeRepository(root, document)
	defer repository.releaseExcept(path)
	definition.Related = repository.relationships(document, definition)
	return definition.Symbol, nil
}

func treeSitterRecovery(path string, symbols *[]Symbol, symbol *Symbol, err *error) {
	if recovered := recover(); recovered != nil {
		if symbols != nil {
			*symbols = nil
		}
		if symbol != nil {
			*symbol = Symbol{}
		}
		*err = fmt.Errorf("parse Tree-sitter symbols for %s: %v", path, recovered)
	}
}

func parseTreeDocument(path string, source []byte) (*treeDocument, error) {
	grammar, ok := discoverygrammars.ForPath(path)
	if !ok {
		return nil, fmt.Errorf("Tree-sitter symbol discovery is unavailable for %s", path)
	}
	language := grammar.Language()
	if language == nil {
		return nil, fmt.Errorf("%s parser is unavailable", grammar.Name)
	}
	parser := gotreesitter.NewParser(language)
	tree, err := parser.ParseStrict(source)
	if err != nil {
		return nil, err
	}
	if tree.RootNode() == nil || tree.RootNode().Type(language) != grammar.RootType || tree.RootNode().HasErrorOrMissing() {
		tree.Release()
		return nil, fmt.Errorf("parse %s symbols: incomplete syntax tree", grammar.Name)
	}
	tagger, err := gotreesitter.NewTagger(language, grammar.TagsQuery)
	if err != nil {
		tree.Release()
		return nil, fmt.Errorf("compile %s tags query: %w", grammar.Name, err)
	}
	document := &treeDocument{
		path: path, grammar: grammar, language: language, tree: tree, source: source,
		byName: make(map[string][]RelatedSymbol), imports: make(map[string]treeImport), namespaces: make(map[string]string),
	}
	document.collectTags(tagger.TagTree(tree))
	document.collectImports()
	document.packageName = document.scopeName()
	return document, nil
}

func (document *treeDocument) release() {
	if document != nil && document.tree != nil {
		document.tree.Release()
	}
}

func (document *treeDocument) collectTags(tags []gotreesitter.Tag) {
	for _, tag := range tags {
		if !strings.HasPrefix(tag.Kind, "definition.") {
			continue
		}
		kind := strings.TrimPrefix(tag.Kind, "definition.")
		if kind == "class" || kind == "interface" {
			kind = "type"
		}
		if document.grammar.Family == "go" && kind == "method" {
			kind = "function"
		}
		if kind == "function" && document.directlyEnclosedByClass(tag.Range.StartByte) {
			kind = "method"
		}
		display := tag.Name
		if kind == "function" || kind == "method" || kind == "test" {
			display += "()"
			if looksLikeTest(document.path) || isTestSymbol(tag.Name) {
				kind = "test"
			}
		}
		definition := treeDefinition{
			Symbol:     Symbol{Name: display, Kind: kind, Line: int(tag.NameRange.StartPoint.Row) + 1, Column: int(tag.NameRange.StartPoint.Column) + 1},
			identifier: tag.Name, rangeStart: tag.Range.StartByte, rangeEnd: tag.Range.EndByte,
			nameStart: tag.NameRange.StartByte, nameEnd: tag.NameRange.EndByte,
			node: document.nodeForRange(tag.Range),
		}
		if !containsTreeDefinition(document.definitions, definition) {
			document.definitions = append(document.definitions, definition)
			document.byName[tag.Name] = append(document.byName[tag.Name], definition.related(document.path))
		}
	}
	sort.SliceStable(document.definitions, func(i, j int) bool {
		if document.definitions[i].rangeStart == document.definitions[j].rangeStart {
			return document.definitions[i].rangeEnd < document.definitions[j].rangeEnd
		}
		return document.definitions[i].rangeStart < document.definitions[j].rangeStart
	})
	for _, tag := range tags {
		if !strings.HasPrefix(tag.Kind, "reference.") || document.isDefinitionName(tag.NameRange.StartByte, tag.NameRange.EndByte) {
			continue
		}
		reference := treeReference{
			kind: strings.TrimPrefix(tag.Kind, "reference."), name: tag.Name,
			start: tag.Range.StartByte, end: tag.Range.EndByte,
			nameStart: tag.NameRange.StartByte, nameEnd: tag.NameRange.EndByte,
			node: document.nodeForRange(tag.Range),
		}
		if owner, ok := document.enclosingDefinition(tag.Range.StartByte); ok {
			reference.owner = owner.related(document.path)
		}
		if reference.kind == "call" {
			reference.qualifier, _ = document.callQualifier(reference.node)
		}
		document.references = mergeTreeReference(document.references, reference)
	}
}

func (document *treeDocument) nodeForRange(value gotreesitter.Range) *gotreesitter.Node {
	return document.tree.RootNode().NamedDescendantForByteRange(value.StartByte, value.EndByte)
}

func (document *treeDocument) directlyEnclosedByClass(offset uint32) bool {
	node := document.tree.RootNode().NamedNodeAtByte(offset)
	for current := node; current != nil; current = current.Parent() {
		typeName := current.Type(document.language)
		if document.grammar.IsClassType(typeName) {
			return true
		}
		if current != node && document.grammar.IsFunctionType(typeName) {
			return false
		}
	}
	return false
}

func (document *treeDocument) definitionAt(offset uint32) (treeDefinition, bool) {
	for _, definition := range document.definitions {
		if definition.nameContains(offset) {
			return definition, true
		}
	}
	return document.enclosingDefinition(offset)
}

func (document *treeDocument) enclosingDefinition(offset uint32) (treeDefinition, bool) {
	var selected treeDefinition
	found := false
	for _, definition := range document.definitions {
		if offset < definition.rangeStart || offset >= definition.rangeEnd {
			continue
		}
		if !found || definition.rangeEnd-definition.rangeStart < selected.rangeEnd-selected.rangeStart {
			selected, found = definition, true
		}
	}
	return selected, found
}

func (document *treeDocument) bestUsageOwner(name string) (treeDefinition, bool) {
	type candidate struct {
		definition treeDefinition
		priority   int
	}
	var candidates []candidate
	for _, reference := range document.references {
		if reference.name != name || reference.owner.Name == "" {
			continue
		}
		owner, ok := document.definitionAtLocation(reference.owner.Line, reference.owner.Column)
		if !ok {
			continue
		}
		candidates = append(candidates, candidate{definition: owner, priority: document.usagePriority(reference)})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].priority > candidates[j].priority })
	if len(candidates) == 0 {
		return treeDefinition{}, false
	}
	return candidates[0].definition, true
}

func (document *treeDocument) definitionAtLocation(line, column int) (treeDefinition, bool) {
	for _, definition := range document.definitions {
		if definition.Line == line && definition.Column == column {
			return definition, true
		}
	}
	return treeDefinition{}, false
}

func (document *treeDocument) usagePriority(reference treeReference) int {
	node := document.tree.RootNode().NamedNodeAtByte(reference.nameStart)
	priority := 0
	for current := node; current != nil; current = current.Parent() {
		switch current.Type(document.language) {
		case "keyed_element", "composite_literal":
			return 3
		case "argument_list", "assignment_statement", "short_var_declaration", "assignment", "variable_declarator":
			if priority < 2 {
				priority = 2
			}
		case "binary_expression", "boolean_operator":
			if priority < 1 {
				priority = 1
			}
		}
		if document.grammar.IsFunctionType(current.Type(document.language)) {
			return priority
		}
	}
	return priority
}

func (definition treeDefinition) nameContains(offset uint32) bool {
	return offset >= definition.nameStart && offset < definition.nameEnd
}

func (definition treeDefinition) isBehaviour() bool {
	return definition.Kind == "function" || definition.Kind == "method" || definition.Kind == "test"
}

func (definition treeDefinition) related(path string) RelatedSymbol {
	return RelatedSymbol{Name: definition.Name, Kind: definition.Kind, Path: path, Line: definition.Line, Column: definition.Column}
}

func containsTreeDefinition(definitions []treeDefinition, candidate treeDefinition) bool {
	for _, definition := range definitions {
		if definition.nameStart == candidate.nameStart && definition.nameEnd == candidate.nameEnd && definition.identifier == candidate.identifier {
			return true
		}
	}
	return false
}

func appendUniqueTreeDefinition(definitions []treeDefinition, candidate treeDefinition) []treeDefinition {
	for index, definition := range definitions {
		if definition.nameStart != candidate.nameStart || definition.nameEnd != candidate.nameEnd || definition.identifier != candidate.identifier {
			continue
		}
		if hasSymbolReason(candidate.Reasons, "matched discovery intent") {
			definitions[index] = candidate
		}
		return definitions
	}
	return append(definitions, candidate)
}

func hasSymbolReason(reasons []string, expected string) bool {
	for _, reason := range reasons {
		if reason == expected {
			return true
		}
	}
	return false
}

func mergeTreeReference(references []treeReference, candidate treeReference) []treeReference {
	for index, reference := range references {
		if reference.nameStart != candidate.nameStart || reference.nameEnd != candidate.nameEnd || reference.name != candidate.name {
			continue
		}
		if candidate.kind == "call" && reference.kind != "call" {
			references[index] = candidate
		}
		return references
	}
	return append(references, candidate)
}

func isTestSymbol(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "test_") || strings.HasPrefix(lower, "test") || strings.HasSuffix(lower, "test")
}

func buildTreeRepository(root string, current *treeDocument) treeRepository {
	repository := treeRepository{family: current.grammar.Family, files: map[string]*treeDocument{current.path: current}}
	directory := filepath.Dir(filepath.FromSlash(current.path))
	entries, err := os.ReadDir(filepath.Join(root, directory))
	if err != nil {
		return repository
	}
	parsed := 0
	for _, entry := range entries {
		if parsed >= 128 {
			break
		}
		if entry.IsDir() {
			continue
		}
		relative := filepath.ToSlash(filepath.Join(directory, entry.Name()))
		if relative == current.path || isRepositoryMetadata(relative) {
			continue
		}
		grammar, ok := discoverygrammars.ForPath(relative)
		if !ok || grammar.Family != current.grammar.Family {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileSize {
			continue
		}
		source, err := readSearchableFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil || looksGenerated(string(source)) {
			continue
		}
		document, err := parseTreeDocument(relative, source)
		if err != nil {
			continue
		}
		if current.grammar.Family == "go" && current.packageName != "" && document.packageName != current.packageName {
			document.release()
			continue
		}
		repository.files[relative] = document
		parsed++
	}
	return repository
}

func (repository treeRepository) releaseExcept(path string) {
	for candidatePath, document := range repository.files {
		if candidatePath != path {
			document.release()
		}
	}
}

func (repository treeRepository) relationships(document *treeDocument, root treeDefinition) []RelatedSymbol {
	if !root.isBehaviour() {
		return repository.symbolUsages(document, root)
	}
	var relationships []RelatedSymbol
	for _, candidate := range repository.files {
		for _, reference := range candidate.references {
			if reference.kind != "call" || reference.owner.Name == "" {
				continue
			}
			target, ok := repository.resolve(candidate, reference)
			if !ok || target.Path != document.path || target.Line != root.Line || target.Column != root.Column {
				continue
			}
			caller := reference.owner
			if caller.Path == document.path && caller.Line == root.Line && caller.Column == root.Column {
				continue
			}
			caller.Relation = "called by " + caller.Name
			relationships = appendUniqueRelationship(relationships, caller)
		}
	}

	for _, reference := range document.references {
		if reference.owner.Line != root.Line || reference.owner.Column != root.Column {
			continue
		}
		target, ok := repository.resolve(document, reference)
		if !ok || (target.Path == document.path && target.Line == root.Line && target.Column == root.Column) {
			continue
		}
		relation := "uses " + target.Name
		if reference.kind == "call" {
			relation = "calls " + target.Name
			if receiver := document.assignedName(reference.node); receiver != "" {
				relation = "receives " + receiver + " from " + target.Name
			}
		}
		target.Relation = relation
		relationships = appendUniqueRelationship(relationships, target)
	}
	relationships = orderTreeRelationships(root, relationships)
	if len(relationships) > maxSymbolsPerFile {
		relationships = relationships[:maxSymbolsPerFile]
	}
	return relationships
}

func (repository treeRepository) symbolUsages(document *treeDocument, root treeDefinition) []RelatedSymbol {
	type usage struct {
		related  RelatedSymbol
		priority int
	}
	var usages []usage
	for _, candidate := range repository.files {
		for _, reference := range candidate.references {
			if reference.name != root.identifier || reference.owner.Name == "" {
				continue
			}
			if candidate.path != document.path && repository.family != "go" {
				resolved, ok := repository.resolve(candidate, reference)
				if !ok || resolved.Path != document.path || resolved.Line != root.Line || resolved.Column != root.Column {
					continue
				}
			}
			related := reference.owner
			related.Relation = "used by " + related.Name
			if reference.kind == "call" {
				related.Relation = "called by " + related.Name
			}
			usages = append(usages, usage{related: related, priority: candidate.usagePriority(reference)})
		}
	}
	sort.SliceStable(usages, func(i, j int) bool { return usages[i].priority > usages[j].priority })
	var relationships []RelatedSymbol
	for _, usage := range usages {
		relationships = appendUniqueRelationship(relationships, usage.related)
		if len(relationships) == maxSymbolsPerFile {
			break
		}
	}
	return relationships
}

func (repository treeRepository) resolve(document *treeDocument, reference treeReference) (RelatedSymbol, bool) {
	if repository.family == "go" {
		if reference.qualifier != "" {
			return RelatedSymbol{}, false
		}
		var definitions []RelatedSymbol
		for _, candidate := range repository.files {
			definitions = append(definitions, candidate.byName[reference.name]...)
		}
		if len(definitions) != 1 {
			return RelatedSymbol{}, false
		}
		return definitions[0], true
	}
	if reference.qualifier != "" {
		module, ok := document.namespaces[reference.qualifier]
		if !ok {
			return RelatedSymbol{}, false
		}
		targetPath, ok := repository.modulePath(document.path, module)
		if !ok {
			return RelatedSymbol{}, false
		}
		definitions := repository.files[targetPath].byName[reference.name]
		if len(definitions) != 1 {
			return RelatedSymbol{}, false
		}
		return definitions[0], true
	}
	if definitions := document.byName[reference.name]; len(definitions) == 1 {
		return definitions[0], true
	}
	imported, ok := document.imports[reference.name]
	if !ok {
		return RelatedSymbol{}, false
	}
	targetPath, ok := repository.modulePath(document.path, imported.source)
	if !ok {
		return RelatedSymbol{}, false
	}
	definitions := repository.files[targetPath].byName[imported.imported]
	if len(definitions) != 1 {
		return RelatedSymbol{}, false
	}
	return definitions[0], true
}

func (repository treeRepository) modulePath(from, module string) (string, bool) {
	directory := filepath.Dir(filepath.FromSlash(from))
	module = strings.TrimSpace(module)
	if repository.family == "python" {
		module = strings.TrimLeft(module, ".")
		if module == "" || strings.Contains(module, ".") || strings.ContainsAny(module, "/\\") {
			return "", false
		}
	} else {
		if !strings.HasPrefix(module, "./") && !strings.HasPrefix(module, "../") {
			return "", false
		}
		module = filepath.FromSlash(module)
	}
	base := filepath.Clean(filepath.Join(directory, module))
	if filepath.Ext(base) != "" {
		candidate := filepath.ToSlash(base)
		_, ok := repository.files[candidate]
		return candidate, ok
	}
	extensions := []string{".py"}
	if repository.family == "ecmascript" {
		extensions = []string{".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs"}
	}
	for _, extension := range extensions {
		candidate := filepath.ToSlash(base + extension)
		if _, ok := repository.files[candidate]; ok {
			return candidate, true
		}
	}
	return "", false
}

func orderTreeRelationships(root treeDefinition, relationships []RelatedSymbol) []RelatedSymbol {
	var ordered, calledBy, calls, receives, uses, types []RelatedSymbol
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
		case strings.HasPrefix(relationship.Relation, "called by "):
			calledBy = append(calledBy, relationship)
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
	if len(calledBy) > 0 {
		ordered = appendUniqueRelationship(ordered, calledBy[0])
		calledBy = calledBy[1:]
	}
	for _, group := range [][]RelatedSymbol{calledBy, calls, uses, receives, types} {
		for _, relationship := range group {
			ordered = appendUniqueRelationship(ordered, relationship)
		}
	}
	return ordered
}

func appendUniqueRelationship(relationships []RelatedSymbol, candidate RelatedSymbol) []RelatedSymbol {
	for _, relationship := range relationships {
		if relationship.Name == candidate.Name && relationship.Path == candidate.Path &&
			relationship.Line == candidate.Line && relationship.Relation == candidate.Relation {
			return relationships
		}
	}
	return append(relationships, candidate)
}

func (document *treeDocument) callQualifier(call *gotreesitter.Node) (string, bool) {
	if call == nil || call.Type(document.language) != document.grammar.CallType {
		return "", false
	}
	function := call.ChildByFieldName("function", document.language)
	if function == nil {
		return "", false
	}
	if function.Type(document.language) != document.grammar.MemberType {
		return "", true
	}
	object := function.ChildByFieldName(document.grammar.MemberObjectField, document.language)
	name := function.ChildByFieldName(document.grammar.MemberNameField, document.language)
	if object == nil || name == nil || object.Type(document.language) != "identifier" {
		return "", false
	}
	return object.Text(document.source), true
}

func (document *treeDocument) assignedName(call *gotreesitter.Node) string {
	if call == nil {
		return ""
	}
	for current := call.Parent(); current != nil; current = current.Parent() {
		typeName := current.Type(document.language)
		if document.grammar.IsAssignmentType(typeName) {
			var identifier string
			walkNamedNodes(current, func(node *gotreesitter.Node) {
				if identifier != "" || node.StartByte() >= call.StartByte() {
					return
				}
				switch node.Type(document.language) {
				case "identifier", "field_identifier", "property_identifier":
					identifier = node.Text(document.source)
				}
			})
			return identifier
		}
		if document.grammar.IsFunctionType(typeName) {
			return ""
		}
	}
	return ""
}

func (document *treeDocument) isDefinitionName(start, end uint32) bool {
	for _, definition := range document.definitions {
		if definition.nameStart == start && definition.nameEnd == end {
			return true
		}
	}
	return false
}

func (document *treeDocument) scopeName() string {
	if document.grammar.Family != "go" {
		return ""
	}
	root := document.tree.RootNode()
	for index := 0; index < root.NamedChildCount(); index++ {
		child := root.NamedChild(index)
		if child == nil || child.Type(document.language) != "package_clause" {
			continue
		}
		for nameIndex := 0; nameIndex < child.NamedChildCount(); nameIndex++ {
			if name := child.NamedChild(nameIndex); name != nil {
				return name.Text(document.source)
			}
		}
	}
	return ""
}

func (document *treeDocument) collectImports() {
	if document.grammar.Family == "python" {
		document.collectPythonImports()
		return
	}
	if document.grammar.Family == "ecmascript" {
		document.collectECMAScriptImports()
		document.collectECMAScriptDefaultExports()
	}
}

func (document *treeDocument) collectPythonImports() {
	walkNamedNodes(document.tree.RootNode(), func(node *gotreesitter.Node) {
		if node.Type(document.language) == "import_statement" {
			for index := 0; index < node.ChildCount(); index++ {
				if node.FieldNameForChild(index, document.language) != "name" {
					continue
				}
				imported := node.Child(index)
				if imported == nil {
					continue
				}
				name := imported.ChildByFieldName("name", document.language)
				alias := imported.ChildByFieldName("alias", document.language)
				if name == nil {
					name = imported
				}
				module := name.Text(document.source)
				local := strings.Split(module, ".")[0]
				if alias != nil {
					local = alias.Text(document.source)
				}
				document.namespaces[local] = module
			}
			return
		}
		if node.Type(document.language) != "import_from_statement" {
			return
		}
		module := node.ChildByFieldName("module_name", document.language)
		if module == nil {
			return
		}
		moduleName := module.Text(document.source)
		for index := 0; index < node.ChildCount(); index++ {
			if node.FieldNameForChild(index, document.language) != "name" {
				continue
			}
			imported := node.Child(index)
			if imported == nil {
				continue
			}
			name := imported.ChildByFieldName("name", document.language)
			alias := imported.ChildByFieldName("alias", document.language)
			if name == nil {
				name = imported
			}
			local := name.Text(document.source)
			if alias != nil {
				local = alias.Text(document.source)
			}
			document.imports[local] = treeImport{source: moduleName, imported: lastDottedName(name.Text(document.source))}
		}
	})
}

func (document *treeDocument) collectECMAScriptImports() {
	walkNamedNodes(document.tree.RootNode(), func(node *gotreesitter.Node) {
		if node.Type(document.language) != "import_statement" {
			return
		}
		module := node.ChildByFieldName("source", document.language)
		if module == nil {
			return
		}
		moduleName := strings.Trim(module.Text(document.source), "'\"")
		clause := firstNamedChildOfType(node, document.language, "import_clause")
		if clause != nil {
			for index := 0; index < clause.NamedChildCount(); index++ {
				child := clause.NamedChild(index)
				if child == nil {
					continue
				}
				switch child.Type(document.language) {
				case "identifier":
					document.imports[child.Text(document.source)] = treeImport{source: moduleName, imported: "@default"}
				case "namespace_import":
					if child.NamedChildCount() > 0 {
						local := child.NamedChild(child.NamedChildCount() - 1)
						document.namespaces[local.Text(document.source)] = moduleName
					}
				}
			}
		}
		walkNamedNodes(node, func(imported *gotreesitter.Node) {
			if imported.Type(document.language) != "import_specifier" {
				return
			}
			name := imported.ChildByFieldName("name", document.language)
			alias := imported.ChildByFieldName("alias", document.language)
			if name == nil {
				return
			}
			local := name.Text(document.source)
			if alias != nil {
				local = alias.Text(document.source)
			}
			document.imports[local] = treeImport{source: moduleName, imported: name.Text(document.source)}
		})
	})
}

func (document *treeDocument) collectECMAScriptDefaultExports() {
	walkNamedNodes(document.tree.RootNode(), func(node *gotreesitter.Node) {
		if node.Type(document.language) != "export_statement" || !strings.HasPrefix(strings.TrimSpace(node.Text(document.source)), "export default ") {
			return
		}
		for _, definition := range document.definitions {
			if definition.rangeStart < node.StartByte() || definition.rangeEnd > node.EndByte() {
				continue
			}
			document.byName["@default"] = []RelatedSymbol{definition.related(document.path)}
			return
		}
	})
}

func walkNamedNodes(root *gotreesitter.Node, visit func(*gotreesitter.Node)) {
	if root == nil {
		return
	}
	visit(root)
	for index := 0; index < root.NamedChildCount(); index++ {
		walkNamedNodes(root.NamedChild(index), visit)
	}
}

func firstNamedChildOfType(root *gotreesitter.Node, language *gotreesitter.Language, typeName string) *gotreesitter.Node {
	if root == nil {
		return nil
	}
	for index := 0; index < root.NamedChildCount(); index++ {
		child := root.NamedChild(index)
		if child != nil && child.Type(language) == typeName {
			return child
		}
	}
	return nil
}

func lastDottedName(name string) string {
	if index := strings.LastIndex(name, "."); index >= 0 {
		return name[index+1:]
	}
	return name
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

func sameIdentifierText(identifier, match string) bool {
	canonical := func(value string) string {
		return strings.NewReplacer("_", "", " ", "").Replace(strings.ToLower(value))
	}
	return canonical(identifier) == canonical(match)
}

func containsSymbol(symbols []Symbol, candidate Symbol) bool {
	for _, symbol := range symbols {
		if symbol.Line == candidate.Line && symbol.Column == candidate.Column && symbol.Name == candidate.Name {
			return true
		}
	}
	return false
}
