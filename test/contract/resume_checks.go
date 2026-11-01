package contract

import (
	"encoding/json"
	"errors"
	"slices"

	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/parking"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// AffinityDelayMs is how long a preferred worker has a woken lane to itself.
const AffinityDelayMs = control.AffinityDelayMs

// ResumeChecks are what the ledger promises a woken lane (FR-PARK-3, FR-PARK-6): the next claim
// resumes it, a new generation starts under that claim, and affinity limits who can take it.
var ResumeChecks = []Check{
	{"R1", "the next claim on woken work runs the lane it woke", checkClaimNamesTheLane},
	{"R2", "a claim keeps the lane's own profile layer", checkClaimKeepsOverrides},
	{"R3", "a resume starts the next generation and says why", checkResumeStartsAGeneration},
	{"R4", "a resume clears the lane to resume, and a replay agrees", checkResumeClears},
	{"R5", "a claim given back before the resume still resumes that lane", checkReleasedClaimResumes},
	{"R6", "work no remote holds is offered only to its worker", checkRequiredAffinity},
	{"R7", "a preferred worker has the first claim, and a delay lets any other in", checkPreferredAffinity},
	{"R8", "woken work sorts ahead of new work", checkWokenSortsFirst},
	{"R9", "desired state says what a resume starts from", checkDesiredResume},
	{"R10", "the last model response is database time, on the generation", checkLastResponse},
	{"R11", "the generation before a resume cannot write", checkResumeFences},
	{"R12", "a lane that resumed parks and resumes again", checkResumeTwice},
	{"R13", "a worker with the wrong claim cannot resume", checkResumeOthers},
	{"R14", "a member's word reaches a lane through its work order, and only a member's", checkResumeWork},
	{"R15", "a retried resume returns the generation it already started", checkContinueRepeats},
}

// checkResumeWork parks two lanes, because a word that named no work order would still find the
// one lane there is. Only the lane of the named work order may wake.
func checkResumeWork(r *Rig) error {
	s, _, err := parkedOn(r, true)
	if err != nil {
		return err
	}
	bystander, _, err := parkedOn(r, true)
	if err != nil {
		return err
	}
	ops := r.NewOps(s.store)
	id := s.grant.WorkOrder
	orchestrator := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "orchestrator-1"}
	if _, err := ops.ResumeWork(r.Ctx, r.Scope, id, orchestrator); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("an orchestrator woke a lane only a person can: %v", err)
	}
	fired, err := ops.ResumeWork(r.Ctx, r.Scope, id, r.Actor)
	if err != nil {
		return err
	}
	if len(fired) != 1 {
		return Violatedf("a member's word fired %v", fired)
	}
	row, err := r.WorkOrder(s.store, id)
	if err != nil {
		return err
	}
	if row.State != workorders.Ready || row.ResumeLaneID != s.lane {
		return Violatedf("row %+v", row)
	}
	other, err := r.WorkOrder(s.store, bystander.grant.WorkOrder)
	if err != nil {
		return err
	}
	if other.State != workorders.Parked {
		return Violatedf("a word to one work order moved another: %+v", other)
	}
	if _, err := ops.ResumeWork(r.Ctx, r.Scope, id, r.Actor); !errors.Is(err, workorders.ErrIllegalMove) {
		return Violatedf("a word to a lane that is not parked: %v", err)
	}
	return nil
}

// checkContinueRepeats retries a resume by both roads. The use case goes straight to the
// transaction, and a new event of a worker that lost the answer does too, because only an event
// it already stored is found by its ID.
func checkContinueRepeats(r *Rig) error {
	s, err := wokenOn(r, true)
	if err != nil {
		return err
	}
	grant, err := s.claim()
	if err != nil {
		return err
	}
	holder := workorders.Holder{Lease: grant.Lease, Lane: s.lane, Generation: 1}
	ops := r.NewOps(s.store)
	for attempt := range 2 {
		next, err := ops.ContinueLane(r.Ctx, r.Scope, holder, ledger.StartResume, r.Actor)
		if err != nil {
			return Violatedf("attempt %d of a resume: %v", attempt, err)
		}
		if next != 2 {
			return Violatedf("generation %d", next)
		}
	}
	if err := accepted(s.resumeUpload(grant, 2, ledger.StartResume)); err != nil {
		return err
	}
	generations, err := s.generations()
	if err != nil {
		return err
	}
	if len(generations) != 2 {
		return Violatedf("a retry started another generation: %+v", generations)
	}
	return r.CheckProjection(s.store)
}

