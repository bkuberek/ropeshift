//go:build screenshots

package screenshots

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/test/doubles"
)

// The script the fake agent CLI plays for the sample. Each work lane has a script of its own under the
// lane ID the population starts it with, the orchestrator has one under its first run's lane, and any other
// session, the assistant's conversation and the planning session, plays the fixture's own turns.

// The lanes the population starts, by the work each runs.
const (
	laneTimeouts     = "lane-timeouts"
	laneRetries      = "lane-retries"
	laneErrorShape   = "lane-error-shape"
	laneErrorDocs    = "lane-error-docs"
	laneSigningKeys  = "lane-signing-keys"
	laneRateLimit    = "lane-rate-limit"
	laneRequestIDs   = "lane-request-ids"
	reviewErrorShape = "review-error-shape"
	reviewTimeouts   = "review-timeouts"
	reviewRetries    = "review-retries"
	orchestrator     = "orchestrator-1"
)

// The questions a member records, which lanes ask again and the orchestrator takes up the ladder.
const (
	questionSigningKeys = "How long should tokens signed with the old key stay valid after a rotation?"
	questionRetryKey    = "Should a retry reuse the original request's idempotency key?"
	questionLogLevel    = "At what level should a rate limit rejection be logged?"
	questionPartnerTier = "Do partner integrations get a higher rate limit than the storefront?"
)

// The questions' IDs, which the orchestrator's script names.
const (
	qSigningKeys = "q-signing-keys"
	qRetryKey    = "q-retry-key"
	qLogLevel    = "q-log-level"
	qPartnerTier = "q-partner-tier"
)

const errorDocsBlocked = "The public API reference builds from the docs site's repository, which this lane cannot write to, " +
	"so the error code pages cannot be published from here."

type step = map[string]any

func begin() []step {
	return []step{{"init": true}, {"hook": "UserPromptSubmit", "input": map[string]any{"prompt": "the brief"}}}
}

// context reports the context a turn holds, as each API call's first stream event does.
func context(tokens int) step {
	return step{"emit": map[string]any{
		"type": "stream_event", "parent_tool_use_id": nil,
		"event": map[string]any{"type": "message_start", "message": map[string]any{"usage": map[string]any{
			"input_tokens": 12, "cache_creation_input_tokens": tokens / 10, "cache_read_input_tokens": tokens - tokens/10 - 12, "output_tokens": 0,
		}}},
	}}
}

// bash runs one shell command in the lane's worktree. The fake CLI ends the session on a command that
// fails, so every command here succeeds on the sample's files.
func bash(command string) step { return step{"tool": "Bash", "command": command} }

// commit writes one file on the lane's branch, line by line, and commits it. No line holds a single quote.
func commit(file, message string, body ...string) step {
	if strings.Contains(message+strings.Join(body, ""), "'") {
		panic("a scripted commit holds a single quote: " + file)
	}
	return bash("mkdir -p " + filepath.Dir(file) + " && printf '%s\\n' '" + strings.Join(body, "' '") + "' > " + file +
		" && git add -A && git commit -q -m '" + message + "'")
}

func check(name, criterion string) map[string]any {
	return map[string]any{"name": name, "passed": true, "criterion": criterion}
}

func report(outcome string, fields map[string]any) step {
	arguments := map[string]any{"outcome": outcome}
	for key, value := range fields {
		arguments[key] = value
	}
	return step{"mcp": "lane_report", "arguments": arguments}
}

func result(cost float64, output int) step {
	return step{"emit": map[string]any{
		"type": "result", "subtype": "success", "is_error": false, "terminal_reason": "completed", "num_turns": 1,
		"total_cost_usd": cost, "usage": map[string]any{"input_tokens": 1200, "output_tokens": output},
	}}
}

func turn(steps ...step) []step { return append(begin(), steps...) }

// reply is one message of the assistant's conversation.
func reply(id, text string) []step {
	return []step{
		{"init": true},
		{"emit": map[string]any{"type": "assistant", "message": map[string]any{
			"id": id, "role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}},
			"usage": map[string]any{"input_tokens": 900, "output_tokens": 140},
		}}},
		result(0.04, 140),
	}
}

func verdict(criterion, word, reasons string, citations ...string) map[string]any {
	return map[string]any{"criterion": criterion, "verdict": word, "reasons": reasons, "citations": citations}
}

