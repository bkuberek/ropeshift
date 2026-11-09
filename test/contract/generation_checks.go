package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// GenerationChecks are what the ledger promises about generations and handoffs (FR-LANE-9,
// FR-LANE-10).
var GenerationChecks = []Check{
	{"G1", "a lane's first generation starts as initial", checkFirstGeneration},
	{"G2", "each next generation says why it began", checkStartReasons},
	{"G3", "a handoff stays in the ledger under its digest", checkHandoffStored},
	{"G4", "a handoff that does not match its digest is refused", checkHandoffDigest},
	{"G5", "a generation that moved on cannot store a handoff", checkHandoffStale},
	{"G6", "one session name can span generations", checkSessionSpans},
	{"G7", "a lane's end closes its generation", checkGenerationEnds},
	{"G8", "a storing handoff replays", checkHandoffReplays},
}

func (s *statusRig) generations() ([]ledger.Generation, error) {
	var out []ledger.Generation
	err := s.store.Read(s.r.Ctx, s.r.Scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Lanes().Generations(s.lane)
		return err
	})
	return out, err
}

func (s *statusRig) storedHandoff(generation int64) (ledger.HandoffRecord, error) {
	var out ledger.HandoffRecord
	err := s.store.Read(s.r.Ctx, s.r.Scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Lanes().Handoff(s.lane, generation)
		return err
	})
	return out, err
}

func (s *statusRig) uploadAt(generation int64, eventType string, payload []byte) (ledger.UploadReply, error) {
	s.seq++
	return s.plane.Upload(s.r.Ctx, ledger.UploadBatch{Auth: s.auth, Epoch: s.epoch, Events: []ledger.UploadEvent{{
		Epoch: s.epoch, Seq: s.seq, Type: eventType, WorkOrder: s.grant.WorkOrder, ClaimID: s.grant.ClaimID,
		Fence: s.grant.Fence, Lane: s.lane, Generation: generation, Payload: payload, OccurredAt: 1,
	}}})
}

func (s *statusRig) nextGeneration(number int64, reason ledger.StartReason) error {
	raw, err := json.Marshal(ledger.GenerationStart{Reason: reason})
	if err != nil {
		return err
	}
	reply, err := s.uploadAt(number, ledger.UploadGenerationStarted, raw)
	if err != nil {
		return err
	}
	if len(reply.Rejected) != 0 {
		return Violatedf("generation %d refused: %+v", number, reply.Rejected)
	}
	return nil
}

func handoffBody(body string) (ledger.HandoffUpload, []byte, error) {
	sum := sha256.Sum256([]byte(body))
	upload := ledger.HandoffUpload{Body: []byte(body), Digest: "sha256:" + hex.EncodeToString(sum[:])}
	raw, err := json.Marshal(upload)
	return upload, raw, err
}

func checkFirstGeneration(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	generations, err := s.generations()
	if err != nil {
		return err
	}
	if len(generations) != 1 || generations[0].Number != 1 || generations[0].StartReason != ledger.StartInitial || generations[0].StartedAt == 0 {
		return Violatedf("generations %+v", generations)
	}
	return nil
}

func checkStartReasons(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if err := s.nextGeneration(2, ledger.StartCrashRestart); err != nil {
		return err
	}
	_, raw, err := handoffBody(`{"schema":"x"}`)
	if err != nil {
		return err
	}
	if reply, err := s.uploadAt(2, ledger.UploadHandoffStored, raw); err != nil || len(reply.Rejected) != 0 {
		return Violatedf("a handoff: %+v, %v", reply, err)
	}
	if err := s.nextGeneration(3, ledger.StartHandoff); err != nil {
		return err
	}
	generations, err := s.generations()
	if err != nil {
		return err
	}
	if len(generations) != 3 || generations[1].StartReason != ledger.StartCrashRestart || generations[2].StartReason != ledger.StartHandoff {
		return Violatedf("generations %+v", generations)
	}
	if generations[0].EndReason != string(ledger.StartCrashRestart) || generations[0].EndedAt == 0 {
		return Violatedf("the first generation %+v", generations[0])
	}
	if generations[2].HandoffIn == "" || generations[2].HandoffIn != generations[1].HandoffOut {
		return Violatedf("the handoff links %+v", generations)
	}
	return nil
}

