package contract

import (
	"encoding/json"
	"strings"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// statusHeartbeatMs sets the freshness window of these checks. Time moves only when a
// check moves it.
const statusHeartbeatMs = int64(1_000)

// LaneChecks are what a read of a lane's state promises.
var LaneChecks = []Check{
	{"S20", "a lane's status carries its freshness", checkLaneFreshness},
	{"S21", "a new epoch does not inherit a value as observed", checkEpochRelabels},
	{"S22", "an ended lane stays ended, and reads as the ledger's own fact", checkEndedLane},
	{"S23", "a fenced worker cannot move a lane's status", checkFencedStatus},
	{"S24", "an older epoch cannot overwrite a newer status", checkOlderEpoch},
	{"S25", "a status arrives with its event, under the revision", checkStatusEvent},
	{"S26", "a status names the check its worker runs, and keeps none that is no plain driver, version, and words", checkStatusCheck},
}

// statusRig is one registered worker holding one running lane.
type statusRig struct {
	r       *Rig
	store   ports.Store
	plane   *control.Workers
	auth    ledger.WorkerAuth
	epoch   int64
	seq     int64
	grant   workorders.Grant
	lane    string
	working ledger.LaneStatus
}

func newStatusRig(r *Rig) (*statusRig, error) {
	return newStatusRigOn(r, r.Store(), r.Ready)
}

// newStatusRigOn runs a lane on a work order a test made ready.
func newStatusRigOn(r *Rig, store ports.Store, ready func(ports.Store) (string, error)) (*statusRig, error) {
	s := &statusRig{
		r: r, store: store, lane: r.ID("lane"),
		working: ledger.LaneStatus{ActualState: "working", Liveness: "alive", ModelReach: "reachable", LedgerReach: "unknown"},
	}
	s.plane = control.NewWorkers(s.store, r.Scope, control.WorkerOptions{HeartbeatMs: statusHeartbeatMs, LeaseTTLMs: TTLMs})
	token, err := s.plane.CreateJoinToken(r.Ctx, ledger.Actor{Kind: ledger.ActorMember, ID: "member"})
	if err != nil {
		return nil, err
	}
	worker := r.ID("worker")
	credential, err := s.plane.Register(r.Ctx, ledger.Registration{JoinToken: token, WorkerID: worker, Name: "w"})
	if err != nil {
		return nil, err
	}
	s.auth = ledger.WorkerAuth{WorkerID: worker, Credential: credential.Credential}
	if err := s.open(); err != nil {
		return nil, err
	}
	id, err := ready(s.store)
	if err != nil {
		return nil, err
	}
	s.grant, err = s.plane.Claim(r.Ctx, s.auth, workorders.ClaimRequest{WorkOrder: id, ClaimID: r.ID("claim"), TTLMs: TTLMs})
	if err != nil {
		return nil, err
	}
	ops := r.NewOps(s.store)
	if _, err := ops.Dispatch(r.Ctx, r.Scope, id, s.lane, r.Actor); err != nil {
		return nil, err
	}
	_, err = ops.StartLane(r.Ctx, r.Scope, s.grant.Lease, s.lane, r.Actor)
	return s, err
}

func (s *statusRig) open() error {
	session, err := s.plane.OpenSession(s.r.Ctx, ledger.SessionRequest{Auth: s.auth, Protocol: ledger.ProtocolVersion, Manifest: UsableManifest})
	s.epoch = session.Epoch
	return err
}

func (s *statusRig) upload(eventType string, payload []byte) (ledger.UploadReply, error) {
	s.seq++
	return s.plane.Upload(s.r.Ctx, ledger.UploadBatch{Auth: s.auth, Epoch: s.epoch, Events: []ledger.UploadEvent{{
		Epoch: s.epoch, Seq: s.seq, Type: eventType, WorkOrder: s.grant.WorkOrder, ClaimID: s.grant.ClaimID,
		Fence: s.grant.Fence, Lane: s.lane, Generation: 1, Payload: payload, OccurredAt: 1,
	}}})
}

func (s *statusRig) report(status ledger.LaneStatus) (ledger.UploadReply, error) {
	raw, err := json.Marshal(status)
	if err != nil {
		return ledger.UploadReply{}, err
	}
	return s.upload(ledger.UploadLaneStatus, raw)
}

func (s *statusRig) beat() error {
	_, err := s.plane.Heartbeat(s.r.Ctx, ledger.Heartbeat{
		Auth: s.auth, Epoch: s.epoch, Protocol: ledger.ProtocolVersion, Nonce: "n",
		Leases: []ledger.HeldLease{{WorkOrder: s.grant.WorkOrder, Claim: s.grant.ClaimID, Fence: s.grant.Fence}},
	})
	return err
}

func (s *statusRig) want(state, freshness string) (ledger.LaneView, error) {
	view, err := s.plane.Lane(s.r.Ctx, s.lane)
	if err != nil {
		return view, err
	}
	if view.ActualState != state || view.Freshness != freshness {
		return view, Violatedf("lane reads %s %s, want %s %s", view.ActualState, view.Freshness, state, freshness)
	}
	return view, nil
}

func checkLaneFreshness(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if _, err := s.want("pending", ledger.FreshnessLastReported); err != nil {
		return err
	}
	if _, err := s.report(s.working); err != nil {
		return err
	}
	view, err := s.want("working", ledger.FreshnessObserved)
	if err != nil {
		return err
	}
	if view.AsOf == 0 || view.ReportedByEpoch != s.epoch || view.WorkerID != s.auth.WorkerID {
		return Violatedf("a read carries %+v", view)
	}
	r.DB.ShiftClock(3*statusHeartbeatMs + 1)
	stale, err := s.want("working", ledger.FreshnessLastReported)
	if err != nil {
		return err
	}
	if stale.AsOf != view.AsOf {
		return Violatedf("as_of moved from %d to %d with nothing confirming it", view.AsOf, stale.AsOf)
	}
	if err := s.beat(); err != nil {
		return err
	}
	confirmed, err := s.want("working", ledger.FreshnessObserved)
	if err != nil {
		return err
	}
	if confirmed.AsOf <= view.AsOf {
		return Violatedf("a heartbeat left as_of at %d, from %d", confirmed.AsOf, view.AsOf)
	}
	return nil
}

func checkEpochRelabels(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if _, err := s.report(s.working); err != nil {
		return err
	}
	if err := s.open(); err != nil {
		return err
	}
	_, err = s.want("working", ledger.FreshnessLastReported)
	return err
}

func checkEndedLane(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeCompleted}})
	if err != nil {
		return err
	}
	if _, err := s.upload(ledger.UploadLaneEnded, raw); err != nil {
		return err
	}
	view, err := s.want(ledger.StateEnded, ledger.FreshnessObserved)
	if err != nil {
		return err
	}
	if view.Liveness != "dead" {
		return Violatedf("an ended lane reads liveness %s", view.Liveness)
	}
	if _, err := s.report(s.working); err != nil {
		return err
	}
	r.DB.ShiftClock(TTLMs)
	_, err = s.want(ledger.StateEnded, ledger.FreshnessObserved)
	return err
}

