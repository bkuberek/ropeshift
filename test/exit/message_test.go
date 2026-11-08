package exit

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/messages"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
	"ropeshift.dev/ropeshift/test/doubles"
	"ropeshift.dev/ropeshift/test/gittest"
)

// restProfile holds a lane at rest for longer than a test takes, so a message finds it there.
const restProfile = "[lifecycle]\nask_wait = \"10m\"\nhandoff_timeout = \"10s\"\nmax_restarts = 0\n"

func (in *install) message(t *testing.T, id string) messages.Message {
	t.Helper()
	var out messages.Message
	if err := json.Unmarshal([]byte(in.must("message", "show", id, "--json")), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestAMessageReachesALaneAndItsReplyItsSender is the path on the real binary, the real driver, the
// real shim, the real hook, and the real MCP server: a member sends, a session at rest takes it into
// a turn, and the session's reply is what the sender reads.
func TestAMessageReachesALaneAndItsReplyItsSender(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", restProfile)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallFakeClaude(t, "lane-replies")
	first := in.start(parkFlags(repo.Dir)...)
	in.waitReady(first)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	in.must("work", "claim", id, "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", id, "--lane", "lane-1")
	doubles.ReapShims(t, filepath.Join(in.paths().StateDir, "lanes", "lane-1"))
	in.waitFor("a lane at rest", func() bool {
		code, out, _ := in.run("lane", "show", "lane-1", "--json")
		var view ledger.LaneView
		return code == 0 && json.Unmarshal([]byte(out), &view) == nil && view.ActualState == "idle"
	})

	in.must("message", "send", "lane-1", "--id", "m-cli", "--body", "Use two retries.", "--kind", "instruction")
	in.waitFor("the reply", func() bool { return in.message(t, "m-cli").State == messages.Replied })
	got := in.message(t, "m-cli")
	if got.Reply != "Read, and going on." || got.To.Lane != "lane-1" || got.To.Generation != 1 {
		t.Errorf("message %+v", got)
	}
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	first.kill()
}

// TestAMessageToALaneThatEndedTellsItsSender reads what a sender reads, with nothing running: a lane
// that ended has no session, and the reason says so.
func TestAMessageToALaneThatEndedTellsItsSender(t *testing.T) {
	in := newInstall(t)
	id, running := in.runLane("lane-asks")
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	in.must("message", "send", "lane-1", "--id", "m-late", "--body", "Too late.")
	got := in.message(t, "m-late")
	if got.State != messages.Expired || got.Reason != messages.ReasonGenerationEnded {
		t.Errorf("message %+v", got)
	}
	running.kill()
}
