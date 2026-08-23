package verify

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/config"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

const maxStoredOutput = 128 * 1024

func Run(root string, stdout, stderr io.Writer, now time.Time) (state.Verification, error) {
	workspace, err := state.Load(root)
	if err != nil {
		return state.Verification{}, err
	}
	commands, err := config.VerificationCommands(root)
	if err != nil {
		return state.Verification{}, err
	}
	if len(commands) == 0 {
		return state.Verification{}, fmt.Errorf("no verification is configured; run `spec configure` and add verify commands to config.yml")
	}
	result := state.Verification{Commands: commands, StartedAt: now.UTC()}
	var stored bytes.Buffer
	for _, command := range commands {
		line := "-> " + command + "\n"
		_, _ = io.WriteString(stdout, line)
		stored.WriteString(line)
		cmd := shellCommand(command)
		cmd.Dir = root
		cmd.Env = os.Environ()
		cmd.Stdout = io.MultiWriter(stdout, &stored)
		cmd.Stderr = io.MultiWriter(stderr, &stored)
		if runErr := cmd.Run(); runErr != nil {
			result.FinishedAt = time.Now().UTC()
			result.Output = truncate(stored.String())
			if saveErr := workspace.SaveVerification(result); saveErr != nil {
				return result, fmt.Errorf("%s failed and result could not be saved: %v", command, saveErr)
			}
			return result, fmt.Errorf("%s failed: %w", command, runErr)
		}
	}
	result.Passed = true
	result.FinishedAt = time.Now().UTC()
	result.Output = truncate(stored.String())
	if err := workspace.SaveVerification(result); err != nil {
		return result, err
	}
	return result, nil
}

func shellCommand(command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/C", command)
	}
	return exec.Command("sh", "-c", command)
}

func truncate(value string) string {
	if len(value) <= maxStoredOutput {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(value[:maxStoredOutput]) + "\n[output truncated]"
}
