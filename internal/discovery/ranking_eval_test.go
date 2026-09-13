package discovery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

type rankingEvaluationCase struct {
	Query  string `json:"query"`
	Path   string `json:"path"`
	Symbol string `json:"symbol,omitempty"`
}

type rankingEvaluationFixture struct {
	Cases []rankingEvaluationCase `json:"cases"`
}

type rankingMetrics struct {
	top1       int
	top3       int
	reciprocal float64
	symbols    int
	symbolHits int
}

type rankingOutcome struct {
	results []Result
	rank    int
}

type exactSymbolIndex map[string][]Result

var exactQueryIdentifierPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

type fusionEntry struct {
	result   Result
	score    float64
	bestRank int
}

// TestRankerComparisonCorpus is an evaluation harness, not an assertion that
// an experimental strategy wins. Every case has an explicit target in this
// repository so retrieval changes can be compared with the production BM25
// baseline against the same fixed workload with:
//
//	go test ./internal/discovery -run TestRankerComparisonCorpus -v
func TestRankerComparisonCorpus(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cases := rankingEvaluationCases(t, root)
	if len(cases) != 100 {
		t.Fatalf("evaluation cases = %d, want the fixed 100-query baseline", len(cases))
	}
	for _, evaluation := range cases {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(evaluation.Path))); err != nil {
			t.Fatalf("target %s for %q: %v", evaluation.Path, evaluation.Query, err)
		}
	}
	files, err := repositoryFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	files = withoutEvaluationHarness(files)
	symbols := buildExactSymbolIndex(root, files)

	strategyNames := []string{
		"A BM25",
		"B BM25 + exact symbol -> RRF",
		"C BM25 + exact symbol + path -> RRF",
	}
	metrics := make([]rankingMetrics, len(strategyNames))
	outcomes := make([][]rankingOutcome, len(strategyNames))
	for index := range outcomes {
		outcomes[index] = make([]rankingOutcome, 0, len(cases))
	}
	symbolChecks := 0
	for _, evaluation := range cases {
		corpus := prepareLexicalCorpusFiles(root, Query{Intent: evaluation.Query}, files)
		bm25Results := rankLexicalCorpus(corpus, rankerBM25)
		exactSymbols := exactSymbolResults(symbols, evaluation.Query)
		strategies := [][]Result{
			bm25Results,
			reciprocalRankFusion(bm25Results, exactSymbols),
			reciprocalRankFusion(bm25Results, exactSymbols, exactPathResults(corpus)),
		}
		for index, results := range strategies {
			rank := resultRank(results, evaluation.Path)
			outcomes[index] = append(outcomes[index], rankingOutcome{results: results, rank: rank})
			updateRankingMetrics(&metrics[index], rank)
		}
		if evaluation.Symbol != "" && symbolChecks < 12 {
			symbolChecks++
			for index, results := range strategies {
				metrics[index].symbols++
				if symbolInTopResults(root, results, evaluation) {
					metrics[index].symbolHits++
				}
			}
		}
	}
	for index, name := range strategyNames {
		metric := metrics[index]
		t.Logf("%s: top1=%d/%d top3=%d/%d MRR=%.3f symbols=%d/%d", name, metric.top1, len(cases), metric.top3, len(cases), metric.reciprocal/float64(len(cases)), metric.symbolHits, metric.symbols)
	}

	for index, evaluation := range cases {
		baseline := outcomes[0][index]
		for strategy := 1; strategy < len(strategyNames); strategy++ {
			candidate := outcomes[strategy][index]
			if baseline.rank != candidate.rank {
				t.Logf("%q\n  target: %s\n  A: rank %s %s\n  %c: rank %s %s", evaluation.Query, evaluation.Path, displayRank(baseline.rank), resultPaths(baseline.results), 'A'+rune(strategy), displayRank(candidate.rank), resultPaths(candidate.results))
			}
		}
	}
}

func buildExactSymbolIndex(root string, files []string) exactSymbolIndex {
	index := make(exactSymbolIndex)
	for _, path := range files {
		if providerFor(path) == nil {
			continue
		}
		source, err := readSearchableFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil || looksGenerated(string(source)) {
			continue
		}
		document, err := parseTreeDocument(path, source)
		if err != nil {
			continue
		}
		for _, definition := range document.definitions {
			result := Result{
				Path: path, Line: definition.Line, Column: definition.Column,
				Reasons: []string{"exact symbol match"}, Symbols: []Symbol{definition.Symbol},
			}
			key := canonicalIdentifier(definition.identifier)
			index[key] = append(index[key], result)
		}
		document.release()
	}
	for key := range index {
		sort.SliceStable(index[key], func(i, j int) bool {
			if index[key][i].Path != index[key][j].Path {
				return index[key][i].Path < index[key][j].Path
			}
			return index[key][i].Line < index[key][j].Line
		})
	}
	return index
}

func exactSymbolResults(index exactSymbolIndex, query string) []Result {
	type candidate struct {
		result Result
		score  float64
	}
	byPath := make(map[string]candidate)
	for _, raw := range exactQueryIdentifierPattern.FindAllString(query, -1) {
		if utf8.RuneCountInString(raw) < 3 {
			continue
		}
		matches := index[canonicalIdentifier(raw)]
		if len(matches) == 0 {
			continue
		}
		weight := 1.0
		if strings.Contains(raw, "_") || containsUpperAfterFirst(raw) {
			weight = 2.0
		}
		for _, result := range matches {
			current := byPath[result.Path]
			current.score += weight
			if current.result.Path == "" || weight > current.result.score {
				result.score = weight
				current.result = result
			}
			byPath[result.Path] = current
		}
	}
	candidates := make([]candidate, 0, len(byPath))
	for _, value := range byPath {
		candidates = append(candidates, value)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].result.Path < candidates[j].result.Path
	})
	results := make([]Result, 0, len(candidates))
	for _, candidate := range candidates {
		results = append(results, candidate.result)
	}
	return results
}

