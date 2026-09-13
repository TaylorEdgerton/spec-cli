package discovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	scip "github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

const (
	packageARun = "scip-go gomod example.com/repo v1 packageA/Run()."
	packageBRun = "scip-go gomod example.com/repo v1 packageB/Run()."
	mainStart   = "scip-go gomod example.com/repo v1 main/start()."
	testRun     = "scip-go gomod example.com/repo v1 main/TestRun()."
	runnerType  = "scip-go gomod example.com/repo v1 service/Runner#"
	runnerImpl  = "scip-go gomod example.com/repo v1 service/worker#"
)

func TestSCIPUsesSemanticIdentityForDefinitionsAndReferences(t *testing.T) {
	root := writeSCIPFixture(t)

	symbol, err := Explore(root, "packageA/run.go", 3, 6)
	if err != nil {
		t.Fatal(err)
	}
	if symbol.Name != "Run()" || symbol.Capability != CapabilityPrecise {
		t.Fatalf("root = %+v", symbol)
	}
	start := relatedWithRelation(symbol.Related, "referenced by start()")
	if start.Path != "cmd/main.go" || start.Line != 3 {
		t.Fatalf("packageA reference owner = %+v; all = %+v", start, symbol.Related)
	}
	if related := relatedNamed(symbol.Related, "callOther()"); related.Name != "" {
		t.Fatalf("packageB.Run was returned as a packageA.Run reference: %+v", symbol.Related)
	}
	test := relatedNamed(symbol.Related, "TestRun()")
	if test.Path != "cmd/main_test.go" || test.Kind != "test" {
		t.Fatalf("test reference = %+v; all = %+v", test, symbol.Related)
	}

	fromReference, err := Explore(root, "cmd/main.go", 4, 14)
	if err != nil {
		t.Fatal(err)
	}
	definition := relatedWithRelation(fromReference.Related, "defined as Run()")
	if definition.Path != "packageA/run.go" || definition.Line != 3 || definition.Column != 6 {
		t.Fatalf("definition = %+v; all = %+v", definition, fromReference.Related)
	}
}

