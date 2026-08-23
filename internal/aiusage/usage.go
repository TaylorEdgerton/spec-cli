package aiusage

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

type AIUsage struct {
	Provider          string   `json:"provider"`
	Model             string   `json:"model"`
	InputTokens       uint64   `json:"input_tokens"`
	OutputTokens      uint64   `json:"output_tokens"`
	CachedInputTokens uint64   `json:"cached_input_tokens"`
	Requests          uint64   `json:"requests"`
	EstimatedCostUSD  *float64 `json:"estimated_cost_usd,omitempty"`
}

type Summary struct {
	Available              bool      `json:"available"`
	Usage                  []AIUsage `json:"usage,omitempty"`
	SandboxDurationSeconds uint64    `json:"sandbox_duration_seconds"`
	UnavailableReason      string    `json:"unavailable_reason,omitempty"`
}

func Unavailable(reason string, duration time.Duration) Summary {
	return Summary{
		SandboxDurationSeconds: durationSeconds(duration),
		UnavailableReason:      strings.TrimSpace(reason),
	}
}

func ParsePrometheus(input string, duration time.Duration) Summary {
	byProviderModel := make(map[string]*AIUsage)
	seen := false
	scanner := bufio.NewScanner(strings.NewReader(input))
	for scanner.Scan() {
		sample, ok := parseSample(scanner.Text())
		if !ok {
			continue
		}
		model := sampleModel(sample.Labels)
		switch {
		case strings.Contains(sample.Name, "claude_code_token_usage") && !strings.HasSuffix(sample.Name, "_created"):
			usage := usageFor(byProviderModel, "Claude Code", model)
			value := tokenValue(sample.Value)
			switch sample.Labels["type"] {
			case "input":
				usage.InputTokens += value
				seen = true
			case "output":
				usage.OutputTokens += value
				seen = true
			case "cacheRead", "cacheCreation":
				usage.InputTokens += value
				usage.CachedInputTokens += value
				seen = true
			}
		case strings.Contains(sample.Name, "claude_code_cost_usage") && !strings.HasSuffix(sample.Name, "_created"):
			usage := usageFor(byProviderModel, "Claude Code", model)
			addCost(usage, sample.Value)
			seen = true
		case strings.Contains(sample.Name, "codex_turn_token_usage") && strings.HasSuffix(sample.Name, "_sum"):
			usage := usageFor(byProviderModel, "Codex", model)
			value := tokenValue(sample.Value)
			switch sample.Labels["token_type"] {
			case "input":
				usage.InputTokens += value
				seen = true
			case "cached_input":
				usage.CachedInputTokens += value
				seen = true
			case "output", "reasoning_output":
				usage.OutputTokens += value
				seen = true
			}
		case sample.Name == "codex_api_request_total":
			usage := usageFor(byProviderModel, "Codex", model)
			usage.Requests += tokenValue(sample.Value)
			seen = true
		case strings.Contains(sample.Name, "gemini_cli_token_usage") && !strings.HasSuffix(sample.Name, "_created"):
			usage := usageFor(byProviderModel, "Gemini CLI", model)
			value := tokenValue(sample.Value)
			switch sample.Labels["type"] {
			case "input", "tool":
				usage.InputTokens += value
				seen = true
			case "cache":
				usage.InputTokens += value
				usage.CachedInputTokens += value
				seen = true
			case "output", "thought":
				usage.OutputTokens += value
				seen = true
			}
		case strings.Contains(sample.Name, "gemini_cli_api_request_count") && !strings.HasSuffix(sample.Name, "_created"):
			usage := usageFor(byProviderModel, "Gemini CLI", model)
			usage.Requests += tokenValue(sample.Value)
			seen = true
		case strings.Contains(sample.Name, "gen_ai_client_token_usage"):
			provider := genericProvider(sample.Labels)
			if provider == "" {
				continue
			}
			usage := usageFor(byProviderModel, provider, model)
			tokenType := sample.Labels["gen_ai_token_type"]
			switch {
			case strings.HasSuffix(sample.Name, "_sum") && tokenType == "input":
				usage.InputTokens += tokenValue(sample.Value)
				seen = true
			case strings.HasSuffix(sample.Name, "_sum") && tokenType == "output":
				usage.OutputTokens += tokenValue(sample.Value)
				seen = true
			case strings.HasSuffix(sample.Name, "_count") && tokenType == "input":
				usage.Requests += tokenValue(sample.Value)
				seen = true
			}
		}
	}

	result := Summary{Available: seen, SandboxDurationSeconds: durationSeconds(duration)}
	for _, usage := range byProviderModel {
		if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.CachedInputTokens == 0 && usage.Requests == 0 && usage.EstimatedCostUSD == nil {
			continue
		}
		result.Usage = append(result.Usage, *usage)
	}
	sort.Slice(result.Usage, func(i, j int) bool {
		if result.Usage[i].Provider == result.Usage[j].Provider {
			return result.Usage[i].Model < result.Usage[j].Model
		}
		return result.Usage[i].Provider < result.Usage[j].Provider
	})
	if len(result.Usage) == 0 {
		result.Available = false
		result.UnavailableReason = "no supported token telemetry has been received"
	}
	return result
}

func Format(writer io.Writer, summary Summary) {
	fmt.Fprintln(writer, "AI usage")
	fmt.Fprintln(writer)
	FormatSummary(writer, summary, "")
}

