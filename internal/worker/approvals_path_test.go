package worker_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

const (
	orchestratorToken = "orchestrator-token"
	editTool          = "Edit"
)

// fileRig is a lane whose worktree exists on disk, with a class that lets an orchestrator decide an
// edit under `docs/`, a file the class does not cover, and a place outside the worktree.
type fileRig struct {
	*laneRig
	worktree, outside string
	// turn is the gate that holds the lane's first turn open.
	turn *gate
}

func aLaneOnRealFiles(t *testing.T) *fileRig {
	t.Helper()
	return aLaneOnRealFilesThen(t)
}

// aLaneOnRealFilesThen is a lane on real files whose later turns play these steps.
func aLaneOnRealFilesThen(t *testing.T, next ...[]fake.DriverStep) *fileRig {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile := fmt.Sprintf("[worktree]\nroot = %q\n\n[lifecycle]\napproval_wait = \"5m\"\nhandoff_timeout = \"30ms\"\n", root)
	r, g := aLaneThatWaitsUnder(t, profile, next...)
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	f := &fileRig{laneRig: r, worktree: record.Worktree, outside: filepath.Join(root, "outside"), turn: g}
	for _, dir := range []string{"docs", "src"} {
		if err := os.MkdirAll(filepath.Join(f.worktree, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(f.outside, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{filepath.Join(f.worktree, "docs", "a.md"), filepath.Join(f.worktree, "src", "main.go"), filepath.Join(f.outside, "a.md")} {
		if err := os.WriteFile(file, []byte("text"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	err = r.journal.SaveLane(t.Context(), lanes.Record{ID: "orchestrator", State: lanes.StateRunning, Owner: lanes.OwnerOrchestrator, TokenDigest: lanes.TokenDigest(orchestratorToken), Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	body := "[delegation_policy]\nversion = 2\n\n[[delegation_policy.classes]]\ntool = \"Edit\"\npath = \"docs/**\"\n"
	policy := authority.DelegationPolicy{Version: 2, Classes: []authority.DelegationClass{{Tool: editTool, Path: "docs/**"}}}
	if _, err := control.NewApprovals(r.store).PublishDelegation(t.Context(), scope, authority.DelegationDraft{Body: body, Policy: policy}, member); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fileRig) path(elements ...string) string {
	return filepath.Join(append([]string{f.worktree}, elements...)...)
}

func anEdit(t *testing.T, file string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"file_path": file, "old_string": "text", "new_string": "other"})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func (f *fileRig) lookAtEdit(t *testing.T, input, call string, raised bool) lanes.Reply {
	t.Helper()
	return f.lookAtTool(t, editTool, input, call, raised)
}

// orchestratorSays decides through the socket an orchestrator's session reaches.
func (f *fileRig) orchestratorSays(t *testing.T, a authority.Approval) lanes.Reply {
	t.Helper()
	line, err := json.Marshal(lanes.Request{
		Version: lanes.SocketProtocol, Token: orchestratorToken, Decide: &lanes.DecideRequest{ID: a.ID, Digest: a.SubjectDigest, Allow: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return f.subject.AnswerLane(t.Context(), line)
}

// twoRequests reads the request named and the one after it, which a clock's millisecond can list
// in either order.
func (f *fileRig) twoRequests(t *testing.T, first string) (named, other authority.Approval) {
	t.Helper()
	rows := f.requests(t)
	if len(rows) != 2 {
		t.Fatalf("requests %+v", rows)
	}
	if rows[0].ID == first {
		return rows[0], rows[1]
	}
	return rows[1], rows[0]
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// A decision binds to where a call lands, and that is read again at every look. Each of these keeps
// a call's input to the byte and moves where it lands after the decision, which is the one thing a
// digest of the input alone cannot see. The call must not run, its decision is void, and what it
// asks next goes to a member, because no class reaches where it lands now.
func TestADecisionBindsToWhereACallLands(t *testing.T) {
	t.Parallel()
	type attack struct {
		name string
		file func(f *fileRig) string
		move func(t *testing.T, f *fileRig)
	}
	existing := func(f *fileRig) string { return f.path("docs", "a.md") }
	attacks := []attack{
		{
			name: "a file swapped for a link out of the worktree", file: existing,
			move: func(t *testing.T, f *fileRig) {
				mustDo(t, os.Remove(f.path("docs", "a.md")))
				mustDo(t, os.Symlink(filepath.Join(f.outside, "a.md"), f.path("docs", "a.md")))
			},
		},
		{
			name: "a directory swapped for a link out of the worktree", file: existing,
			move: func(t *testing.T, f *fileRig) {
				mustDo(t, os.Rename(f.path("docs"), f.path("docs-old")))
				mustDo(t, os.Symlink(f.outside, f.path("docs")))
			},
		},
		{
			name: "a file swapped for a link past the class, inside the worktree", file: existing,
			move: func(t *testing.T, f *fileRig) {
				mustDo(t, os.Remove(f.path("docs", "a.md")))
				mustDo(t, os.Symlink(filepath.Join("..", "src", "main.go"), f.path("docs", "a.md")))
			},
		},
		{
			name: "a file swapped for a hard link to one the class does not cover", file: existing,
			move: func(t *testing.T, f *fileRig) {
				mustDo(t, os.Remove(f.path("docs", "a.md")))
				mustDo(t, os.Link(f.path("src", "main.go"), f.path("docs", "a.md")))
			},
		},
		{
			name: "a new file's path taken by a link to nothing yet",
			file: func(f *fileRig) string { return f.path("docs", "new.md") },
			move: func(t *testing.T, f *fileRig) {
				mustDo(t, os.Symlink(filepath.Join(f.outside, "new.md"), f.path("docs", "new.md")))
			},
		},
		{
			name: "a missing directory made a link",
			file: func(f *fileRig) string { return f.path("docs", "sub", "new.md") },
			move: func(t *testing.T, f *fileRig) {
				mustDo(t, os.Symlink(f.outside, f.path("docs", "sub")))
			},
		},
	}
	deciders := map[string]func(t *testing.T, f *fileRig, a authority.Approval){
		"an orchestrator": func(t *testing.T, f *fileRig, a authority.Approval) {
			if reply := f.orchestratorSays(t, a); !reply.OK {
				t.Fatalf("the class did not reach the call: %+v", reply)
			}
		},
		"a member": func(t *testing.T, f *fileRig, a authority.Approval) { f.memberSays(t, a, true) },
	}
	for _, tt := range attacks {
		for who, decide := range deciders {
			t.Run(tt.name+", decided by "+who, func(t *testing.T) {
				f := aLaneOnRealFiles(t)
				input := anEdit(t, tt.file(f))
				if got := decisionIn(t, f.lookAtEdit(t, input, "call-1", false)); got != lanes.ApprovalWaiting {
					t.Fatalf("decision %s", got)
				}
				first := f.onlyRequest(t)
				decide(t, f, first)
				tt.move(t, f)
				if got := decisionIn(t, f.lookAtEdit(t, input, "call-2", false)); got == lanes.ApprovalAllow {
					t.Fatal("the call ran where nobody decided it would land")
				}
				voided, second := f.twoRequests(t, first.ID)
				if voided.State != authority.ApprovalVoid || second.State != authority.ApprovalPending {
					t.Fatalf("first %+v, second %+v", voided, second)
				}
				if second.SubjectDigest == first.SubjectDigest || second.Path == first.Path {
					t.Errorf("the second request binds to what the first did: %+v", second)
				}
				if reply := f.orchestratorSays(t, second); reply.OK || reply.Code != lanes.CodeForbidden {
					t.Errorf("a class reached where the call lands now: %+v", reply)
				}
			})
		}
	}
}

// A hook already waiting on a request reads the same way: the next look finds the call lands
// elsewhere, and it goes no further than that look.
func TestAWaitingHookSeesACallMove(t *testing.T) {
	t.Parallel()
	f := aLaneOnRealFiles(t)
	input := anEdit(t, f.path("docs", "a.md"))
	f.lookAtEdit(t, input, "call-1", false)
	f.memberSays(t, f.onlyRequest(t), true)
	mustDo(t, os.Remove(f.path("docs", "a.md")))
	mustDo(t, os.Symlink(filepath.Join(f.outside, "a.md"), f.path("docs", "a.md")))
	if got := decisionIn(t, f.lookAtEdit(t, input, "call-1", true)); got != lanes.ApprovalDeny {
		t.Fatalf("decision %s", got)
	}
	if row := f.onlyRequest(t); row.State != authority.ApprovalVoid {
		t.Errorf("request %+v", row)
	}
}

// swapForALink takes the place of docs/a.md with a link out of the worktree, which moves where a call
// to it lands and leaves its input as it was.
func (f *fileRig) swapForALink(t *testing.T) {
	t.Helper()
	mustDo(t, os.Remove(f.path("docs", "a.md")))
	mustDo(t, os.Symlink(filepath.Join(f.outside, "a.md"), f.path("docs", "a.md")))
}

// firstLookHeld is a hook's first look at a call, and the ID of the request it was given to hold.
func (f *fileRig) firstLookHeld(t *testing.T, input string) string {
	t.Helper()
	first := f.lookAtEdit(t, input, "call-1", false)
	if got := decisionIn(t, first); got != lanes.ApprovalWaiting {
		t.Fatalf("decision %s", got)
	}
	return first.Approval.ID
}

// decide is a member's decision, which a closed request refuses.
func (f *fileRig) decide(t *testing.T, a authority.Approval, allow bool) error {
	t.Helper()
	_, err := control.NewApprovals(f.store).Decide(t.Context(), scope, authority.Decision{ID: a.ID, Digest: a.SubjectDigest, Allow: allow}, member)
	return err
}

func (f *fileRig) openRows(t *testing.T) []lanes.OpenApproval {
	t.Helper()
	rows, err := f.journal.OpenApprovals(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// A hook that looks again after its call moved, with nobody having decided, is told the request is
// closed, and the request is closed for everyone else too. One left pending could be approved by a
// member who reads a call that no longer exists, and a turn's end would record it as a person's answer
// at a terminal, which ropeshift's own hook never asked for.
func TestAWaitingHookSeesACallMoveBeforeADecision(t *testing.T) {
	t.Parallel()
	f := aLaneOnRealFiles(t)
	input := anEdit(t, f.path("docs", "a.md"))
	held := f.firstLookHeld(t, input)
	f.swapForALink(t)
	reply := f.lookHolding(t, editTool, input, "call-1", true, held)
	if got := decisionIn(t, reply); got != lanes.ApprovalDeny || reply.Approval.Reason != lanes.ReasonRequestClosed {
		t.Fatalf("reply %+v", reply.Approval)
	}
	row := f.onlyRequest(t)
	if row.State != authority.ApprovalVoid || row.Channel != "" || row.Reason == "" {
		t.Errorf("request %+v", row)
	}
	if err := f.decide(t, row, true); !errors.Is(err, authority.ErrApprovalClosed) {
		t.Errorf("a member's approval of a request whose call moved: %v", err)
	}
	if open := f.openRows(t); len(open) != 0 {
		t.Errorf("the worker still follows %+v", open)
	}
	if after := f.onlyRequest(t); after.State != authority.ApprovalVoid {
		t.Errorf("request %+v", after)
	}
}

// A member's approval does not keep a request alive when the call moves after it. The hook that holds
// the request closes it as void on its next look, and what was approved can no longer be spent.
func TestAWaitingHookSeesACallMoveAfterAnApproval(t *testing.T) {
	t.Parallel()
	f := aLaneOnRealFiles(t)
	input := anEdit(t, f.path("docs", "a.md"))
	held := f.firstLookHeld(t, input)
	f.memberSays(t, f.onlyRequest(t), true)
	f.swapForALink(t)
	reply := f.lookHolding(t, editTool, input, "call-1", true, held)
	if got := decisionIn(t, reply); got != lanes.ApprovalDeny || reply.Approval.Reason != lanes.ReasonRequestClosed {
		t.Fatalf("reply %+v", reply.Approval)
	}
	if row := f.onlyRequest(t); row.State != authority.ApprovalVoid || row.DecidedBy == "" {
		t.Errorf("request %+v", row)
	}
	if open := f.openRows(t); len(open) != 0 {
		t.Errorf("the worker still follows %+v", open)
	}
}

// A look that names a request closes it only when the call has moved from what that request says. A
// call that lands where it did, a request the worker does not follow, and no request at all leave
// every request as it was.
func TestALookThatSeesNoMoveClosesNothing(t *testing.T) {
	t.Parallel()
	for name, held := range map[string]func(string) string{
		"the request the hook holds":        func(id string) string { return id },
		"a request that is not this lane's": func(string) string { return "somebody-elses" },
	} {
		t.Run(name, func(t *testing.T) {
			f := aLaneOnRealFiles(t)
			input := anEdit(t, f.path("docs", "a.md"))
			id := f.firstLookHeld(t, input)
			reply := f.lookHolding(t, editTool, input, "call-1", true, held(id))
			if got := decisionIn(t, reply); got != lanes.ApprovalWaiting || reply.Approval.ID != id {
				t.Fatalf("reply %+v", reply.Approval)
			}
			if row := f.onlyRequest(t); row.State != authority.ApprovalPending || row.ID != id {
				t.Errorf("request %+v", row)
			}
			if open := f.openRows(t); len(open) != 1 || open[0].ID != id {
				t.Errorf("the worker follows %+v", open)
			}
		})
	}
}

// Closing a request that moved is not a person answering at a terminal. The turn ends with the hook
// gone and nothing left open, so the end records nothing, where a request left pending is marked
// answered on the terminal channel by it.
func TestACallItsHookDeniedAsMovedIsNeverAnsweredAtATerminal(t *testing.T) {
	t.Parallel()
	f := aLaneOnRealFilesThen(t, []fake.DriverStep{{Hold: true}})
	input := anEdit(t, f.path("docs", "a.md"))
	held := f.firstLookHeld(t, input)
	f.swapForALink(t)
	if got := decisionIn(t, f.lookHolding(t, editTool, input, "call-1", true, held)); got != lanes.ApprovalDeny {
		t.Fatalf("decision %s", got)
	}
	f.eventually("the turn again", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	f.turn.release <- struct{}{}
	f.awaitNudge(t)
	if row := f.onlyRequest(t); row.State != authority.ApprovalVoid || row.Channel == authority.ChannelTerminal {
		t.Errorf("request %+v", row)
	}
}

// awaitNudge waits for a lane to be told to go on, which comes after its turn's end was read.
func (r *laneRig) awaitNudge(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if len(r.driver.Sent()) != 0 {
			return
		}
	}
	t.Fatal("the lane was never told to go on")
}

// A lane parks on its request once its wait runs out, and a hook that is still looking finds the call
// moved by then. The turn that ends under a parking lane leaves its requests open on purpose, so the
// request must be closed at the look, or nothing would close it before a member approved it.
func TestACallThatMovesAsTheLaneParksVoidsItsRequest(t *testing.T) {
	t.Parallel()
	f := aLaneOnRealFiles(t)
	input := anEdit(t, f.path("docs", "a.md"))
	held := f.firstLookHeld(t, input)
	request := f.onlyRequest(t)
	f.clock.Advance(6 * time.Minute)
	if reply := f.lookHolding(t, editTool, input, "call-1", true, held); decisionIn(t, reply) != lanes.ApprovalDeny || reply.Approval.Reason != lanes.ReasonApprovalWait {
		t.Fatalf("reply %+v", reply.Approval)
	}
	f.eventually("parking", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Parking) })
	f.swapForALink(t)
	reply := f.lookHolding(t, editTool, input, "call-1", true, held)
	if got := decisionIn(t, reply); got != lanes.ApprovalDeny || reply.Approval.Reason != lanes.ReasonRequestClosed {
		t.Fatalf("reply %+v", reply.Approval)
	}
	if row := f.onlyRequest(t); row.ID != request.ID || row.State != authority.ApprovalVoid {
		t.Errorf("request %+v", row)
	}
	if open := f.openRows(t); len(open) != 0 {
		t.Errorf("the worker still follows %+v", open)
	}
}

// Nothing moved, so what was decided is what runs. A link put back before the use leaves the same
// landing, and that runs too: a decision reads where a call lands, not what happened on the way.
func TestACallThatLandsWhereItWasDecidedRuns(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		move func(t *testing.T, f *fileRig)
	}{
		{name: "nothing moved", move: func(*testing.T, *fileRig) {}},
		{name: "a file changed under the same name", move: func(t *testing.T, f *fileRig) {
			mustDo(t, os.WriteFile(f.path("docs", "a.md"), []byte("newer text"), 0o600))
		}},
		{name: "a link made and taken away", move: func(t *testing.T, f *fileRig) {
			mustDo(t, os.Rename(f.path("docs", "a.md"), f.path("docs", "kept")))
			mustDo(t, os.Symlink(filepath.Join(f.outside, "a.md"), f.path("docs", "a.md")))
			mustDo(t, os.Remove(f.path("docs", "a.md")))
			mustDo(t, os.Rename(f.path("docs", "kept"), f.path("docs", "a.md")))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := aLaneOnRealFiles(t)
			input := anEdit(t, f.path("docs", "a.md"))
			f.lookAtEdit(t, input, "call-1", false)
			if reply := f.orchestratorSays(t, f.onlyRequest(t)); !reply.OK {
				t.Fatalf("reply %+v", reply)
			}
			tt.move(t, f)
			if got := decisionIn(t, f.lookAtEdit(t, input, "call-1", true)); got != lanes.ApprovalAllow {
				t.Fatalf("decision %s", got)
			}
			if a := f.onlyRequest(t); a.State != authority.ApprovalUsed {
				t.Errorf("request %+v", a)
			}
		})
	}
}

// A call that already lands outside a class reads that way from the start, in what a reviewer reads
// and in what its digest covers, so a link moved from one place outside to another voids a member's
// decision as well.
func TestACallThatLandsOutsideSaysSoFromTheStart(t *testing.T) {
	t.Parallel()
	f := aLaneOnRealFiles(t)
	mustDo(t, os.Remove(f.path("docs", "a.md")))
	mustDo(t, os.Symlink(filepath.Join(f.outside, "a.md"), f.path("docs", "a.md")))
	input := anEdit(t, f.path("docs", "a.md"))
	if got := decisionIn(t, f.lookAtEdit(t, input, "call-1", false)); got != lanes.ApprovalWaiting {
		t.Fatalf("decision %s", got)
	}
	a := f.onlyRequest(t)
	want := filepath.Join(f.outside, "a.md")
	if a.Path != want || !strings.Contains(a.Summary, want) {
		t.Fatalf("a reviewer reads %q, and a class %q", a.Summary, a.Path)
	}
	if reply := f.orchestratorSays(t, a); reply.OK || reply.Code != lanes.CodeForbidden {
		t.Fatalf("a class reached a call that lands outside it: %+v", reply)
	}
	f.memberSays(t, a, true)
	mustDo(t, os.Remove(f.path("docs", "a.md")))
	mustDo(t, os.Symlink(filepath.Join(f.outside, "b.md"), f.path("docs", "a.md")))
	if got := decisionIn(t, f.lookAtEdit(t, input, "call-2", false)); got == lanes.ApprovalAllow {
		t.Fatal("a member's decision followed a link somewhere else")
	}
	if voided, _ := f.twoRequests(t, a.ID); voided.State != authority.ApprovalVoid {
		t.Errorf("request %+v", voided)
	}
}

// A hook knows no worktree, so what it forwards of a tool's end is a digest of the input alone. That
// must still find a request bound to where the call lands, or a person's answer at a terminal stays
// open for good.
func TestATerminalAnswerFindsACallWithAPath(t *testing.T) {
	t.Parallel()
	f := aLaneOnRealFiles(t)
	input := anEdit(t, f.path("docs", "a.md"))
	f.lookAtEdit(t, input, "call-1", false)
	seen, err := authority.ReadToolCall(editTool, []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	f.hook(t, lanes.HookEvent{Event: "PostToolUse", Subject: "toolu_1", Tool: editTool, Digest: seen.Digest, AtMs: 1})
	if a := f.onlyRequest(t); a.State != authority.ApprovalAnswered || a.Channel != authority.ChannelTerminal {
		t.Errorf("request %+v", a)
	}
}
