package worker_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
)

const otherLanesToken = "another-lanes-token"

func sealedLine(t *testing.T, token string, event lanes.HookEvent) []byte {
	t.Helper()
	line, err := lanes.SealHook(token, event)
	if err != nil {
		t.Fatal(err)
	}
	return line
}

// forgedLines are what a process that holds no token of this lane can write to a spool it can reach:
// each names the lane, and each would move it if the worker took the line at its word.
func forgedLines(t *testing.T, r *laneRig, a authority.Approval) map[string][]byte {
	t.Helper()
	bare := func(event lanes.HookEvent) []byte {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	closes := lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: "PostToolUse", Subject: "s", Digest: a.SubjectDigest, AtMs: 1}
	clears := lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: "SessionStart", Subject: "s", SessionID: "forged", Detail: lanes.SourceClear, AtMs: 1}
	notes := lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: "Notification", Subject: "s", Detail: "anything", AtMs: 1}
	changed := bytes.Replace(sealedLine(t, r.token(t), notes), []byte(`"Notification"`), []byte(`"PostToolUse"`), 1)
	changed = bytes.Replace(changed, []byte(`"anything"`), []byte(`"`+a.SubjectDigest+`"`), 1)
	return map[string][]byte{
		"a tool's end with no seal":           bare(closes),
		"a clear with no seal":                bare(clears),
		"a tool's end sealed by another lane": sealedLine(t, otherLanesToken, closes),
		"a clear sealed by another lane":      sealedLine(t, otherLanesToken, clears),
		"a line changed after its seal":       changed,
	}
}

// The token says which lane is calling on the socket. A spool has no socket to prove that on, so a
// line that proves nothing says nothing: a lane that can write a file must not be able to end
// another lane's request, or give it a generation, by naming it.
func TestALineInTheSpoolCannotSpeakForALane(t *testing.T) {
	t.Parallel()
	r, _ := aLaneThatWaits(t)
	r.look(t, gatedCall, "call-1", false)
	a := r.onlyRequest(t)
	if err := r.journal.SaveLane(t.Context(), lanes.Record{ID: "other", State: lanes.StateRunning, TokenDigest: lanes.TokenDigest(otherLanesToken), Generation: 1}); err != nil {
		t.Fatal(err)
	}
	for name, line := range forgedLines(t, r, a) {
		t.Run(name, func(t *testing.T) {
			r.spoolLine(t, line)
			if err := r.subject.DrainSpool(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := r.onlyRequest(t); got.State != authority.ApprovalPending {
				t.Errorf("request %+v", got)
			}
			record, err := r.journal.Lane(t.Context(), "lane-1")
			if err != nil {
				t.Fatal(err)
			}
			if record.Generation != 1 {
				t.Errorf("the lane reads generation %d", record.Generation)
			}
			hooks, err := r.journal.HookEvents(t.Context(), "lane-1")
			if err != nil {
				t.Fatal(err)
			}
			for _, hook := range hooks {
				if hook.Event == "PostToolUse" || hook.Event == "SessionStart" {
					t.Errorf("the journal took %+v", hook)
				}
			}
		})
	}
}

// A line goes the way of the token that sealed it: a process that ended has no token left to speak
// with, which is the same answer the socket gives it.
func TestASealOutlivesNoToken(t *testing.T) {
	t.Parallel()
	r, _ := aLaneThatWaits(t)
	r.look(t, gatedCall, "call-1", false)
	a := r.onlyRequest(t)
	line := sealedLine(t, r.token(t), lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: "PostToolUse", Subject: "s", Digest: a.SubjectDigest, AtMs: 1})
	if err := r.journal.ExpireToken(t.Context(), "lane-1"); err != nil {
		t.Fatal(err)
	}
	r.spoolLine(t, line)
	if err := r.subject.DrainSpool(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := r.onlyRequest(t); got.State != authority.ApprovalPending {
		t.Errorf("request %+v", got)
	}
}

// One bad line takes nothing else with it, and a line for a lane this journal never held is dropped
// with the others.
func TestABadLineLeavesTheRest(t *testing.T) {
	t.Parallel()
	r, _ := aLaneThatWaits(t)
	r.look(t, gatedCall, "call-1", false)
	a := r.onlyRequest(t)
	good := sealedLine(t, r.token(t), lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: "PostToolUse", Subject: "s", Digest: a.SubjectDigest, AtMs: 1})
	unknown := sealedLine(t, "a-token", lanes.HookEvent{Lane: "no-such-lane", Generation: 1, Event: "Stop", Subject: "s", AtMs: 1})
	r.spoolLine(t, bytes.Join([][]byte{[]byte("not a line"), unknown, good}, []byte("\n")))
	if err := r.subject.DrainSpool(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := r.onlyRequest(t); got.State != authority.ApprovalAnswered {
		t.Errorf("request %+v", got)
	}
	if hooks, err := r.journal.HookEvents(t.Context(), "no-such-lane"); err != nil || len(hooks) != 0 {
		t.Errorf("hooks %+v: %v", hooks, err)
	}
}