// FormatSummary writes one usage summary without a heading. Prefix is applied
// to every line so callers can embed the summary in a larger report.
func FormatSummary(writer io.Writer, summary Summary, prefix string) {
	if !summary.Available || len(summary.Usage) == 0 {
		reason := summary.UnavailableReason
		if reason == "" {
			reason = "no supported token telemetry has been received"
		}
		fmt.Fprintf(writer, "%susage unavailable: %s\n", prefix, reason)
	} else {
		for index, usage := range summary.Usage {
			if index > 0 {
				fmt.Fprintln(writer)
			}
			heading := usage.Provider
			if usage.Model != "" && usage.Model != "unknown" {
				heading += " (" + usage.Model + ")"
			}
			fmt.Fprintln(writer, prefix+heading)
			fmt.Fprintf(writer, "%s  Input       %s\n", prefix, formatUint(usage.InputTokens))
			fmt.Fprintf(writer, "%s  Cached      %s\n", prefix, formatUint(usage.CachedInputTokens))
			fmt.Fprintf(writer, "%s  Output      %s\n", prefix, formatUint(usage.OutputTokens))
			if usage.Requests > 0 {
				fmt.Fprintf(writer, "%s  Requests    %s\n", prefix, formatUint(usage.Requests))
			}
			if usage.EstimatedCostUSD != nil {
				fmt.Fprintf(writer, "%s  Cost        $%.4f estimated\n", prefix, *usage.EstimatedCostUSD)
			}
		}
	}
	if summary.SandboxDurationSeconds > 0 {
		fmt.Fprintln(writer)
		fmt.Fprintf(writer, "%sSandbox       %s\n", prefix, formatDuration(time.Duration(summary.SandboxDurationSeconds)*time.Second))
	}
}

type prometheusSample struct {
	Name   string
	Labels map[string]string
	Value  float64
}

func parseSample(line string) (prometheusSample, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return prometheusSample{}, false
	}
	nameEnd := strings.IndexAny(line, "{ \t")
	if nameEnd <= 0 {
		return prometheusSample{}, false
	}
	sample := prometheusSample{Name: line[:nameEnd], Labels: make(map[string]string)}
	rest := line[nameEnd:]
	if strings.HasPrefix(rest, "{") {
		labelsEnd := findLabelsEnd(rest)
		if labelsEnd < 0 {
			return prometheusSample{}, false
		}
		sample.Labels = parseLabels(rest[1:labelsEnd])
		rest = rest[labelsEnd+1:]
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return prometheusSample{}, false
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return prometheusSample{}, false
	}
	sample.Value = value
	return sample, true
}

func findLabelsEnd(value string) int {
	quoted, escaped := false, false
	for index, char := range value {
		if escaped {
			escaped = false
			continue
		}
		if char == '\\' && quoted {
			escaped = true
			continue
		}
		if char == '"' {
			quoted = !quoted
			continue
		}
		if char == '}' && !quoted {
			return index
		}
	}
	return -1
}

func parseLabels(value string) map[string]string {
	labels := make(map[string]string)
	for len(value) > 0 {
		value = strings.TrimLeft(value, " ,\t")
		equals := strings.IndexByte(value, '=')
		if equals <= 0 {
			break
		}
		key := strings.TrimSpace(value[:equals])
		value = strings.TrimSpace(value[equals+1:])
		if !strings.HasPrefix(value, "\"") {
			break
		}
		end := 1
		escaped := false
		for end < len(value) {
			if escaped {
				escaped = false
				end++
				continue
			}
			if value[end] == '\\' {
				escaped = true
				end++
				continue
			}
			if value[end] == '"' {
				break
			}
			end++
		}
		if end >= len(value) {
			break
		}
		decoded, err := strconv.Unquote(value[:end+1])
		if err == nil {
			labels[key] = decoded
		}
		value = value[end+1:]
	}
	return labels
}

func usageFor(values map[string]*AIUsage, provider, model string) *AIUsage {
	key := provider + "\x00" + model
	if values[key] == nil {
		values[key] = &AIUsage{Provider: provider, Model: model}
	}
	return values[key]
}

func sampleModel(labels map[string]string) string {
	for _, key := range []string{"model", "gen_ai_response_model", "gen_ai_request_model"} {
		if value := strings.TrimSpace(labels[key]); value != "" {
			return value
		}
	}
	return "unknown"
}

func genericProvider(labels map[string]string) string {
	agent := strings.ToLower(strings.TrimSpace(labels["spec_agent"]))
	switch agent {
	case "copilot":
		return "GitHub Copilot"
	case "gemini", "claude", "codex":
		// These agents have richer native metrics. Ignoring their generic metric
		// avoids counting the same request twice.
		return ""
	}

	service := strings.ToLower(strings.TrimSpace(labels["service_name"]))
	provider := strings.ToLower(strings.TrimSpace(labels["gen_ai_provider_name"]))
	if strings.Contains(service, "copilot") || strings.Contains(provider, "github") {
		return "GitHub Copilot"
	}
	return ""
}

func addCost(usage *AIUsage, value float64) {
	if usage.EstimatedCostUSD == nil {
		usage.EstimatedCostUSD = new(float64)
	}
	*usage.EstimatedCostUSD += value
}

func tokenValue(value float64) uint64 {
	if value >= float64(^uint64(0)) {
		return ^uint64(0)
	}
	return uint64(math.Round(value))
}

func durationSeconds(duration time.Duration) uint64 {
	if duration <= 0 {
		return 0
	}
	return uint64(math.Round(duration.Seconds()))
}

func formatUint(value uint64) string {
	digits := strconv.FormatUint(value, 10)
	for index := len(digits) - 3; index > 0; index -= 3 {
		digits = digits[:index] + "," + digits[index:]
	}
	return digits
}

func formatDuration(duration time.Duration) string {
	if duration < time.Minute {
		return fmt.Sprintf("%ds", int(duration.Seconds()))
	}
	hours := int(duration / time.Hour)
	minutes := int(duration%time.Hour) / int(time.Minute)
	if hours > 0 && minutes > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dm", minutes)
}
