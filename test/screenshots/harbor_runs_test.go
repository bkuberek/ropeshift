//go:build screenshots

package screenshots

// The lanes of guides 5 and 6, which run the approved plans of Harbor and Moorings. A person starts each from the
// web UI and names it in the Start dialog's Lane ID, the berths work's review included, so each plays a script of
// its own under that name. The orchestrator a guide starts plays its own script under the first run's lane, and
// claims and takes nothing: an orchestrator's own lanes get names it cannot choose, and a lane's question an ID it
// cannot know, so no script could play them.
//
// The lane on Harbor's berths work commits, asks to remove an old folder, which waits on a person's approval,
// asks a product question it can go on without, tests its change, and reports its work done. The review lane
// reads the change and passes the contract's one criterion. The lane on Moorings' cancelling work asks a question
// it cannot go on without and parks once moorings-api's `ask_wait` runs out, so Needs you has a parked lane to
// resume. Resumed, its next generation finishes the work.

// The names a guide types into Lane ID.
const (
	laneServeBerths   = "serve-berths"
	reviewServeBerths = "review-serve-berths"
	laneFinishCancel  = "finish-cancel"
)

// questionBerthOrder is the product question the berths lane asks, which waits for a person in Open questions.
const questionBerthOrder = "In what order should GET /berths list the berths?"

// questionCancelRule is the question the cancelling lane cannot go on without, which it parks on.
const questionCancelRule = "A booking holds no arrival date. May a skipper cancel up to a day before the booking starts?"

// mooringsAPIProfile is moorings-api's own profile layer: a lane that waits on a question parks a moment after
// it asks, and gives its handoff in a bounded time.
const mooringsAPIProfile = "[lifecycle]\nask_wait = \"2s\"\nhandoff_timeout = \"10s\"\n"

// harborOrchestratorTurns is how many wakes the orchestrator's script answers. Each wake is a turn, and a session
// past its last turn stays quiet, so the count is more than any walk wakes it.
const harborOrchestratorTurns = 200

// harborLanes is every scripted session of Harbor, by the lane each runs under.
func harborLanes() map[string]any {
	script := func(turns ...[]step) map[string]any { return map[string]any{"turns": turns} }
	quiet := make([][]step, harborOrchestratorTurns)
	for i := range quiet {
		quiet[i] = []step{{"init": true}, result(0.01, 20)}
	}
	return map[string]any{
		// The orchestrator ends each wake with nothing claimed, so the walk's lanes are the ones a person starts.
		orchestrator: script(quiet...),
		laneServeBerths: script([]step{
			context(28600),
			bash("git log --oneline -3"),
			commit("internal/berths/list.go", "List every berth with its state",
				"package berths", "", "// Berth is one berth of the port, and whether it is open.",
				"type Berth struct {", "\tNumber int", "\tName   string", "\tOpen   bool", "}"),
			bash("grep -rn 'Berth' internal"),
			step{"tool": "Bash", "command": "rm -rf internal/berths/legacy", "prompts": true},
			// Allowed, the lane works on a while before it asks, so the walk shows it in the conversation panel
			// with nothing waiting on a person. It still ends before the walk answers its question.
			step{"sleep_ms": 10000},
			laneQuestion(questionBerthOrder, false, "By berth number", "By name"),
			commit("internal/berths/list_test.go", "Test that GET /berths lists every berth",
				"package berths", "", "import \"testing\"", "", "func TestListHasEveryBerth(t *testing.T) {}"),
			report("completed", map[string]any{"artifacts": []string{"branch"}, "checks": []any{check("go test ./internal/berths", "c1")}}),
			result(0.41, 5200),
		}),
		reviewServeBerths: script([]step{
			context(19400),
			bash("git log --oneline -5"),
			report("completed", map[string]any{"verdicts": []any{
				verdict("c1", "pass", "GET /berths returns every berth with whether it is open, and list_test.go covers the list.",
					"internal/berths/list.go:1", "HAR-1"),
			}}),
			result(0.19, 2400),
		}),
		// The first turn asks and comes to rest on the question, and the second gives the handoff parking asks for.
		// The resumed session, which a person's Resume starts, finishes the work without the answer.
		laneFinishCancel: map[string]any{
			"turns": [][]step{
				{
					context(17800),
					bash("git log --oneline -3"),
					bash("grep -rn 'Cancel' internal"),
					laneQuestion(questionCancelRule, true, "Yes, up to a day before the booking starts", "No, wait for the arrival date"),
					result(0.22, 2900),
				},
				{
					{"mcp": "lane_handoff", "arguments": map[string]any{
						"done":              []string{"Read how a booking is stored and what Cancel does today"},
						"in_progress":       []string{"The rule for how late a skipper may cancel"},
						"next_steps":        []string{"Read the answer about the cancel rule, then check it in Cancel"},
						"risks":             []string{"A booking holds no arrival date, so the rule has nothing to count from"},
						"carries_next_step": false,
					}},
					result(0.06, 600),
				},
			},
			"resume_turns": [][]step{{
				context(18900),
				commit("internal/bookings/cancel.go", "Free a cancelled mooring up to a day before the booking starts",
					"package bookings", "", "// Cancel frees a booked mooring, up to a day before the booking starts.", "func Cancel() {}"),
				report("completed", map[string]any{"artifacts": []string{"branch"}}),
				result(0.17, 2100),
			}},
		},
	}
}

// laneQuestion asks one product question. It waits on the orchestrator's ladder, where the guide's person answers
// it in Open questions, because the orchestrator here takes no question.
func laneQuestion(text string, blocking bool, options ...string) step {
	return step{"mcp": "ask", "arguments": map[string]any{
		"text": text, "categories": []string{"product"}, "blocking": blocking, "options": options,
	}}
}
