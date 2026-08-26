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

type symbolMatch struct {
	Line   int
	Column int
	Text   string
}

type symbolProvider interface {
	Supports(path string) bool
	Symbols(source []byte, matches []symbolMatch) ([]Symbol, error)
	Explore(source []byte, location symbolMatch) (Symbol, error)
}

var symbolProviders = []symbolProvider{goSymbolProvider{}}

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
		symbols, err := provider.Symbols(source, results[index].symbolMatches)
		if err != nil || len(symbols) == 0 {
			continue
		}
		if len(symbols) > maxSymbolsPerFile {
			symbols = symbols[:maxSymbolsPerFile]
		}
		results[index].Symbols = symbols
		results[index].Line = symbols[0].Line
		results[index].Column = symbols[0].Column
	}
	return results
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
	return provider.Explore(source, symbolMatch{Line: line, Column: column})
}
