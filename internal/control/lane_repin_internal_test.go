package control

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/roster"
	"ropeshift.com/ropeshift/internal/workorders"
)

// builtinRosterWriter resolves the built-in roster in a transaction that writes no version.
type builtinRosterWriter struct{ ports.RosterWriter }

func (builtinRosterWriter) Latest(scope string) (roster.Version, error) {
	return builtinRoster{}.Latest(scope)
}

func (builtinRosterWriter) Version(scope string, version int64) (roster.Version, error) {
	return builtinRoster{}.Version(scope, version)
}

func (builtinRosterWriter) Versions(scope string) ([]roster.Version, error) {
	return builtinRoster{}.Versions(scope)
}

// pinnedLane answers that the last claim to run a lane pinned an agent.
type pinnedLane struct {
	ports.ClaimWriter
	last workorders.Claim
}

func (p pinnedLane) OfLane(string) (workorders.Claim, error) { return p.last, nil }

func (p pinnedLane) Find(string) (workorders.Claim, error) {
	return workorders.Claim{}, ports.ErrNotFound
}

// neverReworked says no attempt of the work was sent back.
type neverReworked struct{ ports.EvidenceWriter }

func (neverReworked) Reworked(string) (workorders.ReworkNote, error) {
	return workorders.ReworkNote{}, ports.ErrNotFound
}

// movedOrders holds one work order, and notes that it moved.
type movedOrders struct {
	ports.WorkOrderWriter
	order workorders.WorkOrder
	moved *bool
}

func (m movedOrders) Get(string) (workorders.WorkOrder, error) { return m.order, nil }

func (m movedOrders) Move(string, workorders.State, workorders.State) (bool, error) {
	*m.moved = true
	return true, nil
}

// wokenLaneTx is the part of a transaction that reads a woken lane's claim and the roster.
type wokenLaneTx struct {
	ports.WriteTx
	last  workorders.Claim
	order workorders.WorkOrder
	moved bool
}

func (t *wokenLaneTx) Claims() ports.ClaimWriter      { return pinnedLane{last: t.last} }
func (t *wokenLaneTx) Roster() ports.RosterWriter     { return builtinRosterWriter{} }
func (t *wokenLaneTx) Evidence() ports.EvidenceWriter { return neverReworked{} }
func (t *wokenLaneTx) WorkOrders() ports.WorkOrderWriter {
	return movedOrders{order: t.order, moved: &t.moved}
}

func (t *wokenLaneTx) Append(ledger.EventDraft) (ledger.Event, error) { return ledger.Event{}, nil }

func wokenChange(agent string) workorders.WorkOrder {
	return workorders.WorkOrder{ID: "wo-1", Kind: workorders.KindChange, State: workorders.Ready, ResumeLaneID: "lane-1", Agent: agent}
}

// A woken lane resumes as the agent its earlier claim pinned, unless the roster no longer gives that agent to its work,
// such as a shell-less agent pinned before change work needed the shell. It then resumes as the agent the roster picks
// for the work, under the roster as it stands, and the words say which agent it left and why.
func TestAWokenLaneWhoseClaimPinnedAnUnfitAgentResumesOnTheRostersPick(t *testing.T) {
	view, err := ResolveIn(builtinRoster{})
	if err != nil {
		t.Fatal(err)
	}
	pick := view.Resolved.Defaults.Change[roster.SizeMedium]
	if pick == "" || pick == "reasoning" {
		t.Fatalf("the default change agent is %q", pick)
	}
	tx := &wokenLaneTx{}
	order := wokenChange("")
	claim := workorders.Claim{Lane: "lane-1", Agent: "reasoning", Roster: workorders.RosterPin{Installation: 3, Digest: "older"}}
	words, err := repinUnfit(tx, view.Resolved, order, "claim-1", &claim)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Agent != pick || claim.Roster.Digest != view.Resolved.Digest {
		t.Errorf("the claim pins %q under %+v, want %q under the roster as it stands", claim.Agent, claim.Roster, pick)
	}
	for _, want := range []string{`"` + pick + `"`, `"reasoning"`, "denies the shell"} {
		if !strings.Contains(words, want) {
			t.Errorf("the words %q lack %q", words, want)
		}
	}
}

func TestAWokenLaneWhoseClaimPinnedAFitAgentKeepsIt(t *testing.T) {
	view, err := ResolveIn(builtinRoster{})
	if err != nil {
		t.Fatal(err)
	}
	pick := view.Resolved.Defaults.Change[roster.SizeMedium]
	claim := workorders.Claim{Lane: "lane-1", Agent: pick, Roster: workorders.RosterPin{Installation: 3, Digest: "older"}}
	words, err := repinUnfit(&wokenLaneTx{}, view.Resolved, wokenChange(""), "claim-1", &claim)
	if err != nil || words != "" || claim.Agent != pick || claim.Roster.Digest != "older" {
		t.Errorf("a fit pin became %q under %+v with words %q, %v", claim.Agent, claim.Roster, words, err)
	}
}

// Ready work that continues a woken lane is sent back to contract review only when the roster gives it no agent that
// fits: a pin that no longer fits is replaced at the claim, and one that still fits stays.
func TestAWokenLaneWithAnUnfitPinIsSentBackOnlyWhenNoAgentFits(t *testing.T) {
	view, err := ResolveIn(builtinRoster{})
	if err != nil {
		t.Fatal(err)
	}
	pick := view.Resolved.Defaults.Change[roster.SizeMedium]
	actor := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	tests := []struct {
		name   string
		pinned string
		named  string
		back   bool
	}{
		{name: "a fit pin", pinned: pick},
		{name: "an unfit pin and a fit pick", pinned: "reasoning"},
		{name: "an unfit pin and an unfit agent the work names", pinned: "reasoning", named: "reasoning", back: true},
		{name: "a pin the roster does not know and a fit pick", pinned: "retired"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := wokenChange(tt.named)
			tx := &wokenLaneTx{last: workorders.Claim{Lane: "lane-1", Agent: tt.pinned}, order: order}
			reason, err := sendBackUnfit(tx, view.Resolved, order, actor)
			if err != nil {
				t.Fatal(err)
			}
			if (reason != "") != tt.back || tx.moved != tt.back {
				t.Errorf("reason %q, moved %v, want sent back %v", reason, tx.moved, tt.back)
			}
		})
	}
}
