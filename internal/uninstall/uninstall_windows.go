//go:build windows

package uninstall

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

func pathDescription(executable, _ string) string {
	return fmt.Sprintf("installer-managed PATH entry for %s (if present)", filepath.Dir(executable))
}

func completionMessage() string {
	return "Spec uninstall is scheduled and will finish after this command exits. Restart your shell to refresh PATH."
}

func removeInstallation(executable, _ string) error {
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		powershell, err = exec.LookPath("pwsh.exe")
		if err != nil {
			return fmt.Errorf("PowerShell is required to finish uninstalling on Windows")
		}
	}
	installDir := filepath.Dir(executable)
	marker := filepath.Join(installDir, ".spec-path-added")
	script, err := os.CreateTemp("", "spec-uninstall-*.ps1")
	if err != nil {
		return err
	}
	scriptPath := script.Name()
	cleanup := true
	defer func() {
		_ = script.Close()
		if cleanup {
			_ = os.Remove(scriptPath)
		}
	}()
	if _, err := script.WriteString(windowsCleanupScript); err != nil {
		return err
	}
	if err := script.Close(); err != nil {
		return err
	}
	command := exec.Command(
		powershell,
		"-NoProfile",
		"-ExecutionPolicy", "Bypass",
		"-File", scriptPath,
		strconv.Itoa(os.Getpid()),
		executable,
		installDir,
		marker,
		scriptPath,
	)
	if err := command.Start(); err != nil {
		return fmt.Errorf("start deferred uninstall: %w", err)
	}
	cleanup = false
	return nil
}

const windowsCleanupScript = `param(
    [int]$SpecPid,
    [string]$Binary,
    [string]$InstallDir,
    [string]$Marker,
    [string]$ScriptPath
)

Wait-Process -Id $SpecPid -ErrorAction SilentlyContinue

if (Test-Path -LiteralPath $Marker) {
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $entries = @($userPath -split ";" | Where-Object { $_ })
    $updated = @($entries | Where-Object {
        $_.TrimEnd("\\") -ine $InstallDir.TrimEnd("\\")
    }) -join ";"
    [Environment]::SetEnvironmentVariable("Path", $updated, "User")
}

Remove-Item -LiteralPath $Binary -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath $Marker -Force -ErrorAction SilentlyContinue
if ((Test-Path -LiteralPath $InstallDir) -and
    @(Get-ChildItem -LiteralPath $InstallDir -Force).Count -eq 0) {
    Remove-Item -LiteralPath $InstallDir -Force -ErrorAction SilentlyContinue
}
Remove-Item -LiteralPath $ScriptPath -Force -ErrorAction SilentlyContinue
`
