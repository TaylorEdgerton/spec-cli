package discovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	scip "github.com/scip-code/scip/bindings/go/scip"
)

const maxSCIPRelationships = 8

type PrecisionAvailability string

const (
	PrecisionUnavailable PrecisionAvailability = "unavailable"
	PrecisionInvalid     PrecisionAvailability = "invalid"
	PrecisionStale       PrecisionAvailability = "stale"
	PrecisionAvailable   PrecisionAvailability = "available"
)

var errSCIPStale = errors.New("SCIP index is older than repository source")

// codeIntelligenceProvider is deliberately language-agnostic. A provider maps
// a repository location into the same Symbol model used by the TUI.
type codeIntelligenceProvider interface {
	Explore(path string, line, column int) (Symbol, bool)
}

type scipOccurrence struct {
	symbol    string
	path      string
	location  RelatedSymbol
	range_    scip.Range
	enclosing scip.Range
	hasOwner  bool
	roles     int32
}

type scipSymbolInfo struct {
	name          string
	kind          string
	relationships []*scip.Relationship
}

type scipProvider struct {
	byPath          map[string][]scipOccurrence
	bySymbol        map[string][]scipOccurrence
	info            map[string]scipSymbolInfo
	implementations map[string][]string
}

var _ codeIntelligenceProvider = (*scipProvider)(nil)

type scipCacheEntry struct {
	indexPath string
	size      int64
	modTime   int64
	provider  *scipProvider
	err       error
}

var scipCache = struct {
	sync.Mutex
	entries map[string]scipCacheEntry
}{entries: make(map[string]scipCacheEntry)}

// SCIPAvailability reports whether this repository has a usable semantic
// index. It never changes discovery behavior or attempts to create an index.
func SCIPAvailability(root string) PrecisionAvailability {
	_, err := loadSCIPProvider(root)
	switch {
	case err == nil:
		return PrecisionAvailable
	case errors.Is(err, os.ErrNotExist):
		return PrecisionUnavailable
	case errors.Is(err, errSCIPStale):
		return PrecisionStale
	default:
		return PrecisionInvalid
	}
}

func enrichSCIP(root string, results []Result) []Result {
	provider, err := loadSCIPProvider(root)
	if err != nil {
		return results
	}
	for resultIndex := range results {
		var exact Symbol
		exactOK := false
		if len(results[resultIndex].symbolMatches) > 0 {
			exact, exactOK = provider.ExactDefinition(results[resultIndex].Path, results[resultIndex].symbolMatches)
			if exactOK {
				if structural, structuralErr := exploreStructural(root, results[resultIndex].Path, exact.Line, exact.Column); structuralErr == nil {
					exact.Reasons = appendUniqueStrings(exact.Reasons, structural.Reasons...)
					exact.Related = mergeRelationships(exact.Related, structural.Related)
				}
			}
		}
		for symbolIndex := range results[resultIndex].Symbols {
			symbol := &results[resultIndex].Symbols[symbolIndex]
			precise, ok := provider.Explore(results[resultIndex].Path, symbol.Line, symbol.Column)
			if !ok {
				continue
			}
			precise.Reasons = appendUniqueStrings(precise.Reasons, symbol.Reasons...)
			precise.Related = mergeRelationships(precise.Related, symbol.Related)
			*symbol = precise
		}
		if exactOK {
			exact.Reasons = appendUniqueStrings(exact.Reasons, results[resultIndex].Reasons...)
			ordered := []Symbol{exact}
			for _, symbol := range results[resultIndex].Symbols {
				if !sameDiscoveredSymbol(exact, symbol) {
					ordered = append(ordered, symbol)
				}
				if len(ordered) == maxSymbolsPerFile {
					break
				}
			}
			results[resultIndex].Symbols = ordered
		}
		if len(results[resultIndex].Symbols) > 0 {
			results[resultIndex].Line = results[resultIndex].Symbols[0].Line
			results[resultIndex].Column = results[resultIndex].Symbols[0].Column
		}
		if len(results[resultIndex].Symbols) == 0 {
			precise, ok := provider.Explore(results[resultIndex].Path, results[resultIndex].Line, results[resultIndex].Column)
			if ok {
				if structural, structuralErr := exploreStructural(root, results[resultIndex].Path, precise.Line, precise.Column); structuralErr == nil {
					precise.Related = mergeRelationships(precise.Related, structural.Related)
				}
				results[resultIndex].Symbols = []Symbol{precise}
				results[resultIndex].Line = precise.Line
				results[resultIndex].Column = precise.Column
			}
		}
	}
	return results
}

