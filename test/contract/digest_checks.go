package contract

import (
	"bytes"
	"encoding/json"
	"errors"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// DigestChecks are what the ledger promises about a generation's digest (FR-SLOG-2): one record for each owner
// and generation, kept from the first upload, in the project's ledger for a lane and in the installation's for
// an orchestrator.
var DigestChecks = []Check{
	{"DG1", "a lane's digest is kept under its lane and generation", checkDigestKept},
	{"DG2", "a digest uploaded again keeps the first, and no upload is refused for it", checkDigestOnce},
	{"DG3", "a digest passes no fence, so a generation the lane moved past still has one", checkDigestUnfenced},
	{"DG4", "a digest that names no source is refused", checkDigestRefused},
	{"DG5", "an orchestrator's digest is an installation record, kept once", checkOrchestratorDigest},
}

func digestBody(turns int64) ([]byte, error) {
	return json.Marshal(lanes.Digest{
		Source: lanes.DigestStream, Turns: turns, ToolCalls: map[string]int64{"Edit": 2}, FilesWritten: []string{"a.go"},
		StartedAt: 1, EndedAt: 9, Complete: true,
	})
}

func (s *statusRig) digest(generation int64) (ledger.GenerationDigest, error) {
	var out ledger.GenerationDigest
	err := s.store.Read(s.r.Ctx, s.r.Scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Lanes().Digest(ledger.DigestOwnerLane, s.lane, generation)
		return err
	})
	return out, err
}

func checkDigestKept(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	body, err := digestBody(3)
	if err != nil {
		return err
	}
	if err := accepted(s.uploadAt(1, ledger.UploadGenerationDigest, body)); err != nil {
		return err
	}
	kept, err := s.digest(1)
	if err != nil {
		return Violatedf("the digest is not there: %v", err)
	}
	if kept.OwnerKind != ledger.DigestOwnerLane || kept.OwnerID != s.lane || kept.Generation != 1 || kept.RecordedAt == 0 || !bytes.Equal(kept.Body, body) {
		return Violatedf("kept %+v", kept)
	}
	if _, err := s.digest(2); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a generation that sent none has one: %v", err)
	}
	return nil
}

func checkDigestOnce(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	first, err := digestBody(3)
	if err != nil {
		return err
	}
	later, err := digestBody(9)
	if err != nil {
		return err
	}
	for _, body := range [][]byte{first, first, later} {
		if err := accepted(s.uploadAt(1, ledger.UploadGenerationDigest, body)); err != nil {
			return err
		}
	}
	kept, err := s.digest(1)
	if err != nil || !bytes.Equal(kept.Body, first) {
		return Violatedf("a later digest replaced the first: %s, %v", kept.Body, err)
	}
	return nil
}

func checkDigestUnfenced(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if err := s.nextGeneration(2, ledger.StartCrashRestart); err != nil {
		return err
	}
	body, err := digestBody(1)
	if err != nil {
		return err
	}
	if err := accepted(s.uploadAt(1, ledger.UploadGenerationDigest, body)); err != nil {
		return Violatedf("the digest of a generation the lane moved past: %v", err)
	}
	if _, err := s.digest(1); err != nil {
		return Violatedf("the digest is not there: %v", err)
	}
	return nil
}

func checkDigestRefused(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	for _, body := range [][]byte{[]byte(`{"turns":1}`), []byte(`not a digest`)} {
		reply, err := s.uploadAt(1, ledger.UploadGenerationDigest, body)
		if err != nil || len(reply.Rejected) != 1 || reply.Rejected[0].Status != ledger.UploadInvalid {
			return Violatedf("%s: %+v, %v", body, reply, err)
		}
	}
	if _, err := s.digest(1); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a refused digest was kept: %v", err)
	}
	return nil
}

func checkOrchestratorDigest(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	o, err := orchestrating(s)
	if err != nil {
		return err
	}
	body, err := digestBody(4)
	if err != nil {
		return err
	}
	orchestrator := ledger.OrchestratorOf(s.auth.WorkerID)
	send := func(payload []byte) error {
		s.seq++
		return accepted(s.plane.Upload(r.Ctx, ledger.UploadBatch{Auth: s.auth, Epoch: s.epoch, Events: []ledger.UploadEvent{{
			Epoch: s.epoch, Seq: s.seq, Type: ledger.UploadGenerationDigest, Lane: o.call.Lane, Generation: recoveryGeneration,
			Payload: payload, OccurredAt: 1,
		}}}))
	}
	other, err := digestBody(8)
	if err != nil {
		return err
	}
	for _, payload := range [][]byte{body, body, other} {
		if err := send(payload); err != nil {
			return err
		}
	}
	var kept ledger.GenerationDigest
	err = s.store.Installation(r.Scope).Read(r.Ctx, func(tx ports.SharedReadTx) error {
		var err error
		kept, err = tx.Workers().OrchestratorDigest(orchestrator, recoveryGeneration)
		return err
	})
	if err != nil {
		return Violatedf("the orchestrator's digest is not in the installation: %v", err)
	}
	if kept.OwnerKind != ledger.DigestOwnerOrchestrator || kept.OwnerID != orchestrator || kept.Generation != recoveryGeneration || !bytes.Equal(kept.Body, body) {
		return Violatedf("kept %+v", kept)
	}
	if _, err := s.digest(recoveryGeneration); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("an orchestrator's digest is in a project's ledger: %v", err)
	}
	return nil
}
