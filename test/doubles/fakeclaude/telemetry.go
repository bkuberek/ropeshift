package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// standInAccount is what the real CLI's log records carry of a person and an organization. Every value
// is invented, so a test can look for each one where it must not go.
var standInAccount = map[string]string{
	"user.email": "stand-in@example.com", "user.account_uuid": "00000000-0000-0000-0000-00000000c0de",
	"user.id": "stand-in-user", "organization.id": "00000000-0000-0000-0000-0000000000a1",
}

// sendTelemetry stands for the real CLI's exporter: with telemetry on, it sends a prompt event and a
// request log to the endpoint its environment names, with the resource attributes and headers its
// environment names. The prompt event holds the prompt only when its content flag is set, as the real
// CLI's does. It sends to a loopback address only, because this double reaches nothing past this
// machine.
func sendTelemetry(prompt string) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if os.Getenv("CLAUDE_CODE_ENABLE_TELEMETRY") != "1" || endpoint == "" || os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL") != "http/json" {
		return
	}
	target, err := url.Parse(strings.TrimSuffix(endpoint, "/") + "/v1/logs")
	if err != nil {
		return
	}
	if ip := net.ParseIP(target.Hostname()); ip == nil || !ip.IsLoopback() {
		return
	}
	resource := []any{keyValue("service.name", "claude-code")}
	for pair := range strings.SplitSeq(os.Getenv("OTEL_RESOURCE_ATTRIBUTES"), ",") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		if decoded, err := url.PathUnescape(value); err == nil {
			value = decoded
		}
		resource = append(resource, keyValue(key, value))
	}
	for key, value := range standInAccount {
		resource = append(resource, keyValue(key, value))
	}
	if os.Getenv("OTEL_LOG_USER_PROMPTS") != "1" {
		prompt = "<REDACTED>"
	}
	now := strconv.FormatInt(time.Now().UnixNano(), 10)
	record := func(name string, attributes ...any) map[string]any {
		return map[string]any{"timeUnixNano": now, "body": map[string]any{"stringValue": "claude_code." + name}, "attributes": attributes}
	}
	body := map[string]any{"resourceLogs": []any{map[string]any{
		"resource": map[string]any{"attributes": resource},
		"scopeLogs": []any{map[string]any{
			"scope": map[string]any{"name": "com.anthropic.claude_code.events"},
			"logRecords": []any{
				record("user_prompt", keyValue("event.name", "user_prompt"), keyValue("prompt", prompt)),
				record("api_request", keyValue("event.name", "api_request"), intValue("input_tokens", 12), intValue("cache_read_tokens", 3000),
					intValue("cache_creation_tokens", 500), intValue("output_tokens", 40)),
			},
		}},
	}}}
	raw, err := json.Marshal(body)
	if err != nil {
		return
	}
	request, err := http.NewRequest(http.MethodPost, target.String(), bytes.NewReader(raw))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	for pair := range strings.SplitSeq(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"), ",") {
		if name, value, ok := strings.Cut(pair, "="); ok {
			request.Header.Set(name, value)
		}
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	if response, err := client.Do(request); err == nil {
		_ = response.Body.Close()
	}
}

func keyValue(key, value string) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
}

func intValue(key string, value int) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"intValue": strconv.Itoa(value)}}
}
