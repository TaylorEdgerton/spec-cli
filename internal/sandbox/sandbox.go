package sandbox

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/TaylorEdgerton/spec-cli/internal/aiusage"
	"github.com/TaylorEdgerton/spec-cli/internal/secrets"
	"github.com/TaylorEdgerton/spec-cli/internal/state"
)

const (
	collectorContainer = "spec-otel-collector"
	collectorConfig    = "/var/lib/spec-telemetry/collector.yaml"
	collectorEndpoint  = "http://127.0.0.1:9464/metrics"
	defaultImage       = "otel/opentelemetry-collector-contrib:0.158.0"
)

func Available() bool {
	_, err := exec.LookPath("sbx")
	return err == nil
}

func Launch(root, agent string, assumeYes bool, input io.Reader, output io.Writer) error {
	findings, err := secrets.Scan(root)
	if err != nil {
		return err
	}
	if len(findings) > 0 {
		fmt.Fprintln(output, "Potential AI-readable sensitive files:")
		fmt.Fprintln(output)
		for _, finding := range findings {
			fmt.Fprintf(output, "  %-28s %s\n", finding.Path, finding.Reason)
		}
		fmt.Fprintln(output)
		fmt.Fprintln(output, "These files are inside the workspace exposed to the sandbox.")
		fmt.Fprintln(output, "Gitignored files remain readable by tools inside that workspace.")
		fmt.Fprintln(output, "This check does not find all secrets.")
		if !assumeYes {
			fmt.Fprintln(output)
			fmt.Fprint(output, "Continue? [y/N] ")
			var answer string
			_, _ = fmt.Fscanln(input, &answer)
			answer = strings.ToLower(strings.TrimSpace(answer))
			if answer != "y" && answer != "yes" {
				return fmt.Errorf("sandbox launch cancelled")
			}
		}
	}
	if !Available() {
		return fmt.Errorf("Docker Sandbox CLI `sbx` is not available")
	}
	workspace, err := state.Load(root)
	if err != nil {
		return err
	}
	if !workspace.Active {
		return fmt.Errorf("an active specification is required; run `spec new` before `spec sandbox`")
	}
	if _, err := os.Stat(filepath.Join(root, ".spec.md")); err != nil {
		return fmt.Errorf("active specification is not available: %w", err)
	}
	if agent == "" {
		agent = "shell"
	}

	session, existing, err := sessionFor(workspace, agent, time.Now().UTC())
	if err != nil {
		return err
	}
	environment, agentArgs := providerConfiguration(agent, workspace.ID, session.ID)
	exists, err := sandboxExists(session.Name)
	if err != nil {
		return err
	}
	if !exists {
		args := []string{"create", "--name", session.Name}
		args = appendEnvironment(args, environment)
		args = append(args, agent, root)
		if err := runSBX(nil, output, output, args...); err != nil {
			return fmt.Errorf("create telemetry-enabled sandbox: %w", err)
		}
		existing = false
	}
	if err := ensureCollector(session.Name, existing); err != nil {
		return err
	}
	if workspace.SandboxSession == nil {
		if err := workspace.SaveSandboxSession(session); err != nil {
			return fmt.Errorf("save sandbox session metadata: %w", err)
		}
	}

	fmt.Fprintf(output, "AI usage telemetry: local metrics only (%s)\n", session.Name)
	args := []string{"run", "--name", session.Name}
	args = appendEnvironment(args, environment)
	if len(agentArgs) > 0 {
		args = append(args, "--")
		args = append(args, agentArgs...)
	}
	return runSBX(input, output, output, args...)
}

func Usage(root string, now time.Time) aiusage.Summary {
	workspace, err := state.Load(root)
	if err != nil || !workspace.Active || workspace.SandboxSession == nil {
		return aiusage.Unavailable("use a spec-managed sandbox environment for usage stats", 0)
	}
	session := workspace.SandboxSession
	duration := now.UTC().Sub(session.StartedAt)
	if !supportedAgent(session.Agent) {
		return aiusage.Unavailable(fmt.Sprintf("%s does not expose supported token telemetry", session.Agent), duration)
	}
	if !Available() {
		return aiusage.Unavailable("Docker Sandbox is unavailable", duration)
	}
	var stdout bytes.Buffer
	if err := runSBX(nil, &stdout, io.Discard,
		"exec", session.Name, "--", "sh", "-c", metricsCommand,
	); err != nil {
		return aiusage.Unavailable("the sandbox telemetry collector is unavailable", duration)
	}
	return aiusage.ParsePrometheus(stdout.String(), duration)
}