// parkedOn parks a rig's lane on a person's word, with its work on a remote or not.
func parkedOn(r *Rig, pushed bool) (*statusRig, string, error) {
	s, wake, err := parkingOn(r, `{"human_resume":true}`)
	if err != nil {
		return nil, "", err
	}
	return s, wake, accepted(s.parkedUpload(wake, pushed))
}

func (s *statusRig) woken() error {
	_, err := s.r.NewOps(s.store).ResumeLane(s.r.Ctx, s.r.Scope, s.lane, s.r.Actor)
	return err
}

// wokenOn parks a rig's lane and wakes it.
func wokenOn(r *Rig, pushed bool) (*statusRig, error) {
	s, _, err := parkedOn(r, pushed)
	if err != nil {
		return nil, err
	}
	return s, s.woken()
}

func (s *statusRig) claim() (workorders.Grant, error) {
	return s.plane.Claim(s.r.Ctx, s.auth, workorders.ClaimRequest{WorkOrder: s.grant.WorkOrder, ClaimID: s.r.ID("claim"), TTLMs: TTLMs})
}

// resumeUpload starts the next generation under a claim.
func (s *statusRig) resumeUpload(grant workorders.Grant, number int64, reason ledger.StartReason) (ledger.UploadReply, error) {
	raw, err := json.Marshal(ledger.GenerationStart{Reason: reason})
	if err != nil {
		return ledger.UploadReply{}, err
	}
	s.grant = grant
	return s.uploadAt(number, ledger.UploadLaneResumed, raw)
}

func (s *statusRig) desired() (ledger.DesiredState, error) {
	return s.plane.Desired(s.r.Ctx, ledger.DesiredRequest{Auth: s.auth})
}

func checkClaimNamesTheLane(r *Rig) error {
	s, err := wokenOn(r, true)
	if err != nil {
		return err
	}
	grant, err := s.claim()
	if err != nil {
		return err
	}
	if grant.ResumeLaneID != s.lane {
		return Violatedf("the grant names %q, want %q", grant.ResumeLaneID, s.lane)
	}
	state, err := s.desired()
	if err != nil {
		return err
	}
	if len(state.Leases) != 1 || state.Leases[0].Lane != s.lane || state.Leases[0].Resume == nil {
		return Violatedf("desired state reads %+v", state.Leases)
	}
	if len(state.Woken) != 0 {
		return Violatedf("work a claim took is still offered: %+v", state.Woken)
	}
	return nil
}

func checkClaimKeepsOverrides(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	grant, err := ops.Claim(r.Ctx, r.Scope, r.Request(id, "a"), r.worker("a"))
	if err != nil {
		return err
	}
	lane := r.ID("lane")
	const overrides = "[agent]\nmodel = \"haiku\"\n"
	if _, err := control.NewLeases(s).DispatchWith(r.Ctx, r.Scope, id, lane, overrides, r.Actor); err != nil {
		return err
	}
	generation, err := ops.StartLane(r.Ctx, r.Scope, grant.Lease, lane, r.Actor)
	if err != nil {
		return err
	}
	holder := workorders.Holder{Lease: grant.Lease, Lane: lane, Generation: generation}
	wake := r.ID("wake")
	if err := ops.BeginParking(r.Ctx, r.Scope, holder, parking.Draft{ID: wake, Expr: `{"human_resume":true}`}, r.worker("a")); err != nil {
		return err
	}
	if _, err := ops.CommitParked(r.Ctx, r.Scope, holder, PushedCommit(wake), r.worker("a")); err != nil {
		return err
	}
	if _, err := ops.ResumeLane(r.Ctx, r.Scope, lane, r.Actor); err != nil {
		return err
	}
	again, err := ops.Claim(r.Ctx, r.Scope, r.Request(id, "a"), r.worker("a"))
	if err != nil {
		return err
	}
	var claim workorders.Claim
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		claim, err = tx.Claims().Find(again.ClaimID)
		return err
	})
	if err != nil {
		return err
	}
	if claim.Lane != lane || claim.Overrides != overrides {
		return Violatedf("a claim on woken work reads %+v", claim)
	}
	return nil
}