func checkFencedStatus(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if _, err := s.report(s.working); err != nil {
		return err
	}
	if err := r.Expire(s.store); err != nil {
		return err
	}
	ops := r.NewOps(s.store)
	if err := ops.Reassign(r.Ctx, r.Scope, s.grant.WorkOrder, "moved on", r.Actor); err != nil {
		return err
	}
	reply, err := s.report(ledger.LaneStatus{ActualState: "idle", Liveness: "alive"})
	if err != nil {
		return err
	}
	if len(reply.Rejected) != 1 || reply.Rejected[0].Status != ledger.UploadFenced {
		return Violatedf("a fenced status was answered %+v", reply)
	}
	view, err := s.plane.Lane(r.Ctx, s.lane)
	if err != nil {
		return err
	}
	if view.ActualState != "working" {
		return Violatedf("a fenced worker moved the lane to %s", view.ActualState)
	}
	return nil
}

func checkOlderEpoch(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if err := s.open(); err != nil {
		return err
	}
	if _, err := s.report(s.working); err != nil {
		return err
	}
	err = s.store.Write(r.Ctx, r.Scope, "older", func(tx ports.WriteTx) error {
		err := tx.Lanes().Report(ledger.LaneRow{
			ID: s.lane, WorkerID: s.auth.WorkerID, ReportedByEpoch: s.epoch - 1,
			Status: ledger.LaneStatus{ActualState: "idle", Liveness: "alive", ModelReach: "unknown", LedgerReach: "unknown"},
		})
		if err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{Type: "note.added", Actor: r.Actor, Subject: ledger.Subject{Kind: "note", ID: "note"}})
		return err
	})
	if err != nil {
		return err
	}
	_, err = s.want("working", ledger.FreshnessObserved)
	return err
}