func checkHandoffStored(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	upload, _, err := handoffBody(`{"schema":"ropeshift.handoff/v1"}`)
	if err != nil {
		return err
	}
	upload.Degraded = true
	raw, err := json.Marshal(upload)
	if err != nil {
		return err
	}
	if reply, err := s.uploadAt(1, ledger.UploadHandoffStored, raw); err != nil || len(reply.Rejected) != 0 {
		return Violatedf("a handoff: %+v, %v", reply, err)
	}
	record, err := s.storedHandoff(1)
	if err != nil {
		return err
	}
	if string(record.Body) != string(upload.Body) || record.Digest != upload.Digest || !record.Degraded || record.ArtifactID == "" {
		return Violatedf("record %+v", record)
	}
	view, err := s.plane.Lane(r.Ctx, s.lane)
	if err != nil {
		return err
	}
	if len(view.Artifacts) != 1 || view.Artifacts[0].Kind != ledger.ArtifactHandoff || view.Artifacts[0].Digest != upload.Digest {
		return Violatedf("artifacts %+v", view.Artifacts)
	}
	if view.Artifacts[0].URI != ledger.HandoffURI(s.lane, 1) || view.Artifacts[0].Fence != s.grant.Fence {
		return Violatedf("artifact %+v", view.Artifacts[0])
	}
	return nil
}

func checkHandoffDigest(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	upload, _, err := handoffBody(`{"schema":"x"}`)
	if err != nil {
		return err
	}
	upload.Body = []byte(`{"schema":"changed"}`)
	raw, err := json.Marshal(upload)
	if err != nil {
		return err
	}
	reply, err := s.uploadAt(1, ledger.UploadHandoffStored, raw)
	if err != nil {
		return err
	}
	if len(reply.Rejected) != 1 || reply.Rejected[0].Status != ledger.UploadInvalid {
		return Violatedf("a changed handoff: %+v", reply)
	}
	if _, err := s.storedHandoff(1); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a changed handoff was kept: %v", err)
	}
	return nil
}

func checkHandoffStale(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if err := s.nextGeneration(2, ledger.StartHandoff); err != nil {
		return err
	}
	_, raw, err := handoffBody(`{"schema":"x"}`)
	if err != nil {
		return err
	}
	reply, err := s.uploadAt(1, ledger.UploadHandoffStored, raw)
	if err != nil {
		return err
	}
	if len(reply.Rejected) != 1 || reply.Rejected[0].Status != ledger.UploadFenced {
		return Violatedf("a late handoff: %+v", reply)
	}
	return nil
}

func checkSessionSpans(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	bind := func(generation int64) error {
		raw, err := json.Marshal(workorders.GenerationBinding{SessionID: "session-1"})
		if err != nil {
			return err
		}
		reply, err := s.uploadAt(generation, ledger.UploadGenerationBound, raw)
		if err != nil {
			return err
		}
		if len(reply.Rejected) != 0 {
			return Violatedf("a binding refused: %+v", reply)
		}
		return nil
	}
	if err := bind(1); err != nil {
		return err
	}
	if err := s.nextGeneration(2, ledger.StartCrashRestart); err != nil {
		return err
	}
	if err := bind(2); err != nil {
		return err
	}
	generations, err := s.generations()
	if err != nil {
		return err
	}
	if len(generations) != 2 || generations[0].SessionID != "session-1" || generations[1].SessionID != "session-1" {
		return Violatedf("generations %+v", generations)
	}
	return nil
}

func checkGenerationEnds(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	holder := workorders.Holder{Lease: s.grant.Lease, Lane: s.lane, Generation: 1}
	result := workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeFailed, Reason: "crashed"}}
	if err := r.NewOps(s.store).EndLane(r.Ctx, r.Scope, holder, result, r.Actor); err != nil {
		return err
	}
	generations, err := s.generations()
	if err != nil {
		return err
	}
	if len(generations) != 1 || generations[0].EndReason != string(workorders.OutcomeFailed) || generations[0].EndedAt == 0 {
		return Violatedf("generations %+v", generations)
	}
	return nil
}

func checkHandoffReplays(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	_, raw, err := handoffBody(`{"schema":"x"}`)
	if err != nil {
		return err
	}
	if _, err := s.uploadAt(1, ledger.UploadHandoffStored, raw); err != nil {
		return err
	}
	if err := s.nextGeneration(2, ledger.StartHandoff); err != nil {
		return err
	}
	if err := r.CheckRevisions(s.store, r.Scope); err != nil {
		return err
	}
	return r.CheckProjection(s.store)
}