func (provider *scipProvider) ExactDefinition(path string, matches []symbolMatch) (Symbol, bool) {
	path, ok := cleanSCIPPath(path)
	if !ok {
		return Symbol{}, false
	}
	for _, match := range matches {
		if match.Kind != kindIdentifier {
			continue
		}
		occurrence, found := provider.occurrenceAt(path, match.Line, match.Column)
		if !found {
			continue
		}
		info := provider.symbolInfo(occurrence.symbol)
		if (info.kind != "function" && info.kind != "method" && info.kind != "type") || !sameSCIPQueryName(match.Text, info.name) {
			continue
		}
		definitions := provider.definitions(occurrence.symbol)
		if len(definitions) > 0 {
			definition := definitions[0]
			return provider.Explore(definition.path, definition.location.Line, definition.location.Column)
		}
		return provider.Explore(path, match.Line, match.Column)
	}
	return Symbol{}, false
}

func sameSCIPQueryName(query, name string) bool {
	normalize := func(value string) string {
		value = strings.TrimSpace(strings.ToLower(value))
		return strings.TrimSuffix(value, "()")
	}
	return normalize(query) != "" && normalize(query) == normalize(name)
}

func sameDiscoveredSymbol(left, right Symbol) bool {
	return left.Name == right.Name && left.Line == right.Line && left.Column == right.Column
}

func exploreSCIP(root, path string, line, column int) (Symbol, bool) {
	provider, err := loadSCIPProvider(root)
	if err != nil {
		return Symbol{}, false
	}
	return provider.Explore(path, line, column)
}

func loadSCIPProvider(root string) (*scipProvider, error) {
	indexPath, info, err := findSCIPIndex(root)
	if err != nil {
		return nil, err
	}
	cacheKey, err := filepath.Abs(root)
	if err != nil {
		cacheKey = filepath.Clean(root)
	}
	scipCache.Lock()
	entry, ok := scipCache.entries[cacheKey]
	scipCache.Unlock()
	if ok && entry.indexPath == indexPath && entry.size == info.Size() && entry.modTime == info.ModTime().UnixNano() {
		return entry.provider, entry.err
	}
	provider, err := readSCIPIndex(root, indexPath)
	if err == nil && provider.sourcesNewerThan(root, info.ModTime()) {
		provider, err = nil, errSCIPStale
	}
	scipCache.Lock()
	scipCache.entries[cacheKey] = scipCacheEntry{
		indexPath: indexPath, size: info.Size(), modTime: info.ModTime().UnixNano(), provider: provider, err: err,
	}
	scipCache.Unlock()
	return provider, err
}

func (provider *scipProvider) sourcesNewerThan(root string, indexTime time.Time) bool {
	for path := range provider.byPath {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		if err == nil && info.ModTime().After(indexTime) {
			return true
		}
	}
	return false
}

func findSCIPIndex(root string) (string, os.FileInfo, error) {
	for _, relative := range []string{"index.scip", filepath.Join(".scip", "index.scip")} {
		path := filepath.Join(root, relative)
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() {
			return path, info, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return "", nil, err
		}
	}
	return "", nil, os.ErrNotExist
}

