package discovery

import (
	"bytes"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/TaylorEdgerton/spec-cli/internal/textfilter"
)

const (
	MaxResults    = 5
	maxFileSize   = 256 * 1024
	maxSearchSize = 128 * 1024 * 1024
	maxFiles      = 4000
)

type Query struct {
	Intent   string
	Outcome  string
	Criteria []string
}

type Result struct {
	Path            string
	Line            int
	Column          int
	Preview         string
	Reasons         []string
	Symbols         []Symbol
	score           float64
	intentScore     float64
	pathIntentScore float64
	pathScore       float64
	intentTerms     int
	strongIntent    bool
	strongMatch     bool
	testFile        bool
	symbolMatches   []symbolMatch
}

type Symbol struct {
	Name    string
	Kind    string
	Line    int
	Column  int
	Reasons []string
	Related []RelatedSymbol
}

type RelatedSymbol struct {
	Name     string
	Kind     string
	Path     string
	Line     int
	Column   int
	Relation string
}

type signalSource uint8

const (
	sourceIntent signalSource = iota
	sourceOutcome
	sourceCriterion
)

type signalKind uint8

const (
	kindWord signalKind = iota
	kindPhrase
	kindIdentifier
)

type signal struct {
	text         string
	exactTerms   []string
	contentTerms []string
	source       signalSource
	kind         signalKind
	weight       float64
}

type match struct {
	signal   int
	line     int
	column   int
	preview  string
	path     bool
	filename bool
	symbol   bool
}

type fileEvidence struct {
	path           string
	matches        []match
	contextMatches []match
}

type scannedFile struct {
	evidence fileEvidence
	found    []bool
	searched bool
}

type lexicalToken struct {
	text  string
	term  string
	start int
}