func StopTelemetry(session *state.SandboxSession) error {
	if session == nil || !Available() {
		return nil
	}
	return runSBX(nil, io.Discard, io.Discard,
		"exec", session.Name, "--", "docker", "stop", collectorContainer,
	)
}

func sessionFor(workspace state.Workspace, agent string, now time.Time) (state.SandboxSession, bool, error) {
	if workspace.SandboxSession != nil {
		if workspace.SandboxSession.Agent != agent {
			return state.SandboxSession{}, true, fmt.Errorf(
				"the active Spec sandbox uses %s; finish the Spec before switching to %s",
				workspace.SandboxSession.Agent, agent,
			)
		}
		return *workspace.SandboxSession, true, nil
	}
	seed := workspace.ID + "\x00" + workspace.StartedAt.UTC().Format(time.RFC3339Nano) + "\x00" + agent
	sum := sha256.Sum256([]byte(seed))
	sessionID := hex.EncodeToString(sum[:12])
	workspacePrefix := workspace.ID
	if len(workspacePrefix) > 8 {
		workspacePrefix = workspacePrefix[:8]
	}
	return state.SandboxSession{
		ID:        sessionID,
		Name:      "spec-" + workspacePrefix + "-" + agentSlug(agent) + "-" + sessionID[:8],
		Agent:     agent,
		StartedAt: now,
	}, false, nil
}

func agentSlug(agent string) string {
	var builder strings.Builder
	lastDash := false
	for _, character := range strings.ToLower(agent) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			builder.WriteRune(character)
			lastDash = false
		} else if builder.Len() > 0 && !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
		if builder.Len() >= 16 {
			break
		}
	}
	value := strings.Trim(builder.String(), "-")
	if value == "" {
		return "agent"
	}
	return value
}

func supportedAgent(agent string) bool {
	switch agent {
	case "claude", "codex", "copilot", "gemini":
		return true
	default:
		return false
	}
}

func providerConfiguration(agent, workspaceID, sessionID string) ([]string, []string) {
	resource := "spec.workspace.id=" + workspaceID + ",spec.session.id=" + sessionID + ",spec.agent=" + agentSlug(agent)
	switch agent {
	case "claude":
		return []string{
			"CLAUDE_CODE_ENABLE_TELEMETRY=1",
			"OTEL_METRICS_EXPORTER=otlp",
			"OTEL_LOGS_EXPORTER=none",
			"OTEL_TRACES_EXPORTER=none",
			"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL=http/protobuf",
			"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://127.0.0.1:4318/v1/metrics",
			"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=cumulative",
			"OTEL_METRIC_EXPORT_INTERVAL=5000",
			"OTEL_RESOURCE_ATTRIBUTES=" + resource,
			"OTEL_LOG_USER_PROMPTS=0",
			"OTEL_LOG_ASSISTANT_RESPONSES=0",
			"OTEL_LOG_TOOL_DETAILS=0",
			"OTEL_LOG_TOOL_CONTENT=0",
			"OTEL_LOG_RAW_API_BODIES=0",
		}, nil
	case "codex":
		return []string{"OTEL_RESOURCE_ATTRIBUTES=" + resource}, []string{
			"-c", `analytics.enabled=true`,
			"-c", `otel.log_user_prompt=false`,
			"-c", `otel.exporter="none"`,
			"-c", `otel.trace_exporter="none"`,
			"-c", `otel.metrics_exporter={ otlp-http = { endpoint = "http://127.0.0.1:4318/v1/metrics", protocol = "binary" } }`,
		}
	case "copilot":
		return []string{
			"COPILOT_OTEL_ENABLED=true",
			"COPILOT_OTEL_EXPORTER_TYPE=otlp-http",
			"OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318",
			"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
			"OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=false",
			"OTEL_LOGS_EXPORTER=none",
			"OTEL_TRACES_EXPORTER=none",
			"OTEL_RESOURCE_ATTRIBUTES=" + resource,
		}, nil
	case "gemini":
		return []string{
			"GEMINI_TELEMETRY_ENABLED=true",
			"GEMINI_TELEMETRY_TRACES_ENABLED=false",
			"GEMINI_TELEMETRY_TARGET=local",
			"GEMINI_TELEMETRY_OTLP_ENDPOINT=http://127.0.0.1:4317",
			"GEMINI_TELEMETRY_OTLP_PROTOCOL=grpc",
			"GEMINI_TELEMETRY_LOG_PROMPTS=false",
			"GEMINI_TELEMETRY_USE_COLLECTOR=true",
			"OTEL_RESOURCE_ATTRIBUTES=" + resource,
		}, nil
	default:
		return []string{"OTEL_RESOURCE_ATTRIBUTES=" + resource}, nil
	}
}