func exactPathResults(corpus lexicalCorpus) []Result {
	var results []Result
	for _, file := range corpus.candidates {
		score := 0.0
		for _, evidence := range file.evidence.matches {
			if !evidence.path {
				continue
			}
			contribution := corpus.signals[evidence.signal].weight
			if evidence.filename {
				contribution *= 2
			}
			score += contribution
		}
		if score > 0 {
			results = append(results, Result{Path: file.evidence.path, Line: 1, Column: 1, score: score, Reasons: []string{"exact path match"}})
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return results[i].Path < results[j].Path
	})
	return results
}

func reciprocalRankFusion(rankings ...[]Result) []Result {
	const rankConstant = 60.0
	entries := make(map[string]fusionEntry)
	for _, ranking := range rankings {
		seen := make(map[string]bool)
		for index, result := range ranking {
			if seen[result.Path] {
				continue
			}
			seen[result.Path] = true
			rank := index + 1
			entry := entries[result.Path]
			entry.score += 1 / (rankConstant + float64(rank))
			if entry.result.Path == "" || (len(entry.result.Symbols) == 0 && len(result.Symbols) > 0) {
				entry.result = result
			}
			if entry.bestRank == 0 || rank < entry.bestRank {
				entry.bestRank = rank
			}
			entries[result.Path] = entry
		}
	}
	ordered := make([]fusionEntry, 0, len(entries))
	for _, entry := range entries {
		ordered = append(ordered, entry)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].score != ordered[j].score {
			return ordered[i].score > ordered[j].score
		}
		if ordered[i].bestRank != ordered[j].bestRank {
			return ordered[i].bestRank < ordered[j].bestRank
		}
		return ordered[i].result.Path < ordered[j].result.Path
	})
	results := make([]Result, 0, min(len(ordered), MaxResults))
	for _, entry := range ordered {
		entry.result.score = entry.score
		results = append(results, entry.result)
		if len(results) == MaxResults {
			break
		}
	}
	return results
}

func canonicalIdentifier(value string) string {
	return strings.NewReplacer("_", "", " ", "").Replace(strings.ToLower(value))
}

func TestExactSymbolResultsMatchWholeNormalizedIdentifier(t *testing.T) {
	index := exactSymbolIndex{
		canonicalIdentifier("rankFile"): {{Path: "internal/discovery/discovery.go"}},
	}
	if results := exactSymbolResults(index, "rank repository files"); len(results) != 0 {
		t.Fatalf("partial symbol tokens returned %v", resultPaths(results))
	}
	results := exactSymbolResults(index, "change rank_file() behaviour")
	if len(results) != 1 || results[0].Path != "internal/discovery/discovery.go" {
		t.Fatalf("exact normalized symbol results = %v", resultPaths(results))
	}
}

func TestReciprocalRankFusionRewardsAgreement(t *testing.T) {
	bm25 := []Result{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}}
	symbols := []Result{{Path: "b.go", Symbols: []Symbol{{Name: "target()"}}}, {Path: "d.go"}}
	results := reciprocalRankFusion(bm25, symbols)
	if len(results) != 4 || results[0].Path != "b.go" {
		t.Fatalf("fused results = %v, want b.go first", resultPaths(results))
	}
	if len(results[0].Symbols) != 1 || results[0].Symbols[0].Name != "target()" {
		t.Fatalf("fused exact-symbol metadata = %+v", results[0].Symbols)
	}
}

func withoutEvaluationHarness(files []string) []string {
	filtered := make([]string, 0, len(files))
	for _, file := range files {
		if filepath.Base(file) != "ranking_eval_test.go" {
			filtered = append(filtered, file)
		}
	}
	return filtered
}

func rankingEvaluationCases(t *testing.T, root string) []rankingEvaluationCase {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "internal", "discovery", "testdata", "ranking_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture rankingEvaluationFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture.Cases
}

func updateRankingMetrics(metrics *rankingMetrics, rank int) {
	if rank == 1 {
		metrics.top1++
	}
	if rank > 0 {
		metrics.reciprocal += 1 / float64(rank)
	}
	if rank > 0 && rank <= 3 {
		metrics.top3++
	}
}

func symbolInTopResults(root string, results []Result, evaluation rankingEvaluationCase) bool {
	if rank := resultRank(results, evaluation.Path); rank == 0 || rank > 3 {
		return false
	}
	for _, result := range results {
		if result.Path != evaluation.Path {
			continue
		}
		enriched := enrichSymbols(root, []Result{result})
		if len(enriched) == 0 {
			return false
		}
		for _, symbol := range enriched[0].Symbols {
			if symbol.Name == evaluation.Symbol {
				return true
			}
		}
	}
	return false
}

func resultRank(results []Result, expected string) int {
	for index, result := range results {
		if result.Path == expected {
			return index + 1
		}
	}
	return 0
}

func displayRank(rank int) string {
	if rank == 0 {
		return "miss"
	}
	return fmt.Sprintf("%d", rank)
}

func resultPaths(results []Result) string {
	paths := make([]string, 0, len(results))
	for _, result := range results {
		paths = append(paths, result.Path)
	}
	return "[" + strings.Join(paths, ", ") + "]"
}
