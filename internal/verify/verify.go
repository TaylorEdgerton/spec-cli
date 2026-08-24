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
	workspace, err := state.Load(root)
	if err != nil {
		return state.Verification{}, err
	}
	commands, err := config.VerificationCommands(root)
	if err != nil {
		return state.Verification{}, err
	}
	if len(commands) == 0 {
		return state.Verification{}, fmt.Errorf("no verification is configured; run `spec` to configure it")
	}
	result := state.Verification{Commands: commands, StartedAt: now.UTC()}
	var output strings.Builder
	for _, command := range commands {
		output.WriteString("-> ")
		output.WriteString(command)
		output.WriteByte('\n')
		cmd := shellCommand(command)
		cmd.Dir = root
		cmd.Env = os.Environ()
		combined, runErr := cmd.CombinedOutput()
		output.Write(combined)
		if len(combined) > 0 && combined[len(combined)-1] != '\n' {
			output.WriteByte('\n')
		}
		if runErr != nil {
			result.FailedCommand = command
			result.FinishedAt = time.Now().UTC()
			result.Output = truncate(output.String())
			result.Fingerprint, _ = Fingerprint(root, commands)
			if saveErr := workspace.SaveVerification(result); saveErr != nil {
				return result, fmt.Errorf("%s failed and result could not be saved: %v", command, saveErr)
			}
			return result, fmt.Errorf("%s failed: %w", command, runErr)
		}
		result.Completed++
	}
	result.Passed = true
	result.FinishedAt = time.Now().UTC()
	result.Output = truncate(output.String())
	result.Fingerprint, err = Fingerprint(root, commands)
	if err != nil {
		return result, err
	}
	if err := workspace.SaveVerification(result); err != nil {
		return result, err
	}
	return result, nil
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
