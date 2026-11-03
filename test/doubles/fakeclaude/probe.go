package main

import (
	"os"
	"slices"
)

// probeMark is the variable the driver sets on a qualification probe's sessions.
const probeMark = "ROPESHIFT_PROBE"

// drops names the surfaces this run leaves out or changes, for a probe's session only, so each check
// of the qualification matrix can be seen to fail on a stand-in. Every other session keeps them all.
var drops []string

func dropped(surface string) bool { return slices.Contains(drops, surface) }

// isProbe reports a probe's session.
func isProbe() bool { return os.Getenv(probeMark) == "1" }

// probeScript is what a probe's session does, whatever the fixture's lanes do: each turn has an init,
// a rate limit notice with a window's figure, a `command_lifecycle`, and a result, as 2.1.316 showed
// them in the qualification matrix. A pane shows the trust dialog of a new directory and then the
// prompt. The fixture's `probe_drops` takes a surface away.
func probeScript(script fixture) fixture {
	drops = script.ProbeDrops
	info := map[string]any{"status": "allowed", "resetsAt": "$resets_at", "rateLimitType": "seven_day"}
	if !dropped("unified_windows") {
		info["unifiedWindows"] = map[string]any{"seven_day": map[string]any{"utilization": 0.25}}
	}
	turn := []step{{Init: true}}
	if !dropped("login") {
		turn = append(turn, step{Emit: map[string]any{"type": "rate_limit_event", "rate_limit_info": info}, ResetsInS: 3600})
	}
	if !dropped("command_lifecycle") {
		turn = append(turn, step{Emit: map[string]any{"type": "system", "subtype": "command_lifecycle", "state": "started", "command_uuid": "c"}})
	}
	turn = append(turn, step{Emit: map[string]any{
		"type": "result", "subtype": "success", "is_error": false, "terminal_reason": "completed", "num_turns": 1,
		"total_cost_usd": 0.001, "usage": map[string]any{"input_tokens": 10, "output_tokens": 1},
	}})
	script.Turns = [][]step{turn, turn, turn, turn}
	script.ResumeTurns, script.Lanes, script.Inbound, script.StopText = nil, nil, "", ""
	script.Pane = paneScript{Trust: true}
	if dropped("start_screens") {
		script.Pane = paneScript{Stuck: "A screen no qualification has seen\n"}
	}
	return script
}
