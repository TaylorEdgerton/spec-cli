package aiusage

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestParsePrometheusNormalisesSupportedAgents(t *testing.T) {
	metrics := `# HELP claude_code_token_usage_tokens_total tokens
claude_code_token_usage_tokens_total{model="claude-sonnet-5",type="input"} 100
claude_code_token_usage_tokens_total{model="claude-sonnet-5",type="cacheRead"} 30
claude_code_token_usage_tokens_total{model="claude-sonnet-5",type="cacheCreation"} 10
claude_code_token_usage_tokens_total{model="claude-sonnet-5",type="output"} 20
claude_code_cost_usage_USD_total{model="claude-sonnet-5"} 1.25
codex_turn_token_usage_tokens_sum{model="gpt-5.6-sol",token_type="input"} 200
codex_turn_token_usage_tokens_sum{model="gpt-5.6-sol",token_type="cached_input"} 40
codex_turn_token_usage_tokens_sum{model="gpt-5.6-sol",token_type="output"} 15
codex_turn_token_usage_tokens_sum{model="gpt-5.6-sol",token_type="reasoning_output"} 5
codex_turn_token_usage_tokens_sum{model="gpt-5.6-sol",token_type="total"} 220
codex_api_request_total{model="gpt-5.6-sol"} 3
gemini_cli_token_usage_total{model="gemini-2.5-pro",type="input"} 300
gemini_cli_token_usage_total{model="gemini-2.5-pro",type="cache"} 50
gemini_cli_token_usage_total{model="gemini-2.5-pro",type="tool"} 10
gemini_cli_token_usage_total{model="gemini-2.5-pro",type="output"} 25
gemini_cli_token_usage_total{model="gemini-2.5-pro",type="thought"} 5
gemini_cli_api_request_count_total{model="gemini-2.5-pro"} 2
gen_ai_client_token_usage_tokens_sum{spec_agent="copilot",gen_ai_response_model="claude-sonnet-4",gen_ai_token_type="input"} 400
gen_ai_client_token_usage_tokens_count{spec_agent="copilot",gen_ai_response_model="claude-sonnet-4",gen_ai_token_type="input"} 4
gen_ai_client_token_usage_tokens_sum{spec_agent="copilot",gen_ai_response_model="claude-sonnet-4",gen_ai_token_type="output"} 45
gen_ai_client_token_usage_tokens_sum{spec_agent="gemini",gen_ai_response_model="gemini-2.5-pro",gen_ai_token_type="input"} 999
unrelated_metric{prompt="must not appear"} 999
`

	summary := ParsePrometheus(metrics, 41*time.Minute)
	if !summary.Available || len(summary.Usage) != 4 {
		t.Fatalf("summary = %+v", summary)
	}
	claude := usageByProvider(t, summary, "Claude Code")
	if claude.Provider != "Claude Code" || claude.Model != "claude-sonnet-5" ||
		claude.InputTokens != 140 || claude.CachedInputTokens != 40 || claude.OutputTokens != 20 ||
		claude.EstimatedCostUSD == nil || *claude.EstimatedCostUSD != 1.25 {
		t.Fatalf("Claude usage = %+v", claude)
	}
	codex := usageByProvider(t, summary, "Codex")
	if codex.Provider != "Codex" || codex.Model != "gpt-5.6-sol" ||
		codex.InputTokens != 200 || codex.CachedInputTokens != 40 || codex.OutputTokens != 20 || codex.Requests != 3 {
		t.Fatalf("Codex usage = %+v", codex)
	}
	gemini := usageByProvider(t, summary, "Gemini CLI")
	if gemini.Model != "gemini-2.5-pro" || gemini.InputTokens != 360 || gemini.CachedInputTokens != 50 ||
		gemini.OutputTokens != 30 || gemini.Requests != 2 {
		t.Fatalf("Gemini usage = %+v", gemini)
	}
	copilot := usageByProvider(t, summary, "GitHub Copilot")
	if copilot.Model != "claude-sonnet-4" || copilot.InputTokens != 400 || copilot.OutputTokens != 45 ||
		copilot.Requests != 4 {
		t.Fatalf("Copilot usage = %+v", copilot)
	}

	var output bytes.Buffer
	Format(&output, summary)
	text := output.String()
	for _, expected := range []string{
		"AI usage", "Claude Code (claude-sonnet-5)", "140", "Codex (gpt-5.6-sol)",
		"Gemini CLI (gemini-2.5-pro)", "GitHub Copilot (claude-sonnet-4)", "Sandbox       41m",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("formatted output missing %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "must not appear") {
		t.Fatalf("unrelated metric leaked into output: %s", text)
	}
}

func usageByProvider(t *testing.T, summary Summary, provider string) AIUsage {
	t.Helper()
	for _, usage := range summary.Usage {
		if usage.Provider == provider {
			return usage
		}
	}
	t.Fatalf("provider %q not found in %+v", provider, summary)
	return AIUsage{}
}

func TestFormatUnavailableWithoutSandbox(t *testing.T) {
	var output bytes.Buffer
	Format(&output, Unavailable("use a spec-managed sandbox environment for usage stats", 0))
	want := "AI usage\n\nusage unavailable: use a spec-managed sandbox environment for usage stats\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}
