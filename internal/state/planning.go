package state

import (
	"fmt"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
)

// PlanSubmission retains a complete plan at its submission boundary. Keeping
// full snapshots makes removals as well as additions unambiguous.
type PlanSubmission struct {
	Plan         ChangePlan `json:"plan"`
	Source       PlanSource `json:"source"`
	Submitter    string     `json:"submitter,omitempty"`
	SubmittedAt  time.Time  `json:"submitted_at"`
	AfterChanges bool       `json:"after_changes,omitempty"`
}

func (plan StoredChangePlan) Submission() PlanSubmission {
	return PlanSubmission{Plan: plan.Plan, Source: plan.Source, Submitter: plan.Submitter, SubmittedAt: plan.SubmittedAt, AfterChanges: plan.AfterChanges}
}

// PlanNeedsAmendment performs an explicit read at the planning boundary. A
// previously recorded amendment stays an amendment even if code is reverted.
func (workspace Workspace) PlanNeedsAmendment() (bool, error) {
	plan, err := workspace.Plan()
	if err != nil {
		return false, err
	}
	if plan != nil && (plan.AfterChanges || len(plan.Amendments) > 0) {
		return true, nil
	}
	if workspace.StartingFingerprint != "" {
		current, err := gitutil.StartingFingerprint(workspace.Root)
		if err != nil {
			return false, fmt.Errorf("check starting state: %w", err)
		}
		return current != workspace.StartingFingerprint, nil
	}
	// Legacy clean records can use the recorded commit. Dirty legacy records
	// lack enough information to prove that replacing a plan is safe.
	if workspace.GitState == "dirty" {
		return true, nil
	}
	changes, err := gitutil.Changes(workspace.Root, workspace.BaseSHA)
	if err != nil {
		return false, fmt.Errorf("starting state unavailable: %w", err)
	}
	for _, changed := range changes {
		if changed.Path != gitutil.ActiveSpecPattern {
			return true, nil
		}
	}
	return false, nil
}

func (workspace *Workspace) CaptureStartingState() error {
	fingerprint, err := gitutil.StartingFingerprint(workspace.Root)
	if err != nil {
		return err
	}
	workspace.StartingFingerprint = fingerprint
	return workspace.saveMetadata()
}