func checkStatusEvent(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if _, err := s.report(s.working); err != nil {
		return err
	}
	events, err := r.Events(s.store, r.Scope)
	if err != nil {
		return err
	}
	found := 0
	for _, event := range events {
		if event.Type == ledger.EventLaneStatus && event.Subject.ID == s.lane {
			found++
		}
	}
	if found != 1 {
		return Violatedf("%d status events", found)
	}
	return r.CheckProjection(s.store)
}

// checkStatusCheck holds a lane's check to what a person can read: a status that names a driver and a version in
// the shapes a driver and a version have, with words that are one plain line of bounded length, is kept, and a
// status whose check is anything else is kept without it, so the lane still moves.
func checkStatusCheck(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	named := s.working
	named.Checking = ledger.CheckingCLI("claude-code", "2.1.400")
	if _, err := s.report(named); err != nil {
		return err
	}
	view, err := s.want("working", ledger.FreshnessObserved)
	if err != nil {
		return err
	}
	if view.Checking == nil || *view.Checking != *named.Checking {
		return Violatedf("a lane's check reads %+v, want %+v", view.Checking, named.Checking)
	}
	words := named.Checking.Words
	refused := []struct {
		what  string
		check *ledger.CLICheck
	}{
		{"a driver with a space", &ledger.CLICheck{Driver: "claude code", Version: "2.1.400", Words: words}},
		{"a driver in capitals", &ledger.CLICheck{Driver: "Claude-Code", Version: "2.1.400", Words: words}},
		{"a version with a line break", &ledger.CLICheck{Driver: "claude-code", Version: "2.1.400\nelse", Words: words}},
		{"a version that is no version", &ledger.CLICheck{Driver: "claude-code", Version: "../2.1", Words: words}},
		{"a version too long", &ledger.CLICheck{Driver: "claude-code", Version: strings.Repeat("9", 65), Words: words}},
		{"words with an escape sequence", &ledger.CLICheck{Driver: "claude-code", Version: "2.1.400", Words: "Checking \x1b[2J"}},
		{"words with a line break", &ledger.CLICheck{Driver: "claude-code", Version: "2.1.400", Words: "Checking\nClaude Code"}},
		{"words with a hidden character", &ledger.CLICheck{Driver: "claude-code", Version: "2.1.400", Words: "Checking \u202eClaude Code"}},
		{"words past the bound", &ledger.CLICheck{Driver: "claude-code", Version: "2.1.400", Words: strings.Repeat("x", 301)}},
	}
	if _, err := s.report(named); err != nil {
		return err
	}
	// Each refused check rides a status that moves the lane, so the state that arrives with it is what shows it was kept.
	states := []string{"idle", "working"}
	for i, tt := range refused {
		state := states[i%len(states)]
		moved := ledger.LaneStatus{ActualState: state, Liveness: "alive", ModelReach: "reachable", LedgerReach: "unknown", Checking: tt.check}
		if _, err := s.report(moved); err != nil {
			return err
		}
		view, err := s.plane.Lane(r.Ctx, s.lane)
		if err != nil {
			return err
		}
		if view.ActualState != state {
			return Violatedf("a status with %s left the lane %s, want %s", tt.what, view.ActualState, state)
		}
		if view.Checking != nil {
			return Violatedf("a status with %s left the lane's check at %+v", tt.what, view.Checking)
		}
	}
	events, err := r.Events(s.store, r.Scope)
	if err != nil {
		return err
	}
	for _, event := range events {
		if event.Type != ledger.EventLaneStatus || event.Subject.ID != s.lane {
			continue
		}
		var status ledger.LaneStatus
		if err := json.Unmarshal(event.Data, &status); err != nil {
			return err
		}
		if status.Checking != nil && *status.Checking != *named.Checking {
			return Violatedf("the log keeps a lane's check of %+v", status.Checking)
		}
	}
	return r.CheckProjection(s.store)
}