func readSCIPIndex(root, indexPath string) (*scipProvider, error) {
	file, err := os.Open(indexPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	provider := &scipProvider{
		byPath: make(map[string][]scipOccurrence), bySymbol: make(map[string][]scipOccurrence),
		info: make(map[string]scipSymbolInfo), implementations: make(map[string][]string),
	}
	documents := 0
	visitor := scip.IndexVisitor{
		VisitDocument: func(_ context.Context, document *scip.Document) error {
			if provider.addDocument(root, document) {
				documents++
			}
			return nil
		},
		VisitExternalSymbol: func(_ context.Context, info *scip.SymbolInformation) error {
			provider.addSymbolInfo(info)
			return nil
		},
	}
	if err := visitor.ParseStreaming(context.Background(), file); err != nil {
		return nil, fmt.Errorf("read SCIP index: %w", err)
	}
	if documents == 0 {
		return nil, fmt.Errorf("SCIP index contains no documents")
	}
	provider.buildRelationships()
	return provider, nil
}

func (provider *scipProvider) addDocument(root string, document *scip.Document) bool {
	path, ok := cleanSCIPPath(document.GetRelativePath())
	if !ok {
		return false
	}
	source := document.GetText()
	if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path))); err == nil {
		source = string(data)
	}
	sourceLines := strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")
	for _, info := range document.GetSymbols() {
		provider.addSymbolInfo(info)
	}
	for _, occurrence := range document.GetOccurrences() {
		if occurrence.GetSymbol() == "" || scip.SymbolRole_Generated.Matches(occurrence) {
			continue
		}
		range_, ok := occurrence.SourceRange()
		if !ok {
			continue
		}
		start := scipLocation(path, sourceLines, document.GetPositionEncoding(), range_.Start)
		end := scipLocation(path, sourceLines, document.GetPositionEncoding(), range_.End)
		range_.Start.Character = int32(start.Column - 1)
		range_.End.Character = int32(end.Column - 1)
		indexed := scipOccurrence{
			symbol: occurrence.GetSymbol(), path: path, range_: range_, roles: occurrence.GetSymbolRoles(),
			location: RelatedSymbol{Path: path, Line: start.Line, Column: start.Column, Capability: CapabilityPrecise},
		}
		if enclosing, ok := occurrence.EnclosingSourceRange(); ok {
			enclosingStart := scipLocation(path, sourceLines, document.GetPositionEncoding(), enclosing.Start)
			enclosingEnd := scipLocation(path, sourceLines, document.GetPositionEncoding(), enclosing.End)
			enclosing.Start.Character = int32(enclosingStart.Column - 1)
			enclosing.End.Character = int32(enclosingEnd.Column - 1)
			indexed.enclosing, indexed.hasOwner = enclosing, true
		}
		provider.byPath[path] = append(provider.byPath[path], indexed)
		provider.bySymbol[indexed.symbol] = append(provider.bySymbol[indexed.symbol], indexed)
	}
	return true
}

