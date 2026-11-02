package worker_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/lanes"
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
}

func aLaneOnRealFiles(t *testing.T) *fileRig {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile := fmt.Sprintf("[worktree]\nroot = %q\n\n[lifecycle]\napproval_wait = \"5m\"\nhandoff_timeout = \"30ms\"\n", root)
	r, _ := aLaneThatWaitsUnder(t, profile)
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	f := &fileRig{laneRig: r, worktree: record.Worktree, outside: filepath.Join(root, "outside")}
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
	type attack struct {
		name   string
		file   func(f *fileRig) string
		before func(t *testing.T, f *fileRig)
		move   func(t *testing.T, f *fileRig)
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
				rows := f.requests(t)
				if len(rows) != 2 || rows[0].ID != first.ID || rows[0].State != authority.ApprovalVoid || rows[1].State != authority.ApprovalPending {
					t.Fatalf("requests %+v", rows)
				}
				if rows[1].SubjectDigest == first.SubjectDigest || rows[1].Path == first.Path {
					t.Errorf("the second request binds to what the first did: %+v", rows[1])
				}
				if reply := f.orchestratorSays(t, rows[1]); reply.OK || reply.Code != lanes.CodeForbidden {
					t.Errorf("a class reached where the call lands now: %+v", reply)
				}
			})
		}
	}
}

// A hook already waiting on a request reads the same way: the next look finds the call lands
// elsewhere, and it goes no further than that look.
func TestAWaitingHookSeesACallMove(t *testing.T) {
	f := aLaneOnRealFiles(t)
	input := anEdit(t, f.path("docs", "a.md"))
	f.lookAtEdit(t, input, "call-1", false)
	f.memberSays(t, f.onlyRequest(t), true)
	mustDo(t, os.Remove(f.path("docs", "a.md")))
	mustDo(t, os.Symlink(filepath.Join(f.outside, "a.md"), f.path("docs", "a.md")))
	if got := decisionIn(t, f.lookAtEdit(t, input, "call-1", true)); got != lanes.ApprovalDeny {
		t.Fatalf("decision %s", got)
	}
	if rows := f.requests(t); rows[0].State != authority.ApprovalVoid {
		t.Errorf("requests %+v", rows)
	}
}

// Nothing moved, so what was decided is what runs. A link put back before the use leaves the same
// landing, and that runs too: a decision reads where a call lands, not what happened on the way.
func TestACallThatLandsWhereItWasDecidedRuns(t *testing.T) {
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
	if rows := f.requests(t); rows[0].State != authority.ApprovalVoid {
		t.Errorf("requests %+v", rows)
	}
}

// A hook knows no worktree, so what it forwards of a tool's end is a digest of the input alone. That
// must still find a request bound to where the call lands, or a person's answer at a terminal stays
// open for good.
func TestATerminalAnswerFindsACallWithAPath(t *testing.T) {
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
