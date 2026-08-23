package state

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/aiusage"
)

type SandboxSession struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Agent     string    `json:"agent"`
	StartedAt time.Time `json:"started_at"`
}

type Metadata struct {
	Root           string          `json:"root"`
	ID             string          `json:"id"`
	Active         bool            `json:"active"`
	Title          string          `json:"title,omitempty"`
	StartedAt      time.Time       `json:"started_at,omitempty"`
	BaseSHA        string          `json:"base_sha,omitempty"`
	SandboxSession *SandboxSession `json:"sandbox_session,omitempty"`
}

type Verification struct {
	Commands   []string  `json:"commands"`
	Passed     bool      `json:"passed"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Output     string    `json:"output,omitempty"`
}

type History struct {
	Title        string           `json:"title"`
	StartedAt    time.Time        `json:"started_at"`
	BaseSHA      string           `json:"base_sha"`
	FinishedAt   time.Time        `json:"finished_at"`
	EndSHA       string           `json:"end_sha,omitempty"`
	ChangedFiles []string         `json:"changed_files,omitempty"`
	Verification *Verification    `json:"verification,omitempty"`
	Summary      string           `json:"summary,omitempty"`
	SpecArchive  string           `json:"spec_archive"`
	AIUsage      *aiusage.Summary `json:"ai_usage,omitempty"`
}

type Workspace struct {
	Dir string
	Metadata
}

func configBase() (string, error) {
	if value := os.Getenv("SPEC_CONFIG_HOME"); value != "" {
		return value, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "spec"), nil
}

func stateBase() (string, error) {
	if value := os.Getenv("SPEC_STATE_HOME"); value != "" {
		return value, nil
	}
	if runtime.GOOS == "windows" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "Spec", "State"), nil
	}
	if value := os.Getenv("XDG_STATE_HOME"); value != "" {
		return filepath.Join(value, "spec"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "spec"), nil
}

// Path returns the directory containing Spec's external project metadata and history.
func Path() (string, error) {
	return stateBase()
}

// Purge removes all external project metadata and history.
func Purge() error {
	base, err := stateBase()
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	volumeRoot := filepath.VolumeName(absolute) + string(filepath.Separator)
	if absolute == volumeRoot || filepath.Clean(absolute) == filepath.Clean(home) {
		return fmt.Errorf("refusing to purge unsafe state path %q", absolute)
	}
	return os.RemoveAll(absolute)
}

func ConfigPath() (string, error) {
	base, err := configBase()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "config.yml"), nil
}

func EnsureUserDirs() error {
	config, err := configBase()
	if err != nil {
		return err
	}
	state, err := stateBase()
	if err != nil {
		return err
	}
	for _, dir := range []string{config, filepath.Join(config, "templates"), filepath.Join(config, "defaults"), filepath.Join(state, "projects")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func workspaceID(root string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	return hex.EncodeToString(sum[:12])
}

func workspaceDir(root string) (string, error) {
	base, err := stateBase()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "projects", workspaceID(root)), nil
}

func Register(root string) (Workspace, error) {
	if err := EnsureUserDirs(); err != nil {
		return Workspace{}, err
	}
	dir, err := workspaceDir(root)
	if err != nil {
		return Workspace{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Workspace{}, err
	}
	metadataPath := filepath.Join(dir, "metadata.json")
	if _, err := os.Stat(metadataPath); err == nil {
		return Load(root)
	} else if !os.IsNotExist(err) {
		return Workspace{}, err
	}
	workspace := Workspace{Dir: dir, Metadata: Metadata{Root: root, ID: workspaceID(root)}}
	return workspace, workspace.saveMetadata()
}

func Load(root string) (Workspace, error) {
	dir, err := workspaceDir(root)
	if err != nil {
		return Workspace{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace is not registered; run `spec init`: %w", err)
	}
	var metadata Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return Workspace{}, err
	}
	if metadata.Root != root {
		return Workspace{}, fmt.Errorf("workspace state does not match Git root")
	}
	return Workspace{Dir: dir, Metadata: metadata}, nil
}

func (workspace Workspace) saveMetadata() error {
	return writeJSON(filepath.Join(workspace.Dir, "metadata.json"), workspace.Metadata)
}

func (workspace *Workspace) Start(title, baseSHA string, now time.Time) error {
	if workspace.Active {
		return fmt.Errorf("a change is already active; finish it with `spec done`")
	}
	for _, name := range []string{"prompt.md", "verification.json"} {
		if err := os.Remove(filepath.Join(workspace.Dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	workspace.Active = true
	workspace.Title = title
	workspace.StartedAt = now
	workspace.BaseSHA = baseSHA
	workspace.SandboxSession = nil
	return workspace.saveMetadata()
}

func (workspace *Workspace) Abandon() error {
	for _, name := range []string{"prompt.md", "verification.json"} {
		if err := os.Remove(filepath.Join(workspace.Dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	workspace.Active = false
	workspace.Title = ""
	workspace.StartedAt = time.Time{}
	workspace.BaseSHA = ""
	workspace.SandboxSession = nil
	return workspace.saveMetadata()
}

func (workspace *Workspace) SaveSandboxSession(session SandboxSession) error {
	if !workspace.Active {
		return fmt.Errorf("no active change; run `spec new`")
	}
	copy := session
	workspace.SandboxSession = &copy
	return workspace.saveMetadata()
}

func (workspace Workspace) SavePrompt(content string) error {
	return writeFile(filepath.Join(workspace.Dir, "prompt.md"), []byte(content))
}

func (workspace Workspace) SaveVerification(result Verification) error {
	return writeJSON(filepath.Join(workspace.Dir, "verification.json"), result)
}

func (workspace Workspace) Verification() (*Verification, error) {
	data, err := os.ReadFile(filepath.Join(workspace.Dir, "verification.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result Verification
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// HistoryRecords returns completed Specs in their stored chronological order.
func (workspace Workspace) HistoryRecords() ([]History, error) {
	file, err := os.Open(filepath.Join(workspace.Dir, "history.jsonl"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var records []History
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var record History
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, fmt.Errorf("read Spec history: %w", err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func (workspace *Workspace) Finish(record History, specContent []byte, activePath string) (History, error) {
	if !workspace.Active {
		return History{}, fmt.Errorf("no active change; run `spec new`")
	}
	archive, err := workspace.archiveSpec(record.Title, specContent)
	if err != nil {
		return History{}, err
	}
	record.SpecArchive = archive
	if err := workspace.appendHistory(record); err != nil {
		return History{}, err
	}
	if err := os.Remove(activePath); err != nil {
		return History{}, err
	}
	for _, name := range []string{"prompt.md", "verification.json"} {
		if err := os.Remove(filepath.Join(workspace.Dir, name)); err != nil && !os.IsNotExist(err) {
			return History{}, err
		}
	}
	workspace.Active = false
	workspace.Title = ""
	workspace.StartedAt = time.Time{}
	workspace.BaseSHA = ""
	workspace.SandboxSession = nil
	if err := workspace.saveMetadata(); err != nil {
		return History{}, err
	}
	return record, nil
}

func (workspace Workspace) archiveSpec(title string, content []byte) (string, error) {
	dir := filepath.Join(workspace.Dir, "specs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	stamp := workspace.StartedAt.UTC().Format("20060102-150405.000000000")
	name := stamp + "-" + slug(title) + ".md"
	path := filepath.Join(dir, name)
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) == string(content) {
			return filepath.ToSlash(filepath.Join("specs", name)), nil
		}
		return "", fmt.Errorf("spec archive already exists with different content: %s", path)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	_, writeErr := file.Write(content)
	closeErr := file.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return filepath.ToSlash(filepath.Join("specs", name)), nil
}

func (workspace Workspace) appendHistory(record History) error {
	path := filepath.Join(workspace.Dir, "history.jsonl")
	present, err := historyContains(path, record.SpecArchive)
	if err != nil {
		return err
	}
	if present {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	err = encoder.Encode(record)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return nil
}

func historyContains(path, archive string) (bool, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var record History
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return false, err
		}
		if record.SpecArchive == archive {
			return true, nil
		}
	}
	return false, scanner.Err()
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	words := strings.FieldsFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	if len(words) == 0 {
		return "change"
	}
	return strings.Join(words, "-")
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, append(data, '\n'))
}

func writeFile(path string, data []byte) error {
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Rename(temporary, path)
}