func checkResumeStartsAGeneration(r *Rig) error {
	for _, reason := range []ledger.StartReason{ledger.StartResume, ledger.StartHandoff} {
		s, err := wokenOn(r, true)
		if err != nil {
			return err
		}
		grant, err := s.claim()
		if err != nil {
			return err
		}
		if err := accepted(s.resumeUpload(grant, 2, reason)); err != nil {
			return err
		}
		generations, err := s.generations()
		if err != nil {
			return err
		}
		if len(generations) != 2 || generations[1].StartReason != reason || generations[0].EndReason != workorders.EndParked {
			return Violatedf("generations %+v", generations)
		}
		row, err := r.WorkOrder(s.store, grant.WorkOrder)
		if err != nil {
			return err
		}
		if row.State != workorders.Running {
			return Violatedf("a resumed lane's work order is %s", row.State)
		}
	}
	return nil
}

func checkResumeClears(r *Rig) error {
	s, err := wokenOn(r, true)
	if err != nil {
		return err
	}
	grant, err := s.claim()
	if err != nil {
		return err
	}
	row, err := r.WorkOrder(s.store, grant.WorkOrder)
	if err != nil {
		return err
	}
	if row.ResumeLaneID != s.lane {
		return Violatedf("a claim cleared the lane to resume: %+v", row)
	}
	if err := accepted(s.resumeUpload(grant, 2, ledger.StartResume)); err != nil {
		return err
	}
	if row, err = r.WorkOrder(s.store, grant.WorkOrder); err != nil {
		return err
	}
	if row.ResumeLaneID != "" {
		return Violatedf("a resumed lane is still to resume: %+v", row)
	}
	return r.CheckProjection(s.store)
}

func checkReleasedClaimResumes(r *Rig) error {
	s, err := wokenOn(r, true)
	if err != nil {
		return err
	}
	grant, err := s.claim()
	if err != nil {
		return err
	}
	if err := r.NewOps(s.store).Release(r.Ctx, r.Scope, grant.Lease, r.Actor); err != nil {
		return err
	}
	again, err := s.claim()
	if err != nil {
		return err
	}
	if again.ResumeLaneID != s.lane {
		return Violatedf("a released claim lost the lane: %+v", again)
	}
	return r.CheckProjection(s.store)
}

// strangerOf is a second worker in a rig's workspace.
func strangerOf(s *statusRig) (*statusRig, error) {
	r := s.r
	token, err := s.plane.CreateJoinToken(r.Ctx, r.Actor)
	if err != nil {
		return nil, err
	}
	id := r.ID("worker")
	credential, err := s.plane.Register(r.Ctx, ledger.Registration{JoinToken: token, WorkerID: id, Name: "other"})
	if err != nil {
		return nil, err
	}
	other := &statusRig{r: r, store: s.store, plane: s.plane, lane: s.lane, grant: s.grant, working: s.working}
	other.auth = ledger.WorkerAuth{WorkerID: id, Credential: credential.Credential}
	return other, other.open()
}

