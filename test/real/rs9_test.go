//go:build realsession

package real

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/contextpacks"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/gittest"
)

const (
	rs9Lane = "rs9-lane"
	// rs9Charge covers a pane's brief with both tool calls.
	rs9Charge = 6
	// rs9Sessions are the first probe and one lane.
	rs9Sessions    = 2
	rs9TurnCeiling = rs9Charge + 1
	// rs9ToolLimit ends a pane that keeps calling tools past that charge.
	rs9ToolLimit = 4
	rs9Profile   = `run_mode = "tmux"

[agent]
model = "` + sessionModel + `"
permission_mode = "default"

[lifecycle]
ready_timeout = "60s"
max_restarts = 0
`
	rs9Contract = `+++
kind = "contract"
id = "rs9"
title = "Read a context pack"
requirements = ["RS9-1"]

[[criteria]]
id = "c1"
text = "The lane reports both words"

[[work_orders]]
id = "rs9-order"
title = "Read a context pack"
size = "small"
+++

This work order tests what a lane holds at its start, and it has no other work. Change no file,
read no file, and run no command. Follow these steps in order.

1. Your system prompt holds a context pack, and requirement RS9-1 in it gives the first word.
2. Call the requirement_get tool once, with key RS9-2. Its answer gives the second word.
3. Call the lane_report tool once, with outcome completed, and reason set to the two words with one space between them.
4. End your turn with the single word done.
`
)

func rs9Requirements(first, second string) string {
	return `+++
kind = "requirements"
id = "rs9-reqs"
title = "Requirements"

[[requirement]]
id = "RS9-1"
text = "The first word is ` + first + `."

[[requirement]]
id = "RS9-2"
text = "The second word is ` + second + `."
+++
`
}

func rs9Word(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return "hemp" + hex.EncodeToString(raw)
}

func standInPackReader(first, second string) []any {
	return []any{
		map[string]any{"init": true},
		map[string]any{"status": map[string]any{"context_window": map[string]any{"current_usage": map[string]any{"input_tokens": 12000}}}},
		map[string]any{"mcp": lanes.ToolRequirementGet, "arguments": map[string]any{"key": "RS9-2"}},
		map[string]any{"mcp": lanes.ToolLaneReport, "arguments": map[string]any{
			"outcome": "completed", "reason": first + " " + second, "artifacts": []string{"none"},
			"checks": []map[string]any{{"name": "stand-in", "passed": true}},
		}},
		standInResult(),
	}
}

// reportedReason reads what a session wrote as its reason, which this suite only compares.
func (s *suite) reportedReason(id string) string {
	s.t.Helper()
	reason := ""
	for _, event := range s.eventsOf(id) {
		var change workorders.Change
		if json.Unmarshal(event.Data, &change) == nil && change.Outcome != "" {
			reason = change.Reason
		}
	}
	return reason
}

// packRead reads what a stopped daemon left of the one pack this suite made.
func (s *suite) packRead() (contextpacks.Pack, ledger.Generation) {
	s.t.Helper()
	store, err := sqlite.Open(filepath.Join(s.paths.StateDir, api.LedgerFileName), sqlstore.Options{})
	if err != nil {
		s.t.Fatal(err)
	}
	defer store.Close()
	var pack contextpacks.Pack
	var generation ledger.Generation
	err = store.Read(s.t.Context(), rs6Scope, func(tx ports.ReadTx) error {
		packs, err := tx.Packs().All()
		if err != nil {
			return err
		}
		if len(packs) != 1 {
			s.t.Fatalf("%d packs", len(packs))
		}
		pack = packs[0]
		generations, err := tx.Lanes().Generations(rs9Lane)
		if err != nil {
			return err
		}
		if len(generations) != 1 {
			s.t.Fatalf("%d generations", len(generations))
		}
		generation = generations[0]
		return nil
	})
	if err != nil {
		s.t.Fatal(err)
	}
	return pack, generation
}

