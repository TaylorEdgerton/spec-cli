package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/TaylorEdgerton/spec-cli/internal/state"
	"gopkg.in/yaml.v3"
)

type Workspace struct {
	Verify []string `yaml:"verify"`
}

type Config struct {
	Verify     []string             `yaml:"verify"`
	Workspaces map[string]Workspace `yaml:"workspaces"`
}

func Load() (Config, error) {
	path, err := state.ConfigPath()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
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
	return detect(root), nil
}

func detect(root string) []string {
	type candidate struct {
		path    string
		command string
	}
	for _, item := range []candidate{
		{"scripts/verify.sh", "scripts/verify.sh"},
		{"Makefile", "make test"},
		{"go.mod", "go test ./..."},
		{"pyproject.toml", "pytest"},
		{"package.json", "npm test"},
		{"gradlew", "./gradlew test"},
	} {
		if info, err := os.Stat(filepath.Join(root, item.path)); err == nil && !info.IsDir() {
			return []string{item.command}
		}
	}
	return nil
}
