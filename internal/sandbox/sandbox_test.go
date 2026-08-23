package sandbox

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

func TestLaunchWarnsAndCancelsBeforeCommand(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := Launch(root, "shell", false, strings.NewReader("n\n"), &output)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("error = %v", err)
	}
	text := output.String()
	if !strings.Contains(text, ".env") || !strings.Contains(text, "Gitignored files remain readable") {
		t.Fatalf("warning = %q", text)
	}
	if strings.Contains(text, "SECRET=value") {
		t.Fatal("warning printed a secret value")
	}
}

func TestLaunchStartsPrivateCollectorAndReusesSandboxSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX command stub")
	}
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	if err := os.WriteFile(filepath.Join(root, ".spec.md"), []byte("# Change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace, err := state.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.Start("Change", "base", time.Date(2026, 8, 23, 1, 2, 3, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	statePath := filepath.Join(t.TempDir(), "sandboxes")
	configPath := filepath.Join(t.TempDir(), "collector.yaml")
	stub := `#!/usr/bin/env sh
set -e
printf '%s\n' "$*" >> "$SPEC_SBX_LOG"
case "$1" in
  ls)
    if [ -f "$SPEC_SBX_STATE" ]; then cat "$SPEC_SBX_STATE"; fi
    ;;
  create)
    shift
    while [ "$#" -gt 0 ]; do
      if [ "$1" = "--name" ]; then
        printf '%s\n' "$2" > "$SPEC_SBX_STATE"
        break
      fi
      shift
    done
    ;;
  exec)
    case "$*" in
      *"cat > /var/lib/spec-telemetry/collector.yaml"*) cat > "$SPEC_COLLECTOR_CONFIG" ;;
      *"while [ \$attempt"*) exit 0 ;;
      *"curl -fsS http://127.0.0.1:9464/metrics"*) printf '%s\n' "$SPEC_SBX_METRICS" ;;
    esac
    ;;
  run) ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(tools, "sbx"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SPEC_SBX_LOG", logPath)
	t.Setenv("SPEC_SBX_STATE", statePath)
	t.Setenv("SPEC_COLLECTOR_CONFIG", configPath)
	t.Setenv("SPEC_SBX_METRICS", `claude_code_token_usage_tokens_total{model="claude-sonnet-5",type="input"} 42`)
	var output bytes.Buffer
	if err := Launch(root, "claude", true, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	firstSession, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if firstSession.SandboxSession == nil || firstSession.SandboxSession.Agent != "claude" {
		t.Fatalf("sandbox session = %+v", firstSession.SandboxSession)
	}
	if err := Launch(root, "claude", true, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	commands := string(data)
	if strings.Count(commands, "create --name") != 1 {
		t.Fatalf("sandbox was not reused:\n%s", commands)
	}
	if strings.Count(commands, "docker run -d") != 1 || !strings.Contains(commands, defaultImage) {
		t.Fatalf("collector startup commands:\n%s", commands)
	}
	if strings.Count(commands, "run --name") != 2 {
		t.Fatalf("agent reattach commands:\n%s", commands)
	}
	for _, expected := range []string{
		"OTEL_LOGS_EXPORTER=none",
		"OTEL_TRACES_EXPORTER=none",
		"OTEL_LOG_USER_PROMPTS=0",
		"OTEL_LOG_RAW_API_BODIES=0",
	} {
		if !strings.Contains(commands, expected) {
			t.Fatalf("missing privacy setting %q:\n%s", expected, commands)
		}
	}
	collector, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	collectorText := string(collector)
	if !strings.Contains(collectorText, "processors: [filter/usage, transform/privacy]") ||
		!strings.Contains(collectorText, `keep_keys(attributes, ["model", "type", "token_type", "gen_ai.token.type"`) ||
		!strings.Contains(collectorText, `name != "gemini_cli.token.usage"`) ||
		!strings.Contains(collectorText, `name != "gen_ai.client.token.usage"`) {
		t.Fatalf("collector privacy configuration:\n%s", collector)
	}
	if strings.Contains(collectorText, "pipelines:\n    logs:") || strings.Contains(collectorText, "pipelines:\n    traces:") {
		t.Fatalf("collector enabled content-bearing signals:\n%s", collector)
	}

	usage := Usage(root, firstSession.SandboxSession.StartedAt.Add(time.Minute))
	if !usage.Available || len(usage.Usage) != 1 || usage.Usage[0].InputTokens != 42 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestUsageWithoutSandboxExplainsRequirement(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SPEC_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("SPEC_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	workspace, err := state.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.Start("Change", "base", time.Now()); err != nil {
		t.Fatal(err)
	}
	result := Usage(root, time.Now())
	if result.Available || result.UnavailableReason != "use a spec-managed sandbox environment for usage stats" {
		t.Fatalf("usage = %+v", result)
	}
}

func TestCodexConfigurationExportsMetricsOnly(t *testing.T) {
	environment, args := providerConfiguration("codex", "workspace", "session")
	joined := strings.Join(append(environment, args...), " ")
	for _, expected := range []string{
		`otel.log_user_prompt=false`,
		`otel.exporter="none"`,
		`otel.trace_exporter="none"`,
		`otel.metrics_exporter={ otlp-http = { endpoint = "http://127.0.0.1:4318/v1/metrics", protocol = "binary" } }`,
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("Codex configuration missing %q: %s", expected, joined)
		}
	}
}

func TestCopilotConfigurationExportsMetricsWithoutContent(t *testing.T) {
	environment, _ := providerConfiguration("copilot", "workspace", "session")
	joined := strings.Join(environment, " ")
	for _, expected := range []string{
		`COPILOT_OTEL_ENABLED=true`,
		`COPILOT_OTEL_EXPORTER_TYPE=otlp-http`,
		`OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=false`,
		`OTEL_LOGS_EXPORTER=none`,
		`OTEL_TRACES_EXPORTER=none`,
		`spec.agent=copilot`,
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("Copilot configuration missing %q: %s", expected, joined)
		}
	}
}

func TestGeminiConfigurationUsesExternalMetricsCollectorWithoutPrompts(t *testing.T) {
	environment, _ := providerConfiguration("gemini", "workspace", "session")
	joined := strings.Join(environment, " ")
	for _, expected := range []string{
		`GEMINI_TELEMETRY_ENABLED=true`,
		`GEMINI_TELEMETRY_TRACES_ENABLED=false`,
		`GEMINI_TELEMETRY_TARGET=local`,
		`GEMINI_TELEMETRY_LOG_PROMPTS=false`,
		`GEMINI_TELEMETRY_USE_COLLECTOR=true`,
		`spec.agent=gemini`,
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("Gemini configuration missing %q: %s", expected, joined)
		}
	}
}
