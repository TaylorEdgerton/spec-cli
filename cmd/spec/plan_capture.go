package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

type planDecision string

const (
	planAccept planDecision = "accept"
	planEdit   planDecision = "edit"
	planSkip   planDecision = "skip"
)

var planFencePattern = regexp.MustCompile("(?s)```spec-plan[ \\t]*\\r?\\n(.*?)\\r?\\n```")

func extractPlanBlock(input string) ([]byte, error) {
	matches := planFencePattern.FindAllStringSubmatch(input, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no fenced spec-plan block found")
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("multiple fenced spec-plan blocks found")
	}
	return []byte(strings.TrimSpace(matches[0][1])), nil
}
func validateChangePlan(data []byte) (state.ChangePlan, error) {
	var p state.ChangePlan
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return p, fmt.Errorf("parse ChangePlan: %w", err)
	}
	if decoder.More() {
		return p, fmt.Errorf("parse ChangePlan: trailing JSON")
	}
	p.Summary = strings.TrimSpace(p.Summary)
	if p.Summary == "" {
		return p, fmt.Errorf("ChangePlan summary is required")
	}
	seen := map[string]bool{}
	for i := range p.Files {
		f := &p.Files[i]
		f.Path = strings.TrimSpace(strings.ReplaceAll(f.Path, "\\", "/"))
		if f.Path == "" || filepath.IsAbs(f.Path) || strings.Contains(strings.Split(f.Path, "/")[0], ":") {
			return p, fmt.Errorf("file path must be repository-relative: %q", f.Path)
		}
		f.Path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(f.Path)))
		if f.Path == "." || f.Path == ".." || strings.HasPrefix(f.Path, "../") {
			return p, fmt.Errorf("file path escapes repository: %q", f.Path)
		}
		if seen[f.Path] {
			return p, fmt.Errorf("duplicate planned file: %s", f.Path)
		}
		seen[f.Path] = true
		switch f.Action {
		case state.PlanFileCreate, state.PlanFileModify, state.PlanFileDelete:
		default:
			return p, fmt.Errorf("invalid file action %q", f.Action)
		}
		f.Reason = strings.TrimSpace(f.Reason)
	}
	for i := range p.IntegrationPoints {
		p.IntegrationPoints[i].ExistingSymbol = strings.TrimSpace(p.IntegrationPoints[i].ExistingSymbol)
		p.IntegrationPoints[i].PlannedChange = strings.TrimSpace(p.IntegrationPoints[i].PlannedChange)
		p.IntegrationPoints[i].Relationship = strings.TrimSpace(p.IntegrationPoints[i].Relationship)
	}
	for i := range p.Verification {
		p.Verification[i].Behaviour = strings.TrimSpace(p.Verification[i].Behaviour)
		p.Verification[i].LikelyLocation = strings.TrimSpace(p.Verification[i].LikelyLocation)
	}
	for i := range p.Uncertainties {
		p.Uncertainties[i] = strings.TrimSpace(p.Uncertainties[i])
	}
	return p, nil
}
func canonicalPlanBytes(plan state.ChangePlan) ([]byte, error) { return json.Marshal(plan) }
func planPreview(p state.ChangePlan) string {
	return fmt.Sprintf("%s\n%d file(s) · %d integration(s) · %d uncertainty(ies)\n%s", p.Summary, len(p.Files), len(p.IntegrationPoints), len(p.Uncertainties), previewFiles(p.Files))
}
func previewFiles(files []state.PlannedFile) string {
	rows := make([]string, 0, len(files))
	for _, f := range files {
		rows = append(rows, fmt.Sprintf("%s  %s", f.Action, f.Path))
	}
	return strings.Join(rows, "\n")
}
func saveAcceptedPlan(root string, plan state.ChangePlan, source state.PlanSource, submitter string, now time.Time) (*state.StoredChangePlan, error) {
	canonical, err := canonicalPlanBytes(plan)
	if err != nil {
		return nil, err
	}
	plan, err = validateChangePlan(canonical)
	if err != nil {
		return nil, err
	}
	workspace, err := state.Load(root)
	if err != nil {
		return nil, err
	}
	if !workspace.Active {
		return nil, fmt.Errorf("no active Spec")
	}
	stored := state.StoredChangePlan{SchemaVersion: state.ArtifactSchemaVersion, Source: source, Submitter: strings.TrimSpace(submitter), SubmittedAt: now.UTC(), AcceptedAt: now.UTC(), Plan: plan}
	if err := workspace.SavePlan(stored); err != nil {
		return nil, err
	}
	return &stored, nil
}

func capturePlanDecision(root, raw string, decision planDecision, submitter string, now time.Time) (state.ChangePlan, *state.StoredChangePlan, error) {
	if decision == planSkip {
		return state.ChangePlan{}, nil, nil
	}
	block, err := extractPlanBlock(raw)
	if err != nil {
		return state.ChangePlan{}, nil, err
	}
	plan, err := validateChangePlan(block)
	if err != nil {
		return state.ChangePlan{}, nil, err
	}
	if decision != planAccept {
		return plan, nil, nil
	}
	stored, err := saveAcceptedPlan(root, plan, state.PlanSourcePaste, submitter, now)
	return plan, stored, err
}

func runPlanSubmit(root string, input io.Reader, output io.Writer, now time.Time) error {
	data, err := io.ReadAll(input)
	if err != nil {
		return err
	}
	plan, err := validateChangePlan(data)
	if err != nil {
		return err
	}
	stored, err := saveAcceptedPlan(root, plan, state.PlanSourceCLI, "cli", now)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "Accepted ChangePlan v%d: %s\n", stored.SchemaVersion, stored.Plan.Summary)
	return nil
}