func TestFindPrefersExactSCIPDefinitionOverEnclosingCallers(t *testing.T) {
	root := t.TempDir()
	writeRepositoryFile(t, root, "context.go", `package main

func newContextExplorer() {}

func runContextExplorer() {
	newContextExplorer()
}

func exploreDiscoveryContext() {
	newContextExplorer()
}
`)
	rootID := "scip-go gomod example.com/repo v1 main/newContextExplorer()."
	runID := "scip-go gomod example.com/repo v1 main/runContextExplorer()."
	exploreID := "scip-go gomod example.com/repo v1 main/exploreDiscoveryContext()."
	definition := int32(scip.SymbolRole_Definition)
	writeSCIPIndex(t, root, &scip.Index{Documents: []*scip.Document{scipDocument("context.go", []*scip.Occurrence{
		{Range: []int32{2, 5, 23}, EnclosingRange: []int32{2, 0, 2, 28}, Symbol: rootID, SymbolRoles: definition},
		{Range: []int32{4, 5, 23}, EnclosingRange: []int32{4, 0, 6, 1}, Symbol: runID, SymbolRoles: definition},
		{Range: []int32{5, 1, 19}, EnclosingRange: []int32{5, 1, 21}, Symbol: rootID},
		{Range: []int32{8, 5, 28}, EnclosingRange: []int32{8, 0, 10, 1}, Symbol: exploreID, SymbolRoles: definition},
		{Range: []int32{9, 1, 19}, EnclosingRange: []int32{9, 1, 21}, Symbol: rootID},
	},
		scipInfo(rootID, "newContextExplorer", scip.SymbolInformation_Function),
		scipInfo(runID, "runContextExplorer", scip.SymbolInformation_Function),
		scipInfo(exploreID, "exploreDiscoveryContext", scip.SymbolInformation_Function),
	)}})

	results, err := Find(root, Query{Intent: "newContextExplorer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || len(results[0].Symbols) == 0 {
		t.Fatalf("results = %+v", results)
	}
	rootSymbol := results[0].Symbols[0]
	if rootSymbol.Name != "newContextExplorer()" || rootSymbol.Line != 3 || rootSymbol.Capability != CapabilityPrecise {
		t.Fatalf("initial root = %+v", rootSymbol)
	}
	for _, name := range []string{"runContextExplorer()", "exploreDiscoveryContext()"} {
		related := relatedNamed(rootSymbol.Related, name)
		if related.Name == "" || !strings.HasPrefix(related.Relation, "called by ") {
			t.Fatalf("caller %s = %+v; relationships = %+v", name, related, rootSymbol.Related)
		}
	}
	if countRelatedLocation(rootSymbol.Related, "context.go", 5) != 1 || countRelatedLocation(rootSymbol.Related, "context.go", 9) != 1 {
		t.Fatalf("call/reference locations were not deduplicated: %+v", rootSymbol.Related)
	}
}

func TestSCIPExposesImplementationsThroughGenericRelationships(t *testing.T) {
	root := writeSCIPFixture(t)
	symbol, err := Explore(root, "service/runner.go", 3, 6)
	if err != nil {
		t.Fatal(err)
	}
	implementation := relatedWithRelation(symbol.Related, "implemented by worker")
	if implementation.Path != "service/worker.go" || implementation.Line != 3 || implementation.Capability != CapabilityPrecise {
		t.Fatalf("implementation = %+v; all = %+v", implementation, symbol.Related)
	}
}

func TestSCIPEnrichesSearchResultsAndConvertsUTF16Columns(t *testing.T) {
	root := t.TempDir()
	writeRepositoryFile(t, root, "app.ts", "const 🚀 = run();\n")
	symbol := "scip-typescript npm example 1 app/run()."
	index := &scip.Index{Documents: []*scip.Document{{
		RelativePath: "app.ts", PositionEncoding: scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart,
		Occurrences: []*scip.Occurrence{{Range: []int32{0, 11, 14}, Symbol: symbol, SymbolRoles: int32(scip.SymbolRole_Definition)}},
		Symbols:     []*scip.SymbolInformation{{Symbol: symbol, DisplayName: "run", Kind: scip.SymbolInformation_Function}},
	}}}
	writeSCIPIndex(t, root, index)

	results := enrichCodeContext(root, []Result{{
		Path: "app.ts", Line: 1, Column: 11,
		Symbols: []Symbol{{Name: "run()", Kind: "function", Line: 1, Column: 11, Capability: CapabilityStructural}},
	}})
	if len(results[0].Symbols) != 1 || results[0].Symbols[0].Capability != CapabilityPrecise || results[0].Symbols[0].Column != 11 {
		t.Fatalf("enriched results = %+v", results)
	}
}

func TestMissingOrInvalidSCIPIndexFallsBackToStructuralDiscovery(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "invalid"}[invalid], func(t *testing.T) {
			root := t.TempDir()
			writeRepositoryFile(t, root, "main.go", "package main\n\nfunc Run() {}\n")
			if invalid {
				if err := os.WriteFile(filepath.Join(root, "index.scip"), []byte("not protobuf"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			symbol, err := Explore(root, "main.go", 3, 6)
			if err != nil {
				t.Fatal(err)
			}
			if symbol.Name != "Run()" || symbol.Capability != CapabilityStructural {
				t.Fatalf("fallback symbol = %+v", symbol)
			}
		})
	}
}

func TestValidSCIPIndexFallsBackPerUnindexedFile(t *testing.T) {
	root := t.TempDir()
	writeRepositoryFile(t, root, "indexed.go", "package main\n\nfunc Indexed() {}\n")
	writeRepositoryFile(t, root, "service.py", "def run_service():\n    return True\n")
	symbolID := "scip-go gomod example.com/repo v1 main/Indexed()."
	writeSCIPIndex(t, root, &scip.Index{Documents: []*scip.Document{{
		RelativePath: "indexed.go", PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
		Occurrences: []*scip.Occurrence{{Range: []int32{2, 5, 12}, Symbol: symbolID, SymbolRoles: int32(scip.SymbolRole_Definition)}},
		Symbols:     []*scip.SymbolInformation{scipInfo(symbolID, "Indexed", scip.SymbolInformation_Function)},
	}}})

	results := enrichCodeContext(root, []Result{{
		Path: "service.py", Line: 1, Column: 5,
		symbolMatches: []symbolMatch{{Line: 1, Column: 5, Text: "run_service", Kind: kindIdentifier}},
	}})
	if len(results) != 1 || len(results[0].Symbols) != 1 || results[0].Symbols[0].Name != "run_service()" ||
		results[0].Symbols[0].Capability != CapabilityStructural {
		t.Fatalf("unindexed-file fallback = %+v", results)
	}
}

func TestStaleSCIPIndexFallsBackWithoutUsingShiftedLocations(t *testing.T) {
	root := t.TempDir()
	writeRepositoryFile(t, root, "main.go", "package main\n\nfunc Run() {}\n")
	symbolID := "scip-go gomod example.com/repo v1 main/Run()."
	writeSCIPIndex(t, root, &scip.Index{Documents: []*scip.Document{{
		RelativePath: "main.go", PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
		Occurrences: []*scip.Occurrence{{Range: []int32{2, 5, 8}, Symbol: symbolID, SymbolRoles: int32(scip.SymbolRole_Definition)}},
		Symbols:     []*scip.SymbolInformation{scipInfo(symbolID, "Run", scip.SymbolInformation_Function)},
	}}})
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(root, "main.go"), future, future); err != nil {
		t.Fatal(err)
	}

	if availability := SCIPAvailability(root); availability != PrecisionStale {
		t.Fatalf("availability = %q", availability)
	}
	symbol, err := Explore(root, "main.go", 3, 6)
	if err != nil {
		t.Fatal(err)
	}
	if symbol.Name != "Run()" || symbol.Capability != CapabilityStructural {
		t.Fatalf("stale-index fallback = %+v", symbol)
	}
}