func cleanSCIPPath(path string) (string, bool) {
	if strings.Contains(path, "\\") {
		return "", false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if clean == "." || filepath.IsAbs(filepath.FromSlash(clean)) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

func scipLocation(path string, lines []string, encoding scip.PositionEncoding, position scip.Position) RelatedSymbol {
	line := int(position.Line) + 1
	column := int(position.Character) + 1
	if int(position.Line) >= 0 && int(position.Line) < len(lines) {
		column = scipRuneColumn(lines[position.Line], int(position.Character), encoding)
	}
	return RelatedSymbol{Path: path, Line: line, Column: max(1, column), Capability: CapabilityPrecise}
}

func scipRuneColumn(line string, offset int, encoding scip.PositionEncoding) int {
	if offset <= 0 {
		return 1
	}
	switch encoding {
	case scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart:
		if offset > len(line) {
			offset = len(line)
		}
		return utf8.RuneCountInString(line[:offset]) + 1
	case scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart:
		units := 0
		runes := 0
		for _, value := range line {
			width := len(utf16.Encode([]rune{value}))
			if units+width > offset {
				break
			}
			units += width
			runes++
		}
		return runes + 1
	case scip.PositionEncoding_UTF32CodeUnitOffsetFromLineStart:
		count := utf8.RuneCountInString(line)
		if offset > count {
			offset = count
		}
		return offset + 1
	default:
		return offset + 1
	}
}

func (provider *scipProvider) addSymbolInfo(raw *scip.SymbolInformation) {
	if raw == nil || raw.GetSymbol() == "" {
		return
	}
	info := provider.info[raw.GetSymbol()]
	if raw.GetDisplayName() != "" {
		info.name = raw.GetDisplayName()
	}
	if raw.GetKind() != scip.SymbolInformation_UnspecifiedKind {
		info.kind = scipKind(raw.GetKind())
	}
	info.relationships = append(info.relationships, raw.GetRelationships()...)
	provider.info[raw.GetSymbol()] = info
}

func (provider *scipProvider) buildRelationships() {
	for symbol, info := range provider.info {
		for _, relationship := range info.relationships {
			if relationship.GetIsImplementation() && relationship.GetSymbol() != "" {
				provider.implementations[relationship.GetSymbol()] = appendUniqueStrings(provider.implementations[relationship.GetSymbol()], symbol)
			}
		}
	}
}

func (provider *scipProvider) Explore(path string, line, column int) (Symbol, bool) {
	path, ok := cleanSCIPPath(path)
	if !ok {
		return Symbol{}, false
	}
	selected, ok := provider.occurrenceAt(path, line, column)
	if !ok {
		return Symbol{}, false
	}
	info := provider.symbolInfo(selected.symbol)
	root := Symbol{
		Name: info.name, Kind: info.kind, Line: selected.location.Line, Column: selected.location.Column,
		Capability: CapabilityPrecise, Reasons: []string{"exact SCIP symbol match"},
	}
	for _, occurrence := range provider.bySymbol[selected.symbol] {
		switch {
		case scipRoleMatches(occurrence.roles, scip.SymbolRole_Definition):
			if !sameSCIPLocation(selected, occurrence) {
				related := provider.occurrenceSymbol(occurrence)
				related.Relation = "defined as " + related.Name
				root.Related = appendUniqueRelated(root.Related, related)
			}
		case scipRoleMatches(occurrence.roles, scip.SymbolRole_Import), scipRoleMatches(occurrence.roles, scip.SymbolRole_Generated):
			continue
		default:
			if sameSCIPLocation(selected, occurrence) {
				continue
			}
			owner, ownerOK := provider.ownerOf(occurrence)
			if ownerOK {
				related := provider.occurrenceSymbol(owner)
				related.Relation = "referenced by " + related.Name
				if scipRoleMatches(occurrence.roles, scip.SymbolRole_Test) || looksLikeTest(occurrence.path) {
					related.Kind = "test"
					related.Relation = "tests " + root.Name
				}
				root.Related = appendUniqueRelated(root.Related, related)
			} else {
				related := provider.occurrenceSymbol(occurrence)
				related.Name = filepath.Base(occurrence.path)
				related.Kind = "file"
				related.Relation = "references " + root.Name
				root.Related = appendUniqueRelated(root.Related, related)
			}
		}
	}
	for _, implementation := range provider.implementations[selected.symbol] {
		for _, definition := range provider.definitions(implementation) {
			related := provider.occurrenceSymbol(definition)
			related.Relation = "implemented by " + related.Name
			root.Related = appendUniqueRelated(root.Related, related)
		}
	}
	for _, relationship := range info.relationships {
		if !relationship.GetIsDefinition() {
			continue
		}
		for _, definition := range provider.definitions(relationship.GetSymbol()) {
			related := provider.occurrenceSymbol(definition)
			related.Relation = "defined as " + related.Name
			root.Related = appendUniqueRelated(root.Related, related)
		}
	}
	sort.SliceStable(root.Related, func(i, j int) bool {
		left, right := scipRelationshipPriority(root.Related[i]), scipRelationshipPriority(root.Related[j])
		if left != right {
			return left < right
		}
		if root.Related[i].Path != root.Related[j].Path {
			return root.Related[i].Path < root.Related[j].Path
		}
		return root.Related[i].Line < root.Related[j].Line
	})
	if len(root.Related) > maxSCIPRelationships {
		root.Related = root.Related[:maxSCIPRelationships]
	}
	return root, true
}

func scipRelationshipPriority(related RelatedSymbol) int {
	relation := strings.ToLower(related.Relation)
	switch {
	case strings.HasPrefix(relation, "defined "):
		return 0
	case strings.HasPrefix(relation, "implemented by "):
		return 1
	case related.Kind == "test", strings.HasPrefix(relation, "tests "):
		return 2
	default:
		return 3
	}
}

func (provider *scipProvider) occurrenceAt(path string, line, column int) (scipOccurrence, bool) {
	position := scip.Position{Line: int32(max(1, line) - 1), Character: int32(max(1, column) - 1)}
	var matches []scipOccurrence
	for _, occurrence := range provider.byPath[path] {
		if occurrence.range_.Contains(position) || occurrence.range_.Start == position {
			matches = append(matches, occurrence)
		}
	}
	if len(matches) == 0 {
		return scipOccurrence{}, false
	}
	sort.SliceStable(matches, func(i, j int) bool {
		left := rangeSize(matches[i].range_)
		right := rangeSize(matches[j].range_)
		if left != right {
			return left < right
		}
		leftDefinition := scipRoleMatches(matches[i].roles, scip.SymbolRole_Definition)
		rightDefinition := scipRoleMatches(matches[j].roles, scip.SymbolRole_Definition)
		return leftDefinition && !rightDefinition
	})
	return matches[0], true
}

func rangeSize(value scip.Range) int64 {
	return int64(value.End.Line-value.Start.Line)*1_000_000 + int64(value.End.Character-value.Start.Character)
}

func (provider *scipProvider) ownerOf(reference scipOccurrence) (scipOccurrence, bool) {
	var owners []scipOccurrence
	position := reference.range_.Start
	for _, candidate := range provider.byPath[reference.path] {
		if !scipRoleMatches(candidate.roles, scip.SymbolRole_Definition) || !candidate.hasOwner || candidate.symbol == reference.symbol {
			continue
		}
		if candidate.enclosing.Contains(position) {
			owners = append(owners, candidate)
		}
	}
	if len(owners) == 0 {
		return scipOccurrence{}, false
	}
	sort.SliceStable(owners, func(i, j int) bool { return rangeSize(owners[i].enclosing) < rangeSize(owners[j].enclosing) })
	return owners[0], true
}

func (provider *scipProvider) definitions(symbol string) []scipOccurrence {
	var definitions []scipOccurrence
	for _, occurrence := range provider.bySymbol[symbol] {
		if scipRoleMatches(occurrence.roles, scip.SymbolRole_Definition) {
			definitions = append(definitions, occurrence)
		}
	}
	return definitions
}

func (provider *scipProvider) occurrenceSymbol(occurrence scipOccurrence) RelatedSymbol {
	info := provider.symbolInfo(occurrence.symbol)
	result := occurrence.location
	result.Name, result.Kind = info.name, info.kind
	if scipRoleMatches(occurrence.roles, scip.SymbolRole_Test) || looksLikeTest(occurrence.path) {
		result.Kind = "test"
	}
	return result
}

func (provider *scipProvider) symbolInfo(symbol string) scipSymbolInfo {
	info := provider.info[symbol]
	if info.name == "" {
		info.name = scipSymbolName(symbol)
	}
	if info.name == "" {
		info.name = "symbol"
	}
	if info.kind == "" {
		info.kind = "symbol"
	}
	if (info.kind == "function" || info.kind == "method") && !strings.HasSuffix(info.name, "()") {
		info.name += "()"
	}
	return info
}

func scipSymbolName(symbol string) string {
	parsed, err := scip.ParseSymbol(symbol)
	if err != nil || len(parsed.GetDescriptors()) == 0 {
		if strings.HasPrefix(symbol, "local ") {
			return strings.TrimPrefix(symbol, "local ")
		}
		return ""
	}
	return parsed.GetDescriptors()[len(parsed.GetDescriptors())-1].GetName()
}

func scipKind(kind scip.SymbolInformation_Kind) string {
	switch kind {
	case scip.SymbolInformation_Function:
		return "function"
	case scip.SymbolInformation_Method, scip.SymbolInformation_AbstractMethod, scip.SymbolInformation_StaticMethod,
		scip.SymbolInformation_ProtocolMethod, scip.SymbolInformation_TraitMethod:
		return "method"
	case scip.SymbolInformation_Class, scip.SymbolInformation_Struct, scip.SymbolInformation_Interface,
		scip.SymbolInformation_Trait, scip.SymbolInformation_Type, scip.SymbolInformation_TypeAlias:
		return "type"
	case scip.SymbolInformation_Constant, scip.SymbolInformation_EnumMember:
		return "constant"
	case scip.SymbolInformation_Variable, scip.SymbolInformation_StaticVariable, scip.SymbolInformation_Field,
		scip.SymbolInformation_StaticField, scip.SymbolInformation_Property:
		return "variable"
	case scip.SymbolInformation_File:
		return "file"
	default:
		return "symbol"
	}
}

func scipRoleMatches(roles int32, role scip.SymbolRole) bool {
	return roles&int32(role) != 0
}

func sameSCIPLocation(left, right scipOccurrence) bool {
	return left.path == right.path && left.range_.Start == right.range_.Start
}

func appendUniqueRelated(existing []RelatedSymbol, candidate RelatedSymbol) []RelatedSymbol {
	for _, item := range existing {
		if item.Path == candidate.Path && item.Line == candidate.Line && item.Column == candidate.Column && item.Relation == candidate.Relation {
			return existing
		}
	}
	return append(existing, candidate)
}

func appendUniqueStrings(existing []string, values ...string) []string {
	for _, value := range values {
		found := false
		for _, item := range existing {
			if item == value {
				found = true
				break
			}
		}
		if value != "" && !found {
			existing = append(existing, value)
		}
	}
	return existing
}
