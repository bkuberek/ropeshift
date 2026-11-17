package control_test

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
)

var hintedRepo = ledger.ForgeBinding{Host: "github.com", Owner: "acme", Repo: "widgets"}

// A hint is handed to a worker once: each worker names the last one it heard, and the reply holds what came after.
func TestTheBoardHandsAWorkerTheHintsAfterItsMark(t *testing.T) {
	t.Parallel()
	now := time.UnixMilli(1_700_000_000_000)
	board := control.NewCIHints(func() time.Time { return now })
	board.Note(hintedRepo, "aaa")
	board.Note(hintedRepo, "")
	all, seq := board.Since(0)
	if len(all) != 2 || all[0].Head != "aaa" || all[0].Binding != hintedRepo.String() || all[1].Head != "" || all[1].Seq <= all[0].Seq || seq != all[1].Seq {
		t.Fatalf("a worker with no mark hears %+v up to %d", all, seq)
	}
	after, _ := board.Since(all[0].Seq)
	if len(after) != 1 || after[0] != all[1] {
		t.Errorf("a worker that heard the first hears %+v", after)
	}
	if none, again := board.Since(seq); len(none) != 0 || again != seq {
		t.Errorf("a worker that heard them all hears %+v up to %d", none, again)
	}
}

// A hint is a prompt to read sooner, so one that waited too long is stale and is dropped, and the board holds
// the most recent hints and no more.
func TestTheBoardForgetsAHintAfterItsLifeAndHoldsAFixedNumber(t *testing.T) {
	t.Parallel()
	now := time.UnixMilli(1_700_000_000_000)
	board := control.NewCIHints(func() time.Time { return now })
	board.Note(hintedRepo, "old")
	now = now.Add(11 * time.Minute)
	if hints, _ := board.Since(0); len(hints) != 0 {
		t.Errorf("a hint after 11 minutes is still handed out: %+v", hints)
	}
	for i := range 300 {
		board.Note(hintedRepo, string(rune('a'+i%26)))
	}
	hints, seq := board.Since(0)
	if len(hints) != control.CIHintsHeld || hints[len(hints)-1].Seq != seq {
		t.Errorf("the board holds %d hints up to %d, want %d and the newest", len(hints), seq, control.CIHintsHeld)
	}
}

// A control plane that restarted numbers its hints above the ones before it, so a worker's mark from the earlier run
// is below every hint of this one and nothing is held back.
func TestARestartedBoardNumbersAboveTheOneBefore(t *testing.T) {
	t.Parallel()
	earlier := time.UnixMilli(1_700_000_000_000)
	before := control.NewCIHints(func() time.Time { return earlier })
	before.Note(hintedRepo, "a")
	_, mark := before.Since(0)
	after := control.NewCIHints(func() time.Time { return earlier.Add(time.Minute) })
	after.Note(hintedRepo, "b")
	if hints, _ := after.Since(mark); len(hints) != 1 || hints[0].Head != "b" {
		t.Errorf("a worker with the earlier run's mark hears %+v", hints)
	}
}

// The plane's heartbeat carries the hints after the worker's mark, with the new mark.
func TestTheHeartbeatCarriesTheCIHints(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	r.plane.CIHints().Note(hintedRepo, "aaa")
	beat := ledger.Heartbeat{Auth: r.auth, Epoch: r.epoch, Protocol: ledger.ProtocolVersion, Nonce: "n"}
	reply, err := r.plane.Heartbeat(t.Context(), beat)
	if err != nil || len(reply.Hints) != 1 || reply.Hints[0].Head != "aaa" || reply.HintsSeq != reply.Hints[0].Seq {
		t.Fatalf("a heartbeat with no mark answers %+v: %v", reply, err)
	}
	beat.HintsThrough = reply.HintsSeq
	again, err := r.plane.Heartbeat(t.Context(), beat)
	if err != nil || len(again.Hints) != 0 || again.HintsSeq != reply.HintsSeq {
		t.Errorf("a heartbeat with the mark answers %+v: %v", again, err)
	}
}
