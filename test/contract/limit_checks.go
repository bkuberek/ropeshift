package contract

import (
	"encoding/json"
	"strings"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/workorders"
)

// LimitChecks are what the ledger promises a sign-in's lanes (FR-PARK-4, D10). A worker's half is in
// the worker's tests.
var LimitChecks = []Check{
	{"LM1", "one signal from a worker fires every wake that names its sign-in", checkSignalFiresEveryWake},
	{"LM2", "a worker speaks only for its own sign-in", checkSignalOwnKey},
	{"LM3", "a worker says only what a sign-in can say", checkSignalKinds},
	{"LM4", "a signal that arrives twice fires once", checkSignalRepeat},
	{"LM5", "a wake stored after a signal waits for the next one", checkSignalLater},
	{"LM6", "a time ends a lane's wait with no signal at all", checkUsageWakeTime},
	{"LM7", "a sign-in that comes back fires what waits on it", checkSignInWake},
}

const testDriver = "claude-code"

// usageWakeAheadMs puts a reset past a slow commit, because a server's clock runs on real time.
const usageWakeAheadMs = 60_000

func (s *statusRig) key() string { return parking.SignInKey(s.auth.WorkerID, testDriver) }

// signalUpload is a worker's word on its sign-in, which belongs to no lane and no claim.
func (s *statusRig) signalUpload(kind parking.Kind, key string) (ledger.UploadReply, error) {
	raw, err := json.Marshal(parking.Signal{Kind: kind, Key: key})
	if err != nil {
		return ledger.UploadReply{}, err
	}
	s.seq++
	return s.plane.Upload(s.r.Ctx, ledger.UploadBatch{Auth: s.auth, Epoch: s.epoch, Events: []ledger.UploadEvent{{
		Epoch: s.epoch, Seq: s.seq, Type: ledger.UploadWakeSignal, Payload: raw, OccurredAt: 1,
	}}})
}

// parkedOnLimit parks one more lane of this worker on a limit.
func (s *statusRig) parkedOnLimit(atMs int64) (*statusRig, string, error) {
	other := *s
	other.lane = s.r.ID("lane")
	id, err := s.r.Ready(s.store)
	if err != nil {
		return nil, "", err
	}
	other.grant, err = s.plane.Claim(s.r.Ctx, s.auth, workorders.ClaimRequest{WorkOrder: id, ClaimID: s.r.ID("claim"), TTLMs: TTLMs})
	if err != nil {
		return nil, "", err
	}
	ops := s.r.NewOps(s.store)
	if _, err := ops.Dispatch(s.r.Ctx, s.r.Scope, id, other.lane, s.r.Actor); err != nil {
		return nil, "", err
	}
	if _, err := ops.StartLane(s.r.Ctx, s.r.Scope, other.grant.Lease, other.lane, s.r.Actor); err != nil {
		return nil, "", err
	}
	wake := s.r.ID("wake")
	if err := accepted(other.parkingUpload(wake, lanes.UsageWake(s.key(), atMs))); err != nil {
		return nil, "", err
	}
	s.seq = other.seq
	if err := accepted(other.parkedUpload(wake, true)); err != nil {
		return nil, "", err
	}
	s.seq = other.seq
	return &other, wake, nil
}

func (s *statusRig) wakeFired(wake string, want bool) error {
	stored, err := s.wake(wake)
	if err != nil {
		return err
	}
	if stored.Open() == want {
		return Violatedf("wake %s reads open=%v, want fired=%v", wake, stored.Open(), want)
	}
	return nil
}

func checkSignalFiresEveryWake(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	var wakes []string
	for range 3 {
		_, wake, err := s.parkedOnLimit(0)
		if err != nil {
			return err
		}
		wakes = append(wakes, wake)
	}
	if err := accepted(s.signalUpload(parking.UsageReset, s.key())); err != nil {
		return err
	}
	for _, wake := range wakes {
		if err := s.wakeFired(wake, true); err != nil {
			return err
		}
	}
	return nil
}

// refused reads an upload the ledger answered with a refusal, which leaves the mark moving.
func refused(reply ledger.UploadReply, err error) error {
	if err != nil {
		return err
	}
	if len(reply.Rejected) != 1 || reply.Rejected[0].Status != ledger.UploadInvalid {
		return Violatedf("an upload the ledger took: %+v", reply)
	}
	return nil
}

func checkSignalOwnKey(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	_, wake, err := s.parkedOnLimit(0)
	if err != nil {
		return err
	}
	stranger, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if err := refused(stranger.signalUpload(parking.UsageReset, s.key())); err != nil {
		return err
	}
	// A worker whose ID opens another's must not pass as it.
	if err := refused(stranger.signalUpload(parking.UsageReset, stranger.auth.WorkerID)); err != nil {
		return err
	}
	return s.wakeFired(wake, false)
}

func checkSignalKinds(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	for _, kind := range []parking.Kind{parking.HumanResume, parking.ExternalCheck, parking.QuestionAnswered} {
		if err := refused(s.signalUpload(kind, s.key())); err != nil {
			return Violatedf("%s: %v", kind, err)
		}
	}
	return nil
}

// checkSignalRepeat replays one upload, as a worker does when an answer was lost.
func checkSignalRepeat(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	_, wake, err := s.parkedOnLimit(0)
	if err != nil {
		return err
	}
	if err := accepted(s.signalUpload(parking.UsageReset, s.key())); err != nil {
		return err
	}
	s.seq--
	if err := accepted(s.signalUpload(parking.UsageReset, s.key())); err != nil {
		return err
	}
	if err := s.wakeFired(wake, true); err != nil {
		return err
	}
	events, err := r.Events(s.store, r.Scope)
	if err != nil {
		return err
	}
	signals := 0
	for _, event := range events {
		if event.Type == parking.EventSignal && strings.HasPrefix(event.Subject.ID, string(parking.UsageReset)) {
			signals++
		}
	}
	if signals != 1 {
		return Violatedf("%d signals in the log", signals)
	}
	return nil
}

func checkSignalLater(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if err := accepted(s.signalUpload(parking.UsageReset, s.key())); err != nil {
		return err
	}
	_, wake, err := s.parkedOnLimit(0)
	if err != nil {
		return err
	}
	return s.wakeFired(wake, false)
}

// checkUsageWakeTime fires a limit's wake on its time, which is how a known reset ends one.
func checkUsageWakeTime(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	now, err := r.Clock(s.store)
	if err != nil {
		return err
	}
	_, wake, err := s.parkedOnLimit(now + usageWakeAheadMs)
	if err != nil {
		return err
	}
	if err := s.wakeFired(wake, false); err != nil {
		return err
	}
	r.DB.ShiftClock(usageWakeAheadMs + 1)
	if _, err := r.wakeTick(s.store); err != nil {
		return err
	}
	return s.wakeFired(wake, true)
}

func checkSignInWake(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	wake := r.ID("wake")
	if err := accepted(s.parkingUpload(wake, lanes.SignInWake(s.key()))); err != nil {
		return err
	}
	if err := accepted(s.parkedUpload(wake, true)); err != nil {
		return err
	}
	if err := accepted(s.signalUpload(parking.UsageReset, s.key())); err != nil {
		return err
	}
	if err := s.wakeFired(wake, false); err != nil {
		return err
	}
	if err := accepted(s.signalUpload(parking.SigninRestored, s.key())); err != nil {
		return err
	}
	return s.wakeFired(wake, true)
}