func appendEnvironment(args, environment []string) []string {
	for _, value := range environment {
		args = append(args, "--env", value)
	}
	return args
}

func sandboxExists(name string) (bool, error) {
	var output bytes.Buffer
	if err := runSBX(nil, &output, io.Discard, "ls", "--quiet"); err != nil {
		return false, fmt.Errorf("list Docker Sandboxes: %w", err)
	}
	for _, existing := range strings.Fields(output.String()) {
		if existing == name {
			return true, nil
		}
	}
	return false, nil
}

func ensureCollector(sandboxName string, existing bool) error {
	if existing {
		if err := runSBX(nil, io.Discard, io.Discard,
			"exec", sandboxName, "--", "docker", "start", collectorContainer,
		); err == nil {
			return waitForCollector(sandboxName)
		}
	}
	if err := runSBX(strings.NewReader(otelCollectorConfig), io.Discard, io.Discard,
		"exec", "-i", "-u", "root", sandboxName, "--", "sh", "-c",
		"install -d -m 0755 /var/lib/spec-telemetry; cat > "+collectorConfig+"; chmod 0644 "+collectorConfig,
	); err != nil {
		return fmt.Errorf("write OpenTelemetry Collector configuration inside sandbox: %w", err)
	}
	_ = runSBX(nil, io.Discard, io.Discard,
		"exec", sandboxName, "--", "docker", "rm", "-f", collectorContainer,
	)
	image := strings.TrimSpace(os.Getenv("SPEC_OTEL_COLLECTOR_IMAGE"))
	if image == "" {
		image = defaultImage
	}
	if err := runSBX(nil, io.Discard, io.Discard,
		"exec", sandboxName, "--", "docker", "run", "-d",
		"--name", collectorContainer,
		"--pull", "missing",
		"-p", "127.0.0.1:4317:4317",
		"-p", "127.0.0.1:4318:4318",
		"-p", "127.0.0.1:9464:9464",
		"-v", collectorConfig+":/etc/otelcol-contrib/config.yaml:ro",
		image,
		"--config=/etc/otelcol-contrib/config.yaml",
	); err != nil {
		return fmt.Errorf("start OpenTelemetry Collector inside sandbox: %w", err)
	}
	return waitForCollector(sandboxName)
}

func waitForCollector(sandboxName string) error {
	if err := runSBX(nil, io.Discard, io.Discard,
		"exec", sandboxName, "--", "sh", "-c",
		"attempt=0; while [ $attempt -lt 20 ]; do if "+metricsCommand+" >/dev/null 2>&1; then exit 0; fi; attempt=$((attempt + 1)); sleep 0.5; done; exit 1",
	); err != nil {
		return fmt.Errorf("OpenTelemetry Collector did not become ready")
	}
	return nil
}

func runSBX(stdin io.Reader, stdout, stderr io.Writer, args ...string) error {
	command := exec.Command("sbx", args...)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

const metricsCommand = `if command -v curl >/dev/null 2>&1; then curl -fsS ` + collectorEndpoint + `; else wget -qO- ` + collectorEndpoint + `; fi`

const otelCollectorConfig = `receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
      http:
        endpoint: 0.0.0.0:4318

processors:
  filter/usage:
    error_mode: silent
    metrics:
      metric:
        - 'name != "claude_code.token.usage" and name != "claude_code.cost.usage" and name != "codex.turn.token_usage" and name != "codex.api_request" and name != "gemini_cli.token.usage" and name != "gemini_cli.api.request.count" and name != "gen_ai.client.token.usage"'
  transform/privacy:
    error_mode: silent
    metric_statements:
      - context: datapoint
        statements:
          - set(attributes["spec.agent"], resource.attributes["spec.agent"])
          - set(attributes["service.name"], resource.attributes["service.name"])
          - keep_keys(attributes, ["model", "type", "token_type", "gen_ai.token.type", "gen_ai.request.model", "gen_ai.response.model", "gen_ai.provider.name", "spec.agent", "service.name"])
      - context: metric
        statements:
          - aggregate_on_attributes("sum", ["model", "type", "token_type", "gen_ai.token.type", "gen_ai.request.model", "gen_ai.response.model", "gen_ai.provider.name", "spec.agent", "service.name"])

exporters:
  prometheus:
    endpoint: 0.0.0.0:9464
    metric_expiration: 168h	
    resource_to_telemetry_conversion:
      enabled: false

service:
  telemetry:
    logs:
      level: warn
  pipelines:
    metrics:
      receivers: [otlp]
      processors: [filter/usage, transform/privacy]
      exporters: [prometheus]
`