// lanes is every scripted lane.
func lanes() map[string]any {
	script := func(turns ...[]step) map[string]any { return map[string]any{"turns": turns} }
	return map[string]any{
		laneTimeouts: script(turn(
			context(41800),
			bash("git log --oneline -3"),
			commit("internal/orders/client.go", "Set a deadline on every outbound call",
				"package orders", "", "// callTimeout bounds every call to fulfillment and payments.", "const callTimeout = 2 * time.Second"),
			bash("grep -rn 'callTimeout' internal"),
			report("completed", map[string]any{"artifacts": []string{"branch"}, "checks": []any{check("go test ./internal/orders", "c1")}}),
			result(0.42, 6200),
		)),
		laneRetries: script(turn(
			context(59100),
			bash("git log --oneline -5"),
			commit("internal/orders/retry.go", "Retry idempotent calls with exponential backoff",
				"package orders", "", "// retryBudget is how many times an idempotent call is tried again.", "const retryBudget = 3"),
			commit("internal/orders/retry_test.go", "Test the retry budget and the reused key",
				"package orders", "", "func TestRetryStopsAtTheBudget(t *testing.T) {}", "", "func TestRetryReusesTheIdempotencyKey(t *testing.T) {}"),
			report("completed", map[string]any{"artifacts": []string{"branch"}, "checks": []any{
				check("go test ./internal/orders -run Retry", "c1"), check("go test ./internal/orders -run IdempotencyKey", "c2"),
			}}),
			result(0.57, 8800),
		)),
		laneErrorShape: script(turn(
			context(52400),
			bash("grep -rn 'Problem' internal"),
			commit("internal/orders/problem.go", "Write every error through one Problem type",
				"package orders", "", "// writeProblem writes an error as a Problem body with its status.", "func writeProblem(code string, status int) {}"),
			commit("docs/errors.md", "Document the error shape", "# Errors", "", "Every error is a Problem body with a code and a detail."),
			report("completed", map[string]any{"artifacts": []string{"branch"}, "checks": []any{
				check("go test ./internal/orders -run Problem", "c1"), check("docs/errors.md lists the shape", "c2"),
			}}),
			result(0.61, 9300),
		)),
		laneErrorDocs: script(turn(
			context(38200),
			bash("git log --oneline -3 -- docs"),
			bash("grep -rn 'Code' internal/orders/errors.go"),
			report("blocked", map[string]any{"reason": errorDocsBlocked}),
			result(0.38, 5500),
		)),
		reviewErrorShape: script(turn(
			context(20900),
			bash("git log --oneline -5"),
			report("completed", map[string]any{"verdicts": []any{
				verdict("c1", "pass", "Every handler in internal/orders returns its errors through Problem.", "internal/orders/problem.go:1", "ORD-1"),
				verdict("c2", "pass", "docs/errors.md shows the shape with an example.", "docs/errors.md:1", "ORD-1"),
			}}),
			result(0.18, 2300),
		)),
		laneSigningKeys: map[string]any{
			"turns": [][]step{
				turn(
					context(14500),
					bash("git log --oneline -3"),
					step{"mcp": "ask", "arguments": map[string]any{"text": questionSigningKeys, "categories": []string{"security"}, "blocking": true}},
					result(0.11, 1400),
				),
				{
					{"mcp": "lane_handoff", "arguments": map[string]any{
						"done":              []string{"Read how tokens are signed and verified today"},
						"in_progress":       []string{"Verifying against a list of active keys"},
						"next_steps":        []string{"Read the answer about the old key's grace period, then set the verifier's window"},
						"risks":             []string{"A grace period that is too short signs people out at the rotation"},
						"carries_next_step": false,
					}},
					result(0.12, 600),
				},
			},
			"resume_turns": [][]step{turn(report("completed", map[string]any{"artifacts": []string{"branch"}}), result(0.2, 900))},
		},
		laneRateLimit: script(turn(
			context(31500),
			bash("git log --oneline -3"),
			commit("internal/orders/limit.go", "Add a token bucket per client ID",
				"package orders", "", "// bucket holds the requests of one client ID in the current minute.", "type bucket struct{ tokens int }"),
			bash("grep -rn 'bucket' internal"),
			step{"hold": true},
		)),
		laneRequestIDs: script(turn(
			context(22300),
			commit("internal/orders/requestid.go", "Assign a request ID in the router",
				"package orders", "", "// requestIDHeader carries a request ID between services.", "const requestIDHeader = \"X-Request-ID\""),
			step{"tool": "Bash", "command": "git push origin lane/lane-request-ids", "prompts": true},
			step{"hold": true},
		)),
		reviewTimeouts: script(turn(
			context(18700),
			bash("git log --oneline -5"),
			report("completed", map[string]any{"verdicts": []any{
				verdict("c1", "pass", "Both clients in internal/orders/client.go take their deadline from the request context.", "internal/orders/client.go:1", "ORD-3"),
			}}),
			result(0.16, 2100),
		)),
		reviewRetries: script(turn(
			context(24600),
			bash("git log --oneline -5"),
			report("completed", map[string]any{"verdicts": []any{
				verdict("c1", "pass", "The retry stops after the third attempt, and retry_test.go covers the budget.", "internal/orders/retry.go:1", "ORD-3"),
				verdict("c2", "pass", "Each attempt sends the Idempotency-Key the first one carried.", "internal/orders/retry_test.go:1", "ORD-3"),
			}}),
			result(0.21, 2600),
		)),
		orchestrator: script(turn(
			step{"mcp": "question_next", "arguments": map[string]any{"project": sampleProject}},
			step{"mcp": "decision_record", "arguments": map[string]any{
				"project": sampleProject, "question": qRetryKey, "status": "decided", "statement": "A retry reuses the original request's idempotency key.",
				"rationale": "A new key would let fulfillment apply the order twice.", "scope": "retries", "reversibility": "reversible",
				"citations": []string{"requirement:ORD-3"}, "lookup": []string{}, "open": []string{},
			}},
			step{"mcp": "decision_record", "arguments": map[string]any{
				"project": sampleProject, "question": qLogLevel, "status": "assumed", "statement": "Log a rate limit rejection at info level for now.",
				"rationale": "A rejection is expected traffic, and a warning would page the on-call engineer.", "scope": "logging",
				"reversibility": "reversible", "citations": []string{"docs/runbook.md"}, "lookup": []string{}, "open": []string{},
			}},
			step{"mcp": "question_escalate", "arguments": map[string]any{
				"project": sampleProject, "question": qSigningKeys, "reason": "one_way", "options": []string{"Keep the old key valid for 24 hours", "Sign everyone out at the rotation"},
				"recommendation": "Keep the old key valid for 24 hours", "lookup": []string{}, "open": []string{},
			}},
			step{"mcp": "question_escalate", "arguments": map[string]any{
				"project": sampleProject, "question": qPartnerTier, "reason": "scope_change", "options": []string{"One limit for every client", "A higher tier for partners"},
				"recommendation": "A higher tier for partners", "lookup": []string{}, "open": []string{},
			}},
			result(0.09, 1800),
		)),
	}
}