func TestSCIPWorksWithoutALanguageSpecificStructuralProvider(t *testing.T) {
	root := t.TempDir()
	writeRepositoryFile(t, root, "service.rb", "def perform\nend\n")
	symbolID := "scip-ruby gem example 1 service/perform()."
	writeSCIPIndex(t, root, &scip.Index{Documents: []*scip.Document{{
		RelativePath: "service.rb", PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
		Occurrences: []*scip.Occurrence{{Range: []int32{0, 4, 11}, Symbol: symbolID, SymbolRoles: int32(scip.SymbolRole_Definition)}},
		Symbols:     []*scip.SymbolInformation{scipInfo(symbolID, "perform", scip.SymbolInformation_Method)},
	}}})

	symbol, err := Explore(root, "service.rb", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if symbol.Name != "perform()" || symbol.Capability != CapabilityPrecise {
		t.Fatalf("language-agnostic SCIP symbol = %+v", symbol)
	}
}

func TestSCIPSkipsIndexedDependenciesOutsideRepository(t *testing.T) {
	root := t.TempDir()
	writeRepositoryFile(t, root, "main.go", "package main\n\nfunc Run() {}\n")
	symbolID := "scip-go gomod example.com/repo v1 main/Run()."
	writeSCIPIndex(t, root, &scip.Index{Documents: []*scip.Document{
		{RelativePath: "../../.cache/go-build/dependency.go", Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 3}, Symbol: "external"}}},
		{
			RelativePath: "main.go", PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
			Occurrences: []*scip.Occurrence{{Range: []int32{2, 5, 8}, Symbol: symbolID, SymbolRoles: int32(scip.SymbolRole_Definition)}},
			Symbols:     []*scip.SymbolInformation{scipInfo(symbolID, "Run", scip.SymbolInformation_Function)},
		},
	}})

	if availability := SCIPAvailability(root); availability != PrecisionAvailable {
		t.Fatalf("availability = %q", availability)
	}
	symbol, err := Explore(root, "main.go", 3, 6)
	if err != nil || symbol.Capability != CapabilityPrecise {
		t.Fatalf("repository symbol = %+v error = %v", symbol, err)
	}
}

func writeSCIPFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeRepositoryFile(t, root, "packageA/run.go", "package packageA\n\nfunc Run() {}\n")
	writeRepositoryFile(t, root, "packageB/run.go", "package packageB\n\nfunc Run() {}\n")
	writeRepositoryFile(t, root, "cmd/main.go", "package main\n\nfunc start() {\n    packageA.Run()\n}\n\nfunc callOther() {\n    packageB.Run()\n}\n")
	writeRepositoryFile(t, root, "cmd/main_test.go", "package main\n\nfunc TestRun() {\n    packageA.Run()\n}\n")
	writeRepositoryFile(t, root, "service/runner.go", "package service\n\ntype Runner interface{}\n")
	writeRepositoryFile(t, root, "service/worker.go", "package service\n\ntype worker struct{}\n")

	definition := int32(scip.SymbolRole_Definition)
	testDefinition := definition | int32(scip.SymbolRole_Test)
	index := &scip.Index{Documents: []*scip.Document{
		scipDocument("packageA/run.go", []*scip.Occurrence{
			{Range: []int32{2, 5, 8}, EnclosingRange: []int32{2, 0, 2, 13}, Symbol: packageARun, SymbolRoles: definition},
		}, scipInfo(packageARun, "Run", scip.SymbolInformation_Function)),
		scipDocument("packageB/run.go", []*scip.Occurrence{
			{Range: []int32{2, 5, 8}, EnclosingRange: []int32{2, 0, 2, 13}, Symbol: packageBRun, SymbolRoles: definition},
		}, scipInfo(packageBRun, "Run", scip.SymbolInformation_Function)),
		scipDocument("cmd/main.go", []*scip.Occurrence{
			{Range: []int32{2, 5, 10}, EnclosingRange: []int32{2, 0, 4, 1}, Symbol: mainStart, SymbolRoles: definition},
			{Range: []int32{3, 13, 16}, EnclosingRange: []int32{3, 4, 18}, Symbol: packageARun},
			{Range: []int32{6, 5, 14}, EnclosingRange: []int32{6, 0, 8, 1}, Symbol: "scip-go gomod example.com/repo v1 main/callOther().", SymbolRoles: definition},
			{Range: []int32{7, 13, 16}, EnclosingRange: []int32{7, 4, 18}, Symbol: packageBRun},
		},
			scipInfo(mainStart, "start", scip.SymbolInformation_Function),
			scipInfo("scip-go gomod example.com/repo v1 main/callOther().", "callOther", scip.SymbolInformation_Function),
		),
		scipDocument("cmd/main_test.go", []*scip.Occurrence{
			{Range: []int32{2, 5, 12}, EnclosingRange: []int32{2, 0, 4, 1}, Symbol: testRun, SymbolRoles: testDefinition},
			{Range: []int32{3, 13, 16}, EnclosingRange: []int32{3, 4, 18}, Symbol: packageARun, SymbolRoles: int32(scip.SymbolRole_Test)},
		}, scipInfo(testRun, "TestRun", scip.SymbolInformation_Function)),
		scipDocument("service/runner.go", []*scip.Occurrence{
			{Range: []int32{2, 5, 11}, EnclosingRange: []int32{2, 0, 2, 23}, Symbol: runnerType, SymbolRoles: definition},
		}, scipInfo(runnerType, "Runner", scip.SymbolInformation_Interface)),
		scipDocument("service/worker.go", []*scip.Occurrence{
			{Range: []int32{2, 5, 11}, EnclosingRange: []int32{2, 0, 2, 20}, Symbol: runnerImpl, SymbolRoles: definition},
		}, &scip.SymbolInformation{
			Symbol: runnerImpl, DisplayName: "worker", Kind: scip.SymbolInformation_Struct,
			Relationships: []*scip.Relationship{{Symbol: runnerType, IsImplementation: true}},
		}),
	}}
	writeSCIPIndex(t, root, index)
	return root
}

func scipDocument(path string, occurrences []*scip.Occurrence, symbols ...*scip.SymbolInformation) *scip.Document {
	return &scip.Document{
		RelativePath: path, PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
		Occurrences: occurrences, Symbols: symbols,
	}
}

func scipInfo(symbol, name string, kind scip.SymbolInformation_Kind) *scip.SymbolInformation {
	return &scip.SymbolInformation{Symbol: symbol, DisplayName: name, Kind: kind}
}

func writeSCIPIndex(t *testing.T, root string, index *scip.Index) {
	t.Helper()
	data, err := proto.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.scip"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeRepositoryFile(t *testing.T, root, path, source string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func relatedWithRelation(related []RelatedSymbol, relation string) RelatedSymbol {
	for _, item := range related {
		if strings.EqualFold(item.Relation, relation) {
			return item
		}
	}
	return RelatedSymbol{}
}

func countRelatedLocation(related []RelatedSymbol, path string, line int) int {
	count := 0
	for _, item := range related {
		if item.Path == path && item.Line == line {
			count++
		}
	}
	return count
}
