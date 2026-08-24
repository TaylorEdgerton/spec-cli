package config

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/TaylorEdgerton/spec-cli/internal/state"
	"gopkg.in/yaml.v3"
)

type TemplateName string

const (
	README  TemplateName = "readme.md"
	ADR     TemplateName = "adr.md"
	Runbook TemplateName = "runbook.md"
)

//go:embed defaults/config.yml defaults/templates/*.md
var defaultFiles embed.FS

type Workspace struct {
	Verify []string `yaml:"verify"`
}

type Config struct {
	Verify     []string             `yaml:"verify"`
	Workspaces map[string]Workspace `yaml:"workspaces"`
}

type VerificationOption struct {
	Root     string
	Commands []string
}

func Directory() (string, error) {
	path, err := state.ConfigPath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}

func InstallDefaults() (bool, error) {
	if err := state.EnsureUserDirs(); err != nil {
		return false, err
	}
	directory, err := Directory()
	if err != nil {
		return false, err
	}
	files := []struct {
		source      string
		destination string
	}{
		{"defaults/config.yml", filepath.Join(directory, "config.yml")},
		{"defaults/templates/readme.md", filepath.Join(directory, "templates", string(README))},
		{"defaults/templates/adr.md", filepath.Join(directory, "templates", string(ADR))},
		{"defaults/templates/runbook.md", filepath.Join(directory, "templates", string(Runbook))},
	}
	created := false
	for _, item := range files {
		content, err := defaultFiles.ReadFile(item.source)
		if err != nil {
			return created, err
		}
		file, err := os.OpenFile(item.destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return created, err
		}
		if _, err := file.Write(content); err != nil {
			_ = file.Close()
			_ = os.Remove(item.destination)
			return created, err
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(item.destination)
			return created, err
		}
		created = true
	}
	return created, nil
}

func Template(name TemplateName) ([]byte, error) {
	directory, err := Directory()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "templates", string(name))
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("template is missing: %s; run `spec init` to install missing defaults", path)
	}
	if err != nil {
		return nil, fmt.Errorf("read template %s: %w", path, err)
	}
	return content, nil
}

func Load() (Config, error) {
	path, err := state.ConfigPath()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("configuration file is missing: %s; run `spec init` to install missing defaults", path)
	}
	if err != nil {
		return Config{}, err
	}
	var result Config
	if err := yaml.Unmarshal(data, &result); err != nil {
		return Config{}, err
	}
	return result, nil
}

func VerificationCommands(root string) ([]string, error) {
	if workspace, err := state.Load(root); err == nil && len(workspace.VerifyCommands) > 0 {
		return append([]string(nil), workspace.VerifyCommands...), nil
	}
	loaded, err := Load()
	if err != nil {
		return nil, err
	}
	if workspace, ok := loaded.Workspaces[root]; ok && len(workspace.Verify) > 0 {
		return workspace.Verify, nil
	}
	if len(loaded.Verify) > 0 {
		return loaded.Verify, nil
	}
	return nil, nil
}

func ReusableVerification(currentRoot string) ([]VerificationOption, error) {
	loaded, err := Load()
	if err != nil {
		return nil, err
	}
	byRoot := make(map[string][]string)
	for root, workspace := range loaded.Workspaces {
		if root != currentRoot && len(workspace.Verify) > 0 {
			byRoot[root] = append([]string(nil), workspace.Verify...)
		}
	}
	workspaces, err := state.Workspaces()
	if err != nil {
		return nil, err
	}
	for _, workspace := range workspaces {
		if workspace.Root != currentRoot && len(workspace.VerifyCommands) > 0 {
			byRoot[workspace.Root] = append([]string(nil), workspace.VerifyCommands...)
		}
	}
	roots := make([]string, 0, len(byRoot))
	for root := range byRoot {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	options := make([]VerificationOption, 0, len(roots))
	for _, root := range roots {
		options = append(options, VerificationOption{Root: root, Commands: byRoot[root]})
	}
	return options, nil
}