// assistantTurns are the turns of the assistant's conversation: the session's opening prompt, which it
// answers with nothing to show, and then a reply to each of the member's messages.
func assistantTurns() [][]step {
	return [][]step{
		{{"init": true}, result(0.01, 20)},
		reply("msg-1", strings.Join([]string{
			"Resilient calls is two of four done. Two things hold the rest:",
			"- The signing key work is parked on your answer about the old key's grace period.",
			"- Per-client rate limits is still running, and waits on the partner tier question.",
			"The retry work passed its review and waits for you to accept it.",
		}, "\n")),
		reply("msg-2", "Yes. Accepting the retry work unblocks nothing else, so it can wait until the rate limit lane ends."),
	}
}

// planningTurns are the planning session's: it commits a draft contract on its branch, then keeps working,
// so its pane stays open for the capture. A pane's turn begins with the prompt it was typed.
func planningTurns() [][]step {
	return [][]step{{
		{"init": true},
		context(27400),
		bash("git log --oneline -5 -- plans"),
		commit("plans/partner-usage/contract.md", "Draft the partner usage contract",
			"+++", "kind = \"contract\"", "id = \"partner-usage\"", "title = \"Partner usage page\"", "",
			"[[criteria]]", "id = \"c1\"", "text = \"A partner sees its requests and rejections for the last 24 hours.\"", "",
			"[[work_orders]]", "id = \"partner-usage-work\"", "title = \"Build the partner usage page\"", "priority = 2", "size = \"medium\"",
			"+++", "", "Partners see their own usage and the limits of their tier."),
		{"hold": true},
	}}
}

// fixtureText is the whole script, with turns for every session no lane script names.
func fixtureText(t *testing.T, turns [][]step) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(map[string]any{
		"source": "the screenshot kit's sample project",
		"turns":  turns,
		"lanes":  lanes(),
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// writeFixture puts the sample's script beside the fake CLI, in place of the one it was installed with. A
// session reads it as it starts, so a change reaches only the sessions that start after it.
func (in *install) writeFixture(turns [][]step) {
	in.t.Helper()
	file := filepath.Join(doubles.BinDir(in.path), doubles.FixtureFile)
	// A rename, so a session that starts while it changes reads one whole script.
	next := file + ".next"
	if err := os.WriteFile(next, fixtureText(in.t, turns), 0o600); err != nil {
		in.t.Fatal(err)
	}
	if err := os.Rename(next, file); err != nil {
		in.t.Fatal(err)
	}
}
