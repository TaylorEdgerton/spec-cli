package discovery

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	maxSymbolsPerFile = 3
	maxSymbolMatches  = 16
)

// Symbol and RelatedSymbol are language-neutral views of structural or
// semantic code context. Providers translate their native representation into
// these types before discovery or the TUI sees it.
type Symbol struct {
	Name       string
	Kind       string
	Line       int
	Column     int
	Capability Capability
	Reasons    []string
	Related    []RelatedSymbol
}

type RelatedSymbol struct {
	Name       string
	Kind       string
	Path       string
	Line       int
	Column     int
	Capability Capability
	Relation   string
}

// Capability describes the strongest evidence supporting a symbol result.
// Search is lexical, structural is parser-backed, and precise is backed by a
// semantic code-intelligence index such as SCIP.
type Capability string

const (
	CapabilitySearch     Capability = "search"
	CapabilityStructural Capability = "structural"
	CapabilityPrecise    Capability = "precise"
)

type symbolMatch struct {
	Line   int
	Column int
	Text   string
	Kind   signalKind
}

type symbolProvider interface {
	Supports(path string) bool
	Symbols(root, path string, source []byte, matches []symbolMatch) ([]Symbol, error)
	Explore(root, path string, source []byte, location symbolMatch) (Symbol, error)
}

var symbolProviders = []symbolProvider{
	treeSitterSymbolProvider{},
}

func enrichSymbols(root string, results []Result) []Result {
	for index := range results {
		provider := providerFor(results[index].Path)
		if provider == nil || len(results[index].symbolMatches) == 0 {
			continue
		}
		source, err := readSearchableFile(filepath.Join(root, filepath.FromSlash(results[index].Path)))
		if err != nil {
			continue
		}
		symbols, err := provider.Symbols(root, results[index].Path, source, results[index].symbolMatches)
		if err != nil || len(symbols) == 0 {
			continue
		}
		if len(symbols) > maxSymbolsPerFile {
			symbols = symbols[:maxSymbolsPerFile]
		}
		markStructural(symbols)
		results[index].Symbols = symbols
		results[index].Line = symbols[0].Line
		results[index].Column = symbols[0].Column
	}
	return results
}

func markStructural(symbols []Symbol) {
	for index := range symbols {
		if symbols[index].Capability == "" {
			symbols[index].Capability = CapabilityStructural
		}
		for related := range symbols[index].Related {
			if symbols[index].Related[related].Capability == "" {
				symbols[index].Related[related].Capability = CapabilityStructural
			}
		}
	}
}

func markStructuralSymbol(symbol *Symbol) {
	if symbol.Capability == "" {
		symbol.Capability = CapabilityStructural
	}
	for index := range symbol.Related {
		if symbol.Related[index].Capability == "" {
			symbol.Related[index].Capability = CapabilityStructural
		}
	}
}

func providerFor(path string) symbolProvider {
	for _, provider := range symbolProviders {
		if provider.Supports(path) {
			return provider
		}
	}
	return nil
}

func Explore(root, path string, line, column int) (Symbol, error) {
	if precise, preciseOK := exploreSCIP(root, path, line, column); preciseOK {
		structural, structuralErr := exploreStructural(root, path, line, column)
		if structuralErr == nil {
			precise.Related = mergeRelationships(precise.Related, structural.Related)
		}
		return precise, nil
	}
	return exploreStructural(root, path, line, column)
}

func exploreStructural(root, path string, line, column int) (Symbol, error) {
	provider := providerFor(path)
	if provider == nil {
		return Symbol{}, fmt.Errorf("symbol discovery is unavailable for %s", path)
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return Symbol{}, fmt.Errorf("symbol path is outside the repository: %s", path)
	}
	source, err := readSearchableFile(filepath.Join(root, clean))
	if err != nil {
		return Symbol{}, err
	}
	structural, structuralErr := provider.Explore(root, filepath.ToSlash(clean), source, symbolMatch{Line: line, Column: column})
	if structuralErr == nil {
		markStructuralSymbol(&structural)
	}
	return structural, structuralErr
}

func enrichCodeContext(root string, results []Result) []Result {
	if _, err := loadSCIPProvider(root); err != nil {
		return enrichSymbols(root, results)
	}
	results = enrichSCIP(root, results)
	for index := range results {
		if len(results[index].Symbols) != 0 {
			continue
		}
		fallback := enrichSymbols(root, []Result{results[index]})
		if len(fallback) == 1 {
			results[index] = fallback[0]
		}
	}
	return results
}

func mergeRelationships(primary, fallback []RelatedSymbol) []RelatedSymbol {
	merged := append([]RelatedSymbol(nil), primary...)
	for _, candidate := range fallback {
		mergedAt := -1
		for index, existing := range merged {
			if existing.Path == candidate.Path && existing.Line == candidate.Line && existing.Column == candidate.Column {
				if existing.Relation == candidate.Relation || overlappingIncomingRelationship(existing.Relation, candidate.Relation) {
					mergedAt = index
				}
				break
			}
		}
		if mergedAt < 0 {
			merged = append(merged, candidate)
			continue
		}
		if relationshipSpecificity(candidate.Relation) > relationshipSpecificity(merged[mergedAt].Relation) {
			merged[mergedAt] = candidate
		}
	}
	return merged
}

func overlappingIncomingRelationship(left, right string) bool {
	return isIncomingReference(left) && isIncomingReference(right)
}

func isIncomingReference(relation string) bool {
	relation = strings.ToLower(strings.TrimSpace(relation))
	return strings.HasPrefix(relation, "called by ") || strings.HasPrefix(relation, "referenced by ") || strings.HasPrefix(relation, "tests ")
}

func relationshipSpecificity(relation string) int {
	relation = strings.ToLower(strings.TrimSpace(relation))
	switch {
	case strings.HasPrefix(relation, "called by "):
		return 2
	case strings.HasPrefix(relation, "tests "):
		return 1
	default:
		return 0
	}
}
