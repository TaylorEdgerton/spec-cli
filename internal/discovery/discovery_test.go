package discovery

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TaylorEdgerton/spec-cli/internal/textfilter"
)

func TestFindRanksTextPathsAndTestsWithoutRG(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", "")
	writeFile(t, root, "src/core/health.py", "def check_database_health():\n    return database.status()\n")
	writeFile(t, root, "src/core/database.py", "from health import check_database_health\n")
	writeFile(t, root, "tests/test_health.py", "def test_database_health():\n    check_database_health()\n")
	writeFile(t, root, "docs/unrelated.txt", "release notes\n")

	results, err := Find(root, Query{
		Intent:   "Improve database health reporting",
		Outcome:  "The database health check reports failures",
		Criteria: []string{"check_database_health returns the current status"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %+v", results)
	}
	wanted := map[string]bool{
		"src/core/health.py":   false,
		"src/core/database.py": false,
		"tests/test_health.py": false,
	}
	for _, result := range results {
		if len(result.Reasons) == 0 {
			t.Fatalf("result has no reason: %+v", result)
		}
		if result.Line < 1 || result.Column < 1 || result.Preview == "" {
			t.Fatalf("result has no exact location: %+v", result)
		}
		if result.Path == "tests/test_health.py" && !containsReason(result.Reasons, "related test file") {
			t.Fatalf("test result has no test reason: %+v", result)
		}
		if _, ok := wanted[result.Path]; ok {
			wanted[result.Path] = true
		}
	}
	for path, found := range wanted {
		if !found {
			t.Errorf("missing %s from %+v", path, results)
		}
	}
}

func containsReason(reasons []string, expected string) bool {
	for _, reason := range reasons {
		if reason == expected {
			return true
		}
	}
	return false
}

func TestFindBoundsResults(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", "")
	for index := 0; index < MaxResults+5; index++ {
		writeFile(t, root, filepath.Join("src", "health", string(rune('a'+index))+".txt"), "database health\n")
	}
	results, err := Find(root, Query{Intent: "database health"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != MaxResults {
		t.Fatalf("result count = %d, want %d", len(results), MaxResults)
	}
}

func TestFindUsesDocumentFrequencyToReduceGenericMatches(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", "")
	for _, name := range []string{"one.go", "two.go", "three.go", "four.go", "five.go", "six.go"} {
		writeFile(t, root, name, "package sample\n\nfunc handler() {}\n")
	}
	writeFile(t, root, "reconnect.go", "package sample\n\nfunc reconnectHandler() {}\n")

	results, err := Find(root, Query{
		Intent:  "Fix reconnect handling",
		Outcome: "The handler completes successfully",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Path != "reconnect.go" {
		t.Fatalf("intent-specific file was not ranked first: %+v", results)
	}
	if results[0].Line != 3 || results[0].Column != 6 {
		t.Fatalf("match location = %d:%d, want 3:6", results[0].Line, results[0].Column)
	}
}

func TestSignificantTokensNormaliseRepositoryTerms(t *testing.T) {
	tokens := significantTokens("Reconnect discovery.go")
	if len(tokens) != 2 || tokens[1].text != "discovery" ||
		tokens[0].term != textfilter.NormalizeEnglish("reconnect") ||
		tokens[1].term != textfilter.NormalizeEnglish("discovery") {
		t.Fatalf("tokens = %+v", tokens)
	}
}

func TestPathMatchingUsesExactNormalisedTokensWhileContentUsesStems(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/discover.go", "package sample\n")
	writeFile(t, root, "src/discovery.go", "package sample\n")
	writeFile(t, root, "src/other.go", "package sample\n\nfunc discovery() {}\n")
	signals := querySignals(Query{Intent: "discover"})

	exactPath := scanFile(root, "src/discover.go", signals)
	if !hasPathMatch(exactPath.evidence.matches) {
		t.Fatalf("exact path token did not match: %+v", exactPath)
	}
	stemmedPath := scanFile(root, "src/discovery.go", signals)
	if hasPathMatch(stemmedPath.evidence.matches) {
		t.Fatalf("stemmed path token counted as an exact match: %+v", stemmedPath)
	}
	stemmedContent := scanFile(root, "src/other.go", signals)
	if !hasContentMatch(stemmedContent.evidence.matches) {
		t.Fatalf("stemmed content term did not match: %+v", stemmedContent)
	}
}

func hasPathMatch(matches []match) bool {
	for _, match := range matches {
		if match.path {
			return true
		}
	}
	return false
}

func TestFindWeightsIntentAboveOutcome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", "")
	writeFile(t, root, "intent.go", "package sample\n\nfunc reconnect() {}\n")
	writeFile(t, root, "outcome.go", "package sample\n\nfunc dashboard() {}\n")
	writeFile(t, root, "other.go", "package sample\n")

	query := Query{Intent: "Reconnect safely", Outcome: "Update dashboard"}
	results, err := Find(root, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 || results[0].Path != "intent.go" {
		t.Fatalf("intent did not outrank outcome: %+v", results)
	}
}

func TestFindUsesGitRepositoryFilesAndIgnoresExcludedFiles(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	writeFile(t, root, ".gitignore", "ignored.go\n")
	writeFile(t, root, "tracked.go", "package sample\n\nfunc reconnect() {}\n")
	writeFile(t, root, "ignored.go", "package sample\n\nfunc reconnectDatabase() {}\n")
	runGit(t, root, "add", ".gitignore", "tracked.go")

	results, err := Find(root, Query{Intent: "Reconnect database"})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.Path == "ignored.go" {
			t.Fatalf("ignored file was discovered: %+v", results)
		}
	}
	if len(results) == 0 || results[0].Path != "tracked.go" {
		t.Fatalf("tracked repository file was not discovered: %+v", results)
	}
}

func TestFindRanksRepositorySpecificIntentPathsAboveGenericText(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	intents := []string{
		"modify discovery to use a library of ignored words instead of hard coded",
		"update discovery so ignored words come from a reusable source",
		"move stop word handling out of the discovery implementation",
		"replace the hardcoded ignored word map with a shared library",
		"clean up discovery keyword filtering so common words are handled centrally",
		"change how discovery filters insignificant words",
		"make discovery use reusable text filtering instead of local word lists",
		"refactor discovery query parsing to avoid maintaining ignored words manually",
		"improve discovery relevance by handling common words consistently",
		"centralise stop word filtering used by change context discovery",
		"remove the embedded ignored word list from discovery and use a standard implementation",
	}
	want := []string{"internal/discovery/discovery.go", "internal/discovery/discovery_test.go"}
	for _, intent := range intents {
		t.Run(intent, func(t *testing.T) {
			results, err := Find(root, Query{Intent: intent})
			if err != nil {
				t.Fatal(err)
			}
			if len(results) < len(want) {
				t.Fatalf("results = %+v", results)
			}
			for index, path := range want {
				if results[index].Path != path {
					t.Fatalf("result %d = %s, want %s; all results: %+v", index, results[index].Path, path, results)
				}
			}
			for _, result := range results[:len(want)] {
				if result.Path == "LICENSE" || (looksLikeTest(result.Path) && result.Path != want[1]) {
					t.Fatalf("generic result ranked above discovery context: %+v", results)
				}
			}
		})
	}
}

func TestFindExcludesLowConfidenceCandidatesFromReturnedSet(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/discovery.go", "package sample\n\nfunc discoverContext() {}\n")
	writeFile(t, root, "notes/modify.txt", "modify\n")
	writeFile(t, root, "notes/library.txt", "library\n")
	writeFile(t, root, "notes/content.txt", "content\n")

	results, err := Find(root, Query{Intent: "modify discovery content library"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Path != "src/discovery.go" {
		t.Fatalf("results = %+v", results)
	}
}

func TestFindPathMatchesAreStrongButNotAbsolute(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/database.go", "package sample\n")
	writeFile(t, root, "src/connection.go", "package sample\n\n// database recovery status reporting pipeline failure\n")

	results, err := Find(root, Query{Intent: "database recovery status reporting pipeline failure"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 || results[0].Path != "src/connection.go" {
		t.Fatalf("strong content evidence did not outrank a weak path match: %+v", results)
	}
}

func TestFindExcludesRepositoryMetadataAndGeneratedFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/discovery.go", "package sample\n\nfunc discoverContext() {}\n")
	writeFile(t, root, "LICENSE", "discovery context\n")
	writeFile(t, root, ".github/workflows/discovery.yml", "discovery context\n")
	writeFile(t, root, "vendor/discovery.go", "package vendor\n")
	writeFile(t, root, "src/discovery_generated.go", "// Code generated by fixture. DO NOT EDIT.\npackage sample\n")

	results, err := Find(root, Query{Intent: "improve discovery context"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Path != "src/discovery.go" {
		t.Fatalf("results = %+v", results)
	}
}

func TestFindOnlyPromotesTestsRelatedByPath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/discovery.go", "package sample\n\nfunc discoverContext() {}\n")
	writeFile(t, root, "tests/discovery_test.go", "package tests\n\nfunc testDiscoveryContext() {}\n")
	writeFile(t, root, "tests/unrelated_test.go", "package tests\n\n// discovery context\n")

	results, err := Find(root, Query{Intent: "improve discovery context"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 || results[0].Path != "src/discovery.go" || results[1].Path != "tests/discovery_test.go" {
		t.Fatalf("results = %+v", results)
	}
	for _, result := range results {
		if result.Path == "tests/unrelated_test.go" && containsReason(result.Reasons, "related test file") {
			t.Fatalf("unrelated test was promoted: %+v", result)
		}
	}
}

func TestFindExpandsConstantUsageToEnclosingFunction(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "discovery.go", `package discovery

type signalKind uint8

const kindWord signalKind = 0

func addFieldSignals() {
	words := significantWords()
	addSignal(kindWord)
	_ = words
}

func significantWords() []string { return nil }

func addSignal(kind signalKind) {
	somethingElse(kind)
}

func somethingElse(kind signalKind) {}
`)

	results, err := Find(root, Query{Intent: "change kindWord handling"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v", results)
	}
	symbols := results[0].Symbols
	if len(symbols) < 2 || symbols[0].Name != "addFieldSignals()" || !containsSymbolName(symbols, "kindWord") {
		t.Fatalf("symbols = %+v", symbols)
	}
	if !containsSymbolReason(symbols[0].Reasons, "uses kindWord") {
		t.Fatalf("usage reason = %+v", symbols[0])
	}
	for _, expected := range []string{"uses kindWord", "calls addSignal()", "receives words from significantWords()"} {
		if !containsRelatedReason(symbols[0].Related, expected) {
			t.Fatalf("direct relationships missing %q: %+v", expected, symbols[0].Related)
		}
	}
	if containsRelatedName(symbols[0].Related, "somethingElse()") {
		t.Fatalf("initial context expanded through addSignal: %+v", symbols[0].Related)
	}
	if containsSymbolName(symbols, "addSignal()") || containsSymbolName(symbols, "somethingElse()") {
		t.Fatalf("symbol expansion exceeded one hop: %+v", symbols)
	}

	addSignal := relatedNamed(symbols[0].Related, "addSignal()")
	drilled, err := Explore(root, "discovery.go", addSignal.Line, addSignal.Column)
	if err != nil {
		t.Fatal(err)
	}
	if drilled.Name != "addSignal()" || !containsRelatedReason(drilled.Related, "calls somethingElse()") {
		t.Fatalf("drilled context = %+v", drilled)
	}
}

func TestFindResolvesLexicalMatchToEnclosingFunction(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "tokens.go", `package discovery

func significantTokens() {
	discoveryFiltering := true
	_ = discoveryFiltering
}
`)

	results, err := Find(root, Query{Intent: "improve discovery filtering"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].Symbols) == 0 || results[0].Symbols[0].Name != "significantTokens()" {
		t.Fatalf("results = %+v", results)
	}
	if results[0].Line != 3 {
		t.Fatalf("result location = %d:%d, want enclosing function on line 3", results[0].Line, results[0].Column)
	}
}

func TestFindFallsBackWhenSymbolsAreUnsupportedOrParsingFails(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		content string
		line    int
		column  int
	}{
		{name: "unsupported language", path: "discovery.rb", content: "def discovery_context\nend\n", line: 1, column: 5},
		{name: "parser failure", path: "discovery.go", content: "package discovery\n\nfunc discoveryContext(\n", line: 3, column: 6},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, test.path, test.content)
			results, err := Find(root, Query{Intent: "discovery context"})
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || results[0].Path != test.path || results[0].Line != test.line ||
				results[0].Column != test.column || results[0].Preview == "" || len(results[0].Symbols) != 0 {
				t.Fatalf("fallback result = %+v", results)
			}
		})
	}
}

func TestFindDoesNotClaimCommentOrStringOccurrencesAreReferences(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "discovery.go", `package discovery

const kindWord = 1

func unrelated() {
	_ = "kindWord"
	// kindWord is documentation here.
}
`)

	results, err := Find(root, Query{Intent: "change kindWord handling"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !containsSymbolName(results[0].Symbols, "kindWord") {
		t.Fatalf("results = %+v", results)
	}
	for _, symbol := range results[0].Symbols {
		for _, reason := range symbol.Reasons {
			if strings.HasPrefix(reason, "uses kindWord") {
				t.Fatalf("text occurrence was labelled as a structural reference: %+v", results[0].Symbols)
			}
		}
		if containsRelatedName(symbol.Related, "unrelated()") {
			t.Fatalf("text occurrence was labelled as a structural relationship: %+v", symbol.Related)
		}
	}
}

func TestFindExpandsCurrentDiscoveryRegressionToBehaviouralSymbols(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	results, err := Find(root, Query{Intent: "modify discovery to use a library of ignored words instead of hard coded"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Path != "internal/discovery/discovery.go" {
		t.Fatalf("results = %+v", results)
	}
	symbols := results[0].Symbols
	if len(symbols) == 0 || symbols[0].Name != "addFieldSignals()" ||
		!containsRelatedName(symbols[0].Related, "kindWord") ||
		(!containsRelatedName(symbols[0].Related, "significantWords()") && !containsRelatedName(symbols[0].Related, "significantTokens()")) {
		t.Fatalf("behavioural symbols = %+v", symbols)
	}
}

func containsRelatedName(symbols []RelatedSymbol, expected string) bool {
	for _, symbol := range symbols {
		if symbol.Name == expected {
			return true
		}
	}
	return false
}

func containsRelatedReason(symbols []RelatedSymbol, expected string) bool {
	for _, symbol := range symbols {
		if symbol.Relation == expected {
			return true
		}
	}
	return false
}

func relatedNamed(symbols []RelatedSymbol, expected string) RelatedSymbol {
	for _, symbol := range symbols {
		if symbol.Name == expected {
			return symbol
		}
	}
	return RelatedSymbol{}
}

func containsSymbolName(symbols []Symbol, expected string) bool {
	for _, symbol := range symbols {
		if symbol.Name == expected {
			return true
		}
	}
	return false
}

func containsSymbolReason(reasons []string, expected string) bool {
	for _, reason := range reasons {
		if reason == expected {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
