package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	domainlanes "ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// parkedAtItsLimit parks a work order's lane on the wake a hard spend limit leaves, with $30 charged against the $25
// limit, the way the worker's reports leave it.
func (s *session) parkedAtItsLimit(id string) {
	s.t.Helper()
	lane := apitest.NewWorld(s.t, s.daemon).Running(id, "Add the retry budget")
	lane.Upload(ledger.UploadGenerationUsage, ledger.GenerationUsage{CostUSD: 30, KnownTurns: 3})
	lane.Park(domainlanes.SpendLimitWake(id))
}

func (s *session) state(id string) workorders.State {
	s.t.Helper()
	var order workorders.WorkOrder
	if err := json.Unmarshal([]byte(s.must("work", "show", id, "--json")), &order); err != nil {
		s.t.Fatal(err)
	}
	return order.State
}

// `spend block` blocks the work order a hard limit parked, with the reason `spend_limit`, and prints the state it left
// and the reason, as --json does. The work order then reads as blocked work does.
func TestSpendBlockBlocksTheWorkOrderAHardLimitParked(t *testing.T) {
	s := newSession(t)
	s.parkedAtItsLimit("wo")
	if got := s.state("wo"); got != workorders.Parked {
		t.Fatalf("the work order reads %s before the block", got)
	}
	text := s.must("spend", "block", "wo")
	for _, want := range []string{"work order wo", "blocked\treason spend_limit"} {
		if !strings.Contains(text, want) {
			t.Errorf("spend block leaves out %q:\n%s", want, text)
		}
	}
	if got := s.state("wo"); got != workorders.Blocked {
		t.Errorf("the work order reads %s after the block", got)
	}
	code, _, stderr := s.run("spend", "block", "wo")
	if code != cli.ExitUsage || !strings.Contains(stderr, "a hard spend limit parked") {
		t.Errorf("a second block exits %d with %q", code, stderr)
	}
	if code, _, _ := s.run("spend", "block", "no-such-work-order"); code != cli.ExitNotFound {
		t.Errorf("a block of work nobody made exits %d, want %d", code, cli.ExitNotFound)
	}
	if code, _, _ := s.run("spend", "block"); code != cli.ExitUsage {
		t.Errorf("a block that names no work order exits %d", code)
	}
}

// `spend replan` sends the work order a hard limit parked to planning, and says so with --json too.
func TestSpendReplanSendsTheWorkOrderToPlanning(t *testing.T) {
	s := newSession(t)
	s.parkedAtItsLimit("wo")
	var ended api.SpendStopEnded
	if err := json.Unmarshal([]byte(s.must("spend", "replan", "wo", "--json")), &ended); err != nil ||
		ended.WorkOrder != "wo" || ended.State != string(workorders.Planning) || ended.Reason != workorders.ReasonSpendLimit {
		t.Errorf("spend replan --json reads %+v: %v", ended, err)
	}
	if got := s.state("wo"); got != workorders.Planning {
		t.Errorf("the work order reads %s after the replan", got)
	}
	if code, _, _ := s.run("spend", "replan", "wo"); code != cli.ExitUsage {
		t.Errorf("a second replan exits %d, want %d", code, cli.ExitUsage)
	}
}