// TestRS9 holds a real interactive session to its pack. Only a real session can show that
// `--append-system-prompt-file` reaches an interactive lane: the first word is nowhere but in the
// pack, the second is nowhere a session can read but the MCP server, and neither is on any disk
// the session was told to read.
func TestRS9(t *testing.T) {
	s := start(t, "rs9")
	s.watched = []string{rs9Lane}
	s.spend.sessionCeiling, s.spend.turnCeiling = rs9Sessions, rs9TurnCeiling
	t.Cleanup(s.killTerminal)
	s.build()
	s.probe()

	first, second := rs9Word(t), rs9Word(t)
	repo := gittest.NewIn(t, s.scratch)
	repo.Commit(".ropeshift/profile.toml", rs9Profile)
	repo.Commit("requirements.md", rs9Requirements(first, second))
	repo.Commit("contract.md", rs9Contract)
	s.repo = repo.Dir
	s.worktree = filepath.Join(filepath.Dir(repo.Dir), filepath.Base(repo.Dir)+"-lanes", rs9Lane)
	if err := s.spend.charge("lane", rs9Charge); err != nil {
		t.Fatal(err)
	}

	s.playStandIn([]any{standInPackReader(first, second)}, nil)
	running := s.startDaemonWith(map[string]any{"args": paneGuardFlags, "env": paneEnv})
	t.Cleanup(running.stop)
	id := s.dispatch(running, rs9Lane)
	screens := &screenLog{}
	settled := map[workorders.State]bool{
		workorders.Acceptance: true, workorders.Blocked: true, workorders.Failed: true, workorders.Ready: true,
	}
	var row workorders.WorkOrder
	s.poll(running.stop, "the lane's outcome", func() bool {
		s.watchPane(running.stop, rs9Lane+"-g1", screens)
		if tools := eventCount(s.hookRecords(rs9Lane), "PreToolUse"); tools > rs9ToolLimit {
			s.stopWith(running.stop, "%d tool calls passed the limit of %d", tools, rs9ToolLimit)
		}
		row = s.workOrder(id)
		return settled[row.State]
	})
	left := s.settleProcesses(rs9Lane)
	running.stop()

	outcome, _ := s.outcomeOf(id)
	reason := s.reportedReason(id)
	tools := []string{}
	for _, record := range s.hookRecords(rs9Lane) {
		if record.Event == "PreToolUse" && !slices.Contains(tools, record.Tool) {
			tools = append(tools, record.Tool)
		}
	}
	slices.Sort(tools)
	pack, generation := s.packRead()
	onDisk, err := os.ReadFile(filepath.Join(s.worktree, lanes.PackFile))
	if err != nil {
		t.Fatal(err)
	}
	words := reason == first+" "+second
	t.Logf("rs9: state=%s outcome=%s words=%v tools=%v screens=%v processes_left=%d", row.State, outcome, words, tools, screens.names(), left)
	t.Logf("rs9: pack_estimate=%d baseline=%d generation_pack_version=%d same_bytes=%v", pack.TokensEstimate, generation.BaselineTokens, generation.PackVersion, string(onDisk) == pack.Text)
	if row.State != workorders.Acceptance || !words {
		t.Fatalf("the lane ended %s, and its report does not name both words", row.State)
	}
	want := []string{"mcp__ropeshift__lane_report", "mcp__ropeshift__requirement_get"}
	if !slices.Equal(tools, want) {
		t.Errorf("tools %v, want %v: a word read from a file shows nothing of the system prompt", tools, want)
	}
	if !strings.Contains(pack.Text, first) {
		t.Error("the first word is not in the pack")
	}
	if strings.Contains(pack.Text, second) {
		t.Error("the second word is in the pack, so a read of it shows nothing of the MCP server")
	}
	if string(onDisk) != pack.Text {
		t.Error("the worktree does not hold the ledger's bytes")
	}
	if generation.PackID != pack.ID || generation.PackVersion != pack.Version {
		t.Errorf("the generation names %s@%d", generation.PackID, generation.PackVersion)
	}
	if left != 0 {
		t.Error("a process of the session remains")
	}
}
