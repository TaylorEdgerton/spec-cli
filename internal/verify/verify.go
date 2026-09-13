package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/config"
	"github.com/TaylorEdgerton/spec-cli/internal/gitutil"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

const maxStoredOutput = 128 * 1024

func Run(root string, now time.Time) (state.Verification, error) {
	result, _, err := run(root, "", now, false)
	return result, err
}

func run(root, phase string, now time.Time, persistEvidence bool) (state.Verification, []state.EvidenceRun, error) {
	workspace, err := state.Load(root)
	if err != nil {
		return state.Verification{}, nil, err
	}
	commands, err := config.VerificationCommands(root)
	if err != nil {
		return state.Verification{}, nil, err
	}
	if len(commands) == 0 {
		return state.Verification{}, nil, fmt.Errorf("no verification is configured; run `spec` to configure it")
	}
	result := state.Verification{Commands: commands, StartedAt: now.UTC()}
	runs := make([]state.EvidenceRun, 0, len(commands))
	var output strings.Builder
	for index, command := range commands {
		commandStarted := time.Now().UTC()
		if index == 0 {
			commandStarted = now.UTC()
		}
		output.WriteString("-> ")
		output.WriteString(command)
		output.WriteByte('\n')
		cmd := shellCommand(command)
		cmd.Dir = root
		cmd.Env = os.Environ()
		combined, runErr := cmd.CombinedOutput()
		commandFinished := time.Now().UTC()
		output.Write(combined)
		if len(combined) > 0 && combined[len(combined)-1] != '\n' {
			output.WriteByte('\n')
		}
		if persistEvidence {
			worktreeFingerprint, fingerprintErr := gitutil.WorktreeFingerprint(root)
			if fingerprintErr != nil {
				return result, runs, fingerprintErr
			}
			collected := Collect(command, combined, runErr == nil)
			evidenceRun := state.EvidenceRun{
				SchemaVersion:       state.ArtifactSchemaVersion,
				ID:                  evidenceRunID(workspace.SpecID, phase, command, commandStarted, index),
				Phase:               phase,
				Command:             command,
				Passed:              runErr == nil,
				ParserError:         collected.ParserError,
				BaselineSHA:         workspace.BaseSHA,
				WorktreeFingerprint: worktreeFingerprint,
				StartedAt:           commandStarted,
				FinishedAt:          commandFinished,
				Tests:               collected.Tests,
			}
			if err := workspace.AppendEvidence(evidenceRun); err != nil {
				return result, runs, err
			}
			runs = append(runs, evidenceRun)
		}
		if runErr != nil {
			result.FailedCommand = command
			result.FinishedAt = commandFinished
			result.Output = truncate(output.String())
			result.Fingerprint, _ = Fingerprint(root, commands)
			if saveErr := workspace.SaveVerification(result); saveErr != nil {
				return result, runs, fmt.Errorf("%s failed and result could not be saved: %v", command, saveErr)
			}
			return result, runs, fmt.Errorf("%s failed: %w", command, runErr)
		}
		result.Completed++
	}
	result.Passed = true
	result.FinishedAt = time.Now().UTC()
	result.Output = truncate(output.String())
	result.Fingerprint, err = Fingerprint(root, commands)
	if err != nil {
		return result, runs, err
	}
	if err := workspace.SaveVerification(result); err != nil {
		return result, runs, err
	}
	return result, runs, nil
}

func evidenceRunID(specID, phase, command string, started time.Time, index int) string {
	hash := sha256.Sum256([]byte(specID + "\x00" + phase + "\x00" + command))
	return fmt.Sprintf("%d-%d-%s", started.UnixNano(), index, hex.EncodeToString(hash[:6]))
}

func Current(root string, result *state.Verification) (bool, error) {
	if result == nil || !result.Passed || result.Fingerprint == "" {
		return false, nil
	}
	commands, err := config.VerificationCommands(root)
	if err != nil {
		return false, err
	}
	current, err := Fingerprint(root, commands)
	if err != nil {
		return false, err
	}
	return current == result.Fingerprint, nil
}

func Fingerprint(root string, commands []string) (string, error) {
	workspace, err := gitutil.WorktreeFingerprint(root)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(workspace))
	for _, command := range commands {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(command))
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func shellCommand(command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/C", command)
	}
	return exec.Command("sh", "-c", command)
}

func truncate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxStoredOutput {
		return value
	}
	return strings.TrimSpace(value[:maxStoredOutput]) + "\n[output truncated]"
}