func checkRequiredAffinity(r *Rig) error {
	s, err := wokenOn(r, false)
	if err != nil {
		return err
	}
	stranger, err := strangerOf(s)
	if err != nil {
		return err
	}
	if _, err := stranger.claim(); !errors.Is(err, workorders.ErrIneligible) {
		return Violatedf("another worker claimed work no remote holds: %v", err)
	}
	offered, err := s.plane.Offered(r.Ctx, stranger.auth.WorkerID)
	if err != nil {
		return err
	}
	if len(offered) != 0 {
		return Violatedf("work only one machine holds was offered to another: %+v", offered)
	}
	state, err := stranger.desired()
	if err != nil {
		return err
	}
	if len(state.Woken) != 0 {
		return Violatedf("desired state offered it: %+v", state.Woken)
	}
	r.DB.ShiftClock(AffinityDelayMs + 1)
	if _, err := r.NewOps(s.store).WakeTick(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	if _, err := stranger.claim(); !errors.Is(err, workorders.ErrIneligible) {
		return Violatedf("a delay let another worker take work only one machine holds: %v", err)
	}
	state, err = s.desired()
	if err != nil {
		return err
	}
	if len(state.Woken) != 1 || state.Woken[0].Lane != s.lane {
		return Violatedf("its own worker reads %+v", state.Woken)
	}
	_, err = s.claim()
	return err
}

func checkPreferredAffinity(r *Rig) error {
	s, err := wokenOn(r, true)
	if err != nil {
		return err
	}
	stranger, err := strangerOf(s)
	if err != nil {
		return err
	}
	if _, err := stranger.claim(); !errors.Is(err, workorders.ErrIneligible) {
		return Violatedf("another worker went ahead of the delay: %v", err)
	}
	// A tick reads its list with no lock, so this is what a stale list would do.
	err = s.store.Write(r.Ctx, r.Scope, "check.ease", func(tx ports.WriteTx) error {
		won, err := tx.Lanes().Ease(s.lane, tx.Now())
		if won {
			return Violatedf("a stale list eased a lane inside its delay")
		}
		return err
	})
	if err != nil {
		return err
	}
	r.DB.ShiftClock(AffinityDelayMs - 1000)
	if _, err := r.NewOps(s.store).WakeTick(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	if _, err := stranger.claim(); !errors.Is(err, workorders.ErrIneligible) {
		return Violatedf("another worker went ahead of the delay: %v", err)
	}
	before, err := r.Revision(s.store, r.Scope)
	if err != nil {
		return err
	}
	r.DB.ShiftClock(2000)
	if _, err := r.NewOps(s.store).WakeTick(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	after, err := r.Revision(s.store, r.Scope)
	if err != nil {
		return err
	}
	if after == before {
		return Violatedf("a delay ran out and no worker can hear of it, because the revision stayed")
	}
	state, err := stranger.desired()
	if err != nil {
		return err
	}
	if len(state.Woken) != 1 {
		return Violatedf("desired state reads %+v", state.Woken)
	}
	grant, err := stranger.claim()
	if err != nil {
		return err
	}
	if grant.ResumeLaneID != s.lane {
		return Violatedf("grant %+v", grant)
	}
	return r.CheckProjection(s.store)
}

func checkWokenSortsFirst(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	fresh, err := r.Ready(s.store)
	if err != nil {
		return err
	}
	wake := r.ID("wake")
	if err := accepted(s.parkingUpload(wake, `{"human_resume":true}`)); err != nil {
		return err
	}
	if err := accepted(s.parkedUpload(wake, true)); err != nil {
		return err
	}
	if err := s.woken(); err != nil {
		return err
	}
	later, err := r.Ready(s.store)
	if err != nil {
		return err
	}
	offered, err := s.plane.Offered(r.Ctx, s.auth.WorkerID)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(offered))
	for _, row := range offered {
		ids = append(ids, row.ID)
	}
	if len(ids) != 3 || ids[0] != s.grant.WorkOrder || !slices.Contains(ids, fresh) || !slices.Contains(ids, later) {
		return Violatedf("offered %v, and woken work %s must lead", ids, s.grant.WorkOrder)
	}
	return nil
}

func checkDesiredResume(r *Rig) error {
	s, wake, err := parkingOn(r, `{"human_resume":true}`)
	if err != nil {
		return err
	}
	if err := accepted(s.respondedAt(1)); err != nil {
		return err
	}
	_, raw, err := handoffBody(`{"schema":"x"}`)
	if err != nil {
		return err
	}
	if err := accepted(s.uploadAt(1, ledger.UploadHandoffStored, raw)); err != nil {
		return err
	}
	if err := accepted(s.parkedUpload(wake, true)); err != nil {
		return err
	}
	if err := s.woken(); err != nil {
		return err
	}
	r.DB.ShiftClock(5000)
	if _, err := s.claim(); errors.Is(err, workorders.ErrIneligible) {
		return Violatedf("the worker the lane parked on cannot take it back: %v", err)
	} else if err != nil {
		return err
	}
	state, err := s.desired()
	if err != nil {
		return err
	}
	if len(state.Leases) != 1 || state.Leases[0].Resume == nil {
		return Violatedf("leases %+v", state.Leases)
	}
	resume := state.Leases[0].Resume
	if resume.Generation != 1 || resume.Worker != s.auth.WorkerID || string(resume.Handoff) != `{"schema":"x"}` || resume.Pending != "Finish the retry" {
		return Violatedf("resume %+v", resume)
	}
	if !resume.Responded || resume.SinceResponseMs < 5000 {
		return Violatedf("the cache clock reads %d, want at least the 5s that passed", resume.SinceResponseMs)
	}
	return nil
}

func (s *statusRig) respondedAt(generation int64) (ledger.UploadReply, error) {
	return s.uploadAt(generation, ledger.UploadGenerationResponded, nil)
}

func checkLastResponse(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	before, err := r.Clock(s.store)
	if err != nil {
		return err
	}
	if err := accepted(s.respondedAt(1)); err != nil {
		return err
	}
	generations, err := s.generations()
	if err != nil {
		return err
	}
	if len(generations) != 1 || generations[0].LastResponseAt < before {
		return Violatedf("generations %+v, read after %d", generations, before)
	}
	r.DB.ShiftClock(1000)
	if err := accepted(s.respondedAt(1)); err != nil {
		return err
	}
	later, err := s.generations()
	if err != nil {
		return err
	}
	if later[0].LastResponseAt <= generations[0].LastResponseAt {
		return Violatedf("a later response did not move the clock: %+v", later)
	}
	return nil
}

// checkResumeFences holds the fence and the generation apart, because each guards a write the
// other lets through: the last claim naming the new generation stops only on its fence, and the
// new claim naming the last generation stops only on its generation.
func checkResumeFences(r *Rig) error {
	s, err := wokenOn(r, true)
	if err != nil {
		return err
	}
	old := s.grant
	grant, err := s.claim()
	if err != nil {
		return err
	}
	if err := accepted(s.resumeUpload(grant, 2, ledger.StartResume)); err != nil {
		return err
	}
	writes := []struct {
		grant      workorders.Grant
		generation int64
	}{{old, 1}, {old, 2}, {grant, 1}}
	for _, write := range writes {
		s.grant = write.grant
		reply, err := s.uploadAt(write.generation, ledger.UploadAdmissionHeld, nil)
		if err := refusedOnly(reply, err, ledger.UploadFenced); err != nil {
			return err
		}
	}
	return nil
}

func checkResumeTwice(r *Rig) error {
	s, err := wokenOn(r, true)
	if err != nil {
		return err
	}
	grant, err := s.claim()
	if err != nil {
		return err
	}
	if err := accepted(s.resumeUpload(grant, 2, ledger.StartResume)); err != nil {
		return err
	}
	raw, err := json.Marshal(ledger.ParkingStart{Wake: r.ID("wake"), Expr: `{"human_resume":true}`})
	if err != nil {
		return err
	}
	if err := accepted(s.uploadAt(2, ledger.UploadLaneParking, raw)); err != nil {
		return err
	}
	var start ledger.ParkingStart
	if err := json.Unmarshal(raw, &start); err != nil {
		return err
	}
	commit, err := json.Marshal(ledger.ParkedCommit{Wake: start.Wake, Checkpoint: ledger.Checkpoint{Pushed: true}})
	if err != nil {
		return err
	}
	if err := accepted(s.uploadAt(2, ledger.UploadLaneParked, commit)); err != nil {
		return err
	}
	if err := s.woken(); err != nil {
		return err
	}
	grant, err = s.claim()
	if err != nil {
		return err
	}
	if err := accepted(s.resumeUpload(grant, 3, ledger.StartHandoff)); err != nil {
		return err
	}
	generations, err := s.generations()
	if err != nil {
		return err
	}
	if len(generations) != 3 || generations[2].StartReason != ledger.StartHandoff {
		return Violatedf("generations %+v", generations)
	}
	if err := r.CheckProjection(s.store); err != nil {
		return err
	}
	return r.CheckWakes(s.store)
}

// checkResumeOthers holds a resume to the claim that took the lane. A claim nobody holds finds no
// row, but the other two are real claims that the lease guard would pass, so only the resume's
// own check stops them, and the lane must still be waiting afterwards.
func checkResumeOthers(r *Rig) error {
	s, err := wokenOn(r, true)
	if err != nil {
		return err
	}
	grant, err := s.claim()
	if err != nil {
		return err
	}
	stale := grant
	stale.ClaimID = "not-this-claim"
	reply, err := s.resumeUpload(stale, 2, ledger.StartResume)
	if err := refusedOnly(reply, err, ledger.UploadInvalid); err != nil {
		return err
	}
	stranger, err := strangerOf(s)
	if err != nil {
		return err
	}
	reply, err = stranger.resumeUpload(grant, 2, ledger.StartResume)
	if err := refusedOnly(reply, err, ledger.UploadInvalid); err != nil {
		return Violatedf("another worker's resume: %v", err)
	}
	lane := s.lane
	s.lane = r.ID("another-lane")
	reply, err = s.resumeUpload(grant, 2, ledger.StartResume)
	s.lane = lane
	if err := refusedOnly(reply, err, ledger.UploadInvalid); err != nil {
		return Violatedf("a claim on another lane: %v", err)
	}
	row, err := r.WorkOrder(s.store, grant.WorkOrder)
	if err != nil {
		return err
	}
	if row.State != workorders.Claimed || row.ResumeLaneID != s.lane {
		return Violatedf("a refused resume moved the work order: %+v", row)
	}
	generations, err := s.generations()
	if err != nil {
		return err
	}
	if len(generations) != 1 {
		return Violatedf("a refused resume started a generation: %+v", generations)
	}
	return nil
}
