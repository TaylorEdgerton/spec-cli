package verify

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

type Collected struct {
	Tests       []state.EvidenceTest
	ParserError string
}

type goJSONEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
}

func Collect(command string, output []byte, passed bool) Collected {
	if !isGoJSONCommand(command) {
		return Collected{Tests: []state.EvidenceTest{commandFact(command, passed, "")}}
	}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), maxStoredOutput)
	tests := make([]state.EvidenceTest, 0)
	byID := make(map[string]int)
	line := 0
	for scanner.Scan() {
		line++
		data := bytes.TrimSpace(scanner.Bytes())
		if len(data) == 0 {
			continue
		}
		var event goJSONEvent
		if err := json.Unmarshal(data, &event); err != nil {
			message := fmt.Sprintf("parse go test JSON line %d: %v", line, err)
			return Collected{Tests: []state.EvidenceTest{commandFact(command, false, "parser_error")}, ParserError: message}
		}
		if event.Test == "" {
			continue
		}
		id := event.Package + ":" + event.Test
		index, exists := byID[id]
		if !exists {
			index = len(tests)
			byID[id] = index
			tests = append(tests, state.EvidenceTest{ID: id, Name: event.Test, Status: "unknown"})
		}
		switch event.Action {
		case "pass":
			tests[index].Status = "passed"
		case "fail":
			tests[index].Status = "failed"
		case "skip":
			tests[index].Status = "skipped"
		}
	}
	if err := scanner.Err(); err != nil {
		message := "parse go test JSON: " + err.Error()
		return Collected{Tests: []state.EvidenceTest{commandFact(command, false, "parser_error")}, ParserError: message}
	}
	if len(tests) == 0 {
		return Collected{Tests: []state.EvidenceTest{commandFact(command, passed, "")}}
	}
	return Collected{Tests: tests}
}

func RunWithEvidence(root, phase string, now time.Time) (state.Verification, []state.EvidenceRun, error) {
	if strings.TrimSpace(phase) == "" {
		return state.Verification{}, nil, fmt.Errorf("evidence phase is required")
	}
	return run(root, phase, now, true)
}

func isGoJSONCommand(command string) bool {
	command = strings.ToLower(command)
	return strings.Contains(command, "go test") && strings.Contains(command, "-json")
}

func commandFact(command string, passed bool, status string) state.EvidenceTest {
	if status == "" {
		if passed {
			status = "passed"
		} else {
			status = "failed"
		}
	}
	digest := sha256.Sum256([]byte(command))
	return state.EvidenceTest{ID: "command:" + hex.EncodeToString(digest[:8]), Name: command, Status: status}
}