var (
	identifierPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]{3,}`)
	wordPattern       = regexp.MustCompile(`[\pL\pN]+`)
)

// Find searches repository files and returns up to five confident locations.
// Low-confidence candidates are removed here so every caller receives the
// same set. Intent signals carry more weight than outcome and criterion
// signals, while document frequency reduces the influence of common terms.
func Find(root string, query Query) ([]Result, error) {
	signals := querySignals(query)
	if len(signals) == 0 {
		return nil, nil
	}
	files, err := repositoryFiles(root)
	if err != nil {
		return nil, err
	}
	files = boundedSearchFiles(root, files)
	if len(files) == 0 {
		return nil, nil
	}

	scanned := scanFiles(root, files, signals)
	documentFrequency := make([]int, len(signals))
	searched := 0
	var evidence []fileEvidence
	for _, file := range scanned {
		if !file.searched {
			continue
		}
		searched++
		for index, found := range file.found {
			if found {
				documentFrequency[index]++
			}
		}
		if len(file.evidence.matches) > 0 {
			evidence = append(evidence, file.evidence)
		}
	}

	results := make([]Result, 0, len(evidence))
	for _, file := range evidence {
		if result, ok := rankFile(file, signals, documentFrequency, searched); ok {
			results = append(results, result)
		}
	}
	results = promoteRelatedTests(results)
	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		if results[i].intentScore != results[j].intentScore {
			return results[i].intentScore > results[j].intentScore
		}
		if results[i].pathScore != results[j].pathScore {
			return results[i].pathScore > results[j].pathScore
		}
		if results[i].testFile != results[j].testFile {
			return !results[i].testFile
		}
		return results[i].Path < results[j].Path
	})
	if len(results) > MaxResults {
		results = results[:MaxResults]
	}
	return enrichSymbols(root, results), nil
}

func scanFiles(root string, files []string, signals []signal) []scannedFile {
	workerCount := runtime.GOMAXPROCS(0)
	if workerCount > 8 {
		workerCount = 8
	}
	if workerCount > len(files) {
		workerCount = len(files)
	}
	jobs := make(chan string)
	results := make(chan scannedFile, workerCount)
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for relative := range jobs {
				results <- scanFile(root, relative, signals)
			}
		}()
	}
	go func() {
		for _, relative := range files {
			jobs <- relative
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()

	scanned := make([]scannedFile, 0, len(files))
	for result := range results {
		scanned = append(scanned, result)
	}
	return scanned
}

func scanFile(root, relative string, signals []signal) scannedFile {
	path := filepath.Join(root, filepath.FromSlash(relative))
	data, err := readSearchableFile(path)
	if err != nil {
		return scannedFile{}
	}
	content := string(data)
	if looksGenerated(content) {
		return scannedFile{searched: true, found: make([]bool, len(signals))}
	}
	contentTokens := significantTokens(content)
	pathTokens := significantExactTokens(relative)
	filenameTokens := significantExactTokens(filepath.Base(relative))
	file := scannedFile{
		evidence: fileEvidence{path: relative},
		found:    make([]bool, len(signals)),
		searched: true,
	}
	for index, signal := range signals {
		if _, ok := findExactTerms(pathTokens, signal.exactTerms); ok {
			_, filename := findExactTerms(filenameTokens, signal.exactTerms)
			file.evidence.matches = append(file.evidence.matches, match{
				signal: index, line: 1, column: 1, path: true, filename: filename,
			})
		}
		positions := findContentTermPositions(contentTokens, signal.contentTerms, maxSymbolMatches)
		if len(positions) == 0 {
			continue
		}
		file.found[index] = true
		for occurrence, position := range positions {
			line, column, preview := matchLocation(content, position)
			candidate := match{
				signal: index, line: line, column: column, preview: preview,
				symbol: looksLikeCodeMatch(relative, preview),
			}
			file.evidence.contextMatches = append(file.evidence.contextMatches, candidate)
			if occurrence == 0 {
				file.evidence.matches = append(file.evidence.matches, candidate)
			}
		}
	}
	if len(file.evidence.matches) > 0 && !hasContentMatch(file.evidence.matches) {
		preview := content
		if end := strings.IndexByte(preview, '\n'); end >= 0 {
			preview = preview[:end]
		}
		for index := range file.evidence.matches {
			file.evidence.matches[index].preview = boundedPreview(preview)
		}
	}
	return file
}

func rankFile(file fileEvidence, signals []signal, frequencies []int, documents int) (Result, bool) {
	type scoredMatch struct {
		match
		contribution float64
	}
	var scored []scoredMatch
	for _, evidence := range file.matches {
		signal := signals[evidence.signal]
		specificity := signalSpecificity(signal, frequencies[evidence.signal], documents)
		contribution := signal.weight * specificity
		if evidence.path {
			contribution *= 8
			if evidence.filename {
				contribution *= 2
			}
		} else {
			if documents == 0 || frequencies[evidence.signal] == 0 {
				continue
			}
		}
		if contribution > 0 {
			scored = append(scored, scoredMatch{match: evidence, contribution: contribution})
		}
	}
	if len(scored) == 0 {
		return Result{}, false
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].contribution != scored[j].contribution {
			return scored[i].contribution > scored[j].contribution
		}
		return scored[i].path && !scored[j].path
	})

	result := Result{Path: file.path, Line: 1, Column: 1, testFile: looksLikeTest(file.path)}
	intentTerms := make(map[string]bool)
	for _, evidence := range scored {
		signal := signals[evidence.signal]
		result.score += evidence.contribution
		if evidence.path {
			result.pathScore += evidence.contribution
		}
		if evidence.symbol || signal.kind != kindWord {
			result.strongMatch = true
		}
		if signal.source == sourceIntent {
			result.intentScore += evidence.contribution
			for _, term := range signal.contentTerms {
				intentTerms[term] = true
			}
			if evidence.path {
				result.pathIntentScore += evidence.contribution
			}
			if !evidence.path && signal.kind != kindWord {
				result.strongIntent = true
			}
		}
		if result.Preview == "" && !evidence.path {
			result.Line, result.Column, result.Preview = evidence.line, evidence.column, evidence.preview
		}
		reason := matchReason(signal, evidence.path)
		if reason != "" && !contains(result.Reasons, reason) && len(result.Reasons) < 2 {
			result.Reasons = append(result.Reasons, reason)
		}
	}
	for _, scoredEvidence := range scored {
		if scoredEvidence.path {
			continue
		}
		for _, context := range file.contextMatches {
			if context.signal != scoredEvidence.signal {
				continue
			}
			candidate := symbolMatch{
				Line: context.line, Column: context.column, Text: signals[context.signal].text,
			}
			if !containsSymbolMatch(result.symbolMatches, candidate) {
				result.symbolMatches = append(result.symbolMatches, candidate)
			}
			if len(result.symbolMatches) == maxSymbolMatches {
				break
			}
		}
		if len(result.symbolMatches) == maxSymbolMatches {
			break
		}
	}
	result.intentTerms = len(intentTerms)
	if result.Preview == "" {
		for _, evidence := range scored {
			if evidence.preview != "" {
				result.Preview = evidence.preview
				break
			}
		}
	}
	// A lone lexical hit is usually noise. Paths, phrases, identifiers, or two
	// distinct intent terms provide enough confidence to show the result.
	confident := result.strongMatch || result.intentTerms >= 2 ||
		(result.pathScore > 0 && likelyCodeFile(result.Path))
	return result, confident
}

func containsSymbolMatch(matches []symbolMatch, candidate symbolMatch) bool {
	for _, match := range matches {
		if match.Line == candidate.Line && match.Column == candidate.Column {
			return true
		}
	}
	return false
}

func signalSpecificity(signal signal, frequency, documents int) float64 {
	if documents == 0 {
		return 1
	}
	specificity := math.Log(float64(documents+1)/float64(frequency+1)) + 1
	if signal.kind == kindWord && frequency*2 >= documents {
		specificity *= 0.2
	}
	return specificity
}

func promoteRelatedTests(results []Result) []Result {
	implementations := make([]Result, 0, len(results))
	for _, result := range results {
		if !result.testFile && (result.pathIntentScore > 0 || result.strongIntent || result.intentTerms >= 2) {
			implementations = append(implementations, result)
		}
	}
	for index := range results {
		if !results[index].testFile {
			continue
		}
		implementation, ok := relatedImplementation(results[index].Path, implementations)
		if !ok {
			continue
		}
		floor, ceiling := implementation.score*0.97, implementation.score*0.99
		if results[index].score < floor {
			results[index].score = floor
		}
		if results[index].score > ceiling {
			results[index].score = ceiling
		}
		if len(results[index].Reasons) == 2 {
			results[index].Reasons[1] = "related test file"
		} else if !contains(results[index].Reasons, "related test file") {
			results[index].Reasons = append(results[index].Reasons, "related test file")
		}
	}
	return results
}

func relatedImplementation(testPath string, implementations []Result) (Result, bool) {
	testTerms := baseTerms(testPath)
	var best Result
	for _, implementation := range implementations {
		if !sharesTerm(testTerms, baseTerms(implementation.Path)) {
			continue
		}
		if best.Path == "" || implementation.score > best.score {
			best = implementation
		}
	}
	return best, best.Path != ""
}

func baseTerms(path string) map[string]bool {
	base := filepath.Base(filepath.ToSlash(path))
	terms := make(map[string]bool)
	for _, token := range significantExactTokens(base) {
		if token.text != "test" && token.text != "spec" {
			terms[token.text] = true
		}
	}
	return terms
}

func sharesTerm(left, right map[string]bool) bool {
	for term := range left {
		if right[term] {
			return true
		}
	}
	return false
}

func matchReason(signal signal, path bool) string {
	if path {
		return fmt.Sprintf("path matches %q", signal.text)
	}
	switch signal.source {
	case sourceIntent:
		if signal.kind == kindPhrase || signal.kind == kindIdentifier {
			return fmt.Sprintf("strong intent match to %q", signal.text)
		}
		return fmt.Sprintf("intent matches %q", signal.text)
	case sourceOutcome:
		return fmt.Sprintf("supports expected outcome: %q", signal.text)
	case sourceCriterion:
		return fmt.Sprintf("supports success criterion: %q", signal.text)
	default:
		return ""
	}
}

func querySignals(query Query) []signal {
	byText := make(map[string]signal)
	addFieldSignals(byText, query.Intent, sourceIntent, 8, 18, 22)
	addFieldSignals(byText, query.Outcome, sourceOutcome, 3, 7, 9)
	for _, criterion := range query.Criteria {
		addFieldSignals(byText, criterion, sourceCriterion, 2, 5, 7)
	}
	signals := make([]signal, 0, len(byText))
	for _, signal := range byText {
		signals = append(signals, signal)
	}
	sort.Slice(signals, func(i, j int) bool {
		if signals[i].weight != signals[j].weight {
			return signals[i].weight > signals[j].weight
		}
		return signals[i].text < signals[j].text
	})
	if len(signals) > 24 {
		signals = signals[:24]
	}
	return signals
}

func addFieldSignals(target map[string]signal, value string, source signalSource, wordWeight, phraseWeight, identifierWeight float64) {
	words := significantWords(value)
	for _, word := range words {
		addSignal(target, signal{
			text: word.text, exactTerms: []string{word.text}, contentTerms: []string{word.term},
			source: source, kind: kindWord, weight: wordWeight,
		})
	}
	for index := 0; index+1 < len(words); index++ {
		addSignal(target, signal{
			text:         words[index].text + " " + words[index+1].text,
			exactTerms:   []string{words[index].text, words[index+1].text},
			contentTerms: []string{words[index].term, words[index+1].term},
			source:       source, kind: kindPhrase, weight: phraseWeight,
		})
	}
	for _, identifier := range identifierPattern.FindAllString(value, -1) {
		if strings.Contains(identifier, "_") || containsUpperAfterFirst(identifier) {
			parts := significantTokens(identifier)
			exactTerms := make([]string, 0, len(parts))
			contentTerms := make([]string, 0, len(parts))
			for _, part := range parts {
				exactTerms = append(exactTerms, part.text)
				contentTerms = append(contentTerms, part.term)
			}
			if len(contentTerms) > 0 {
				addSignal(target, signal{
					text: strings.ToLower(identifier), exactTerms: exactTerms, contentTerms: contentTerms,
					source: source, kind: kindIdentifier, weight: identifierWeight,
				})
			}
		}
	}
}

func addSignal(target map[string]signal, candidate signal) {
	key := strings.Join(candidate.exactTerms, " ")
	if current, exists := target[key]; !exists || candidate.weight > current.weight {
		target[key] = candidate
	}
}

func significantWords(value string) []lexicalToken {
	return significantTokens(value)
}

func significantTokens(value string) []lexicalToken {
	tokens := significantExactTokens(value)
	for index := range tokens {
		tokens[index].term = textfilter.NormalizeEnglish(tokens[index].text)
	}
	return tokens
}

func significantExactTokens(value string) []lexicalToken {
	var tokens []lexicalToken
	for _, location := range wordPattern.FindAllStringIndex(value, -1) {
		segment := value[location[0]:location[1]]
		for _, part := range splitIdentifierToken(segment, location[0]) {
			word := strings.ToLower(part.text)
			if utf8.RuneCountInString(word) < 3 || textfilter.IsCommonEnglish(word) {
				continue
			}
			part.text = word
			tokens = append(tokens, part)
		}
	}
	return tokens
}

func splitIdentifierToken(value string, offset int) []lexicalToken {
	var tokens []lexicalToken
	start := 0
	var previous rune
	for index, current := range value {
		if index > 0 && unicode.IsUpper(current) && (unicode.IsLower(previous) || unicode.IsDigit(previous)) {
			tokens = append(tokens, lexicalToken{text: value[start:index], start: offset + start})
			start = index
		}
		previous = current
	}
	return append(tokens, lexicalToken{text: value[start:], start: offset + start})
}

func findContentTerms(tokens []lexicalToken, terms []string) (int, bool) {
	return findTerms(tokens, terms, func(token lexicalToken) string { return token.term })
}

func findContentTermPositions(tokens []lexicalToken, terms []string, limit int) []int {
	if len(terms) == 0 || len(tokens) < len(terms) || limit < 1 {
		return nil
	}
	var positions []int
	for start := 0; start+len(terms) <= len(tokens); start++ {
		matched := true
		for index, term := range terms {
			if tokens[start+index].term != term {
				matched = false
				break
			}
		}
		if matched {
			positions = append(positions, tokens[start].start)
		}
	}
	if len(positions) > limit {
		front := limit / 2
		bounded := append([]int(nil), positions[:front]...)
		bounded = append(bounded, positions[len(positions)-(limit-front):]...)
		return bounded
	}
	return positions
}

func findExactTerms(tokens []lexicalToken, terms []string) (int, bool) {
	return findTerms(tokens, terms, func(token lexicalToken) string { return token.text })
}

func findTerms(tokens []lexicalToken, terms []string, value func(lexicalToken) string) (int, bool) {
	if len(terms) == 0 || len(tokens) < len(terms) {
		return 0, false
	}
	for start := 0; start+len(terms) <= len(tokens); start++ {
		matched := true
		for index, term := range terms {
			if value(tokens[start+index]) != term {
				matched = false
				break
			}
		}
		if matched {
			return tokens[start].start, true
		}
	}
	return 0, false
}

func containsUpperAfterFirst(value string) bool {
	for _, r := range []rune(value)[1:] {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

func matchLocation(content string, position int) (int, int, string) {
	lineStart := strings.LastIndex(content[:position], "\n") + 1
	lineEnd := strings.IndexByte(content[position:], '\n')
	if lineEnd < 0 {
		lineEnd = len(content)
	} else {
		lineEnd += position
	}
	line := bytes.Count([]byte(content[:position]), []byte{'\n'}) + 1
	column := utf8.RuneCountInString(content[lineStart:position]) + 1
	return line, column, boundedPreview(content[lineStart:lineEnd])
}

func boundedPreview(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= 96 {
		return value
	}
	return string(runes[:95]) + "…"
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func hasContentMatch(matches []match) bool {
	for _, match := range matches {
		if !match.path {
			return true
		}
	}
	return false
}

func looksGenerated(content string) bool {
	head := content
	if len(head) > 2048 {
		head = head[:2048]
	}
	lower := strings.ToLower(head)
	return strings.Contains(lower, "code generated") && strings.Contains(lower, "do not edit")
}

func looksLikeCodeMatch(path, preview string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".rst", ".txt", ".adoc":
		return false
	}
	trimmed := strings.TrimSpace(preview)
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") ||
		strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
		return false
	}
	return strings.ContainsAny(trimmed, "(){}=:")
}

func likelyCodeFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".rst", ".txt", ".adoc", ".csv":
		return false
	default:
		return true
	}
}

func looksLikeTest(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := filepath.Base(lower)
	return strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") ||
		strings.HasPrefix(lower, "test/") || strings.HasPrefix(lower, "tests/") ||
		strings.Contains(base, "_test.") || strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.") || strings.HasPrefix(base, "test_")
}

func boundedSearchFiles(root string, files []string) []string {
	bounded := make([]string, 0, len(files))
	total := int64(0)
	for _, relative := range files {
		if isRepositoryMetadata(relative) {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileSize {
			continue
		}
		if total+info.Size() > maxSearchSize {
			continue
		}
		total += info.Size()
		bounded = append(bounded, relative)
	}
	return bounded
}

func isRepositoryMetadata(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	parts := strings.Split(lower, "/")
	for _, part := range parts[:len(parts)-1] {
		switch part {
		case ".github", "vendor", "vendors", "node_modules", "third_party", "third-party", "generated":
			return true
		}
	}
	base := parts[len(parts)-1]
	if base == "license" || strings.HasPrefix(base, "license.") ||
		base == "copying" || strings.HasPrefix(base, "copying.") {
		return true
	}
	return strings.Contains(base, ".generated.") || strings.Contains(base, ".gen.") ||
		strings.Contains(base, "_generated.")
}

func repositoryFiles(root string) ([]string, error) {
	if path, err := exec.LookPath("rg"); err == nil {
		command := exec.Command(path, "--files", "--hidden", "--glob", "!.git/**", "-0")
		command.Dir = root
		if output, searchErr := command.Output(); searchErr == nil {
			return repositoryFileList(output), nil
		}
	}
	command := exec.Command("git", "-C", root, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if output, err := command.Output(); err == nil {
		return repositoryFileList(output), nil
	}
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(files) >= maxFiles {
			return fs.SkipAll
		}
		if entry.IsDir() && (entry.Name() == ".git" || (path != root && strings.HasPrefix(entry.Name(), "."))) {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			relative, relErr := filepath.Rel(root, path)
			if relErr == nil && isRepositoryMetadata(filepath.ToSlash(relative)+"/placeholder") {
				return filepath.SkipDir
			}
		}
		if entry.IsDir() || entry.Name() == ".spec.md" {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr == nil {
			files = append(files, filepath.ToSlash(relative))
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func repositoryFileList(output []byte) []string {
	var files []string
	for _, name := range bytes.Split(output, []byte{0}) {
		if relative := filepath.ToSlash(string(name)); relative != "" && relative != ".spec.md" {
			files = append(files, relative)
			if len(files) == maxFiles {
				break
			}
		}
	}
	sort.Strings(files)
	return files
}

func readSearchableFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Errorf("binary file")
	}
	return data, nil
}
