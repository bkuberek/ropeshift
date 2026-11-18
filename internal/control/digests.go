package control

import (
	"encoding/json"
	"fmt"

	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// maxDigestBytes bounds a digest a worker uploads. A real one is a few kilobytes: counts by tool name and at
// most two hundred paths.
const maxDigestBytes = 1 << 20

// checkedDigest reads what a worker sent as a generation's digest, and refuses what is not one. The ledger
// keeps the digest as it came.
func checkedDigest(payload []byte) (json.RawMessage, error) {
	if len(payload) == 0 || len(payload) > maxDigestBytes {
		return nil, fmt.Errorf("%w: a digest is between 1 byte and %d", workorders.ErrInvalid, maxDigestBytes)
	}
	var digest lanes.Digest
	if err := json.Unmarshal(payload, &digest); err != nil {
		return nil, fmt.Errorf("%w: a digest: %v", workorders.ErrInvalid, err)
	}
	if digest.Source != lanes.DigestStream && digest.Source != lanes.DigestHooks {
		return nil, fmt.Errorf("%w: a digest names the source %q", workorders.ErrInvalid, digest.Source)
	}
	return json.RawMessage(payload), nil
}

// digested is the event that says a digest was kept. It holds none of the digest, which its table does.
func digested(e ledger.UploadEvent) ledger.UploadEvent {
	e.Payload = nil
	return e
}

// digestUpload keeps a lane's digest of one generation, once. It passes no fence, because what a generation did
// stays true after its lane moved on, and it holds to the worker that ran the lane, as a guard's report does.
func digestUpload(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	body, err := checkedDigest(e.Payload)
	if err != nil {
		return err
	}
	if err := ranLane(tx, worker, e); err != nil {
		return err
	}
	err = tx.Lanes().SaveDigest(ledger.GenerationDigest{
		OwnerKind: ledger.DigestOwnerLane, OwnerID: e.Lane, Generation: e.Generation, Body: body,
	})
	if err != nil {
		return err
	}
	return appendLaneEvent(tx, worker, id, ledger.EventGenerationDigested, digested(e))
}

// assistantDigest keeps the digest of one generation of an assistant's session under its conversation. The
// session's run counts its generations from one, and the conversation counts them across its runs.
func assistantDigest(tx ports.WriteTx, worker, id string, record conversations.Conversation, e ledger.UploadEvent) error {
	body, err := checkedDigest(e.Payload)
	if err != nil {
		return err
	}
	boundary, _, err := runBoundary(tx.Conversations(), record)
	if err != nil {
		return err
	}
	err = tx.Lanes().SaveDigest(ledger.GenerationDigest{
		OwnerKind: ledger.DigestOwnerAssistant, OwnerID: record.ID, Generation: boundary.Generation + e.Generation - 1, Body: body,
	})
	if err != nil {
		return err
	}
	return appendLaneEvent(tx, worker, id, ledger.EventGenerationDigested, digested(e))
}

// orchestratorDigest keeps the digest of one generation of an orchestrator in the installation, because an
// orchestrator serves every project on its machine.
func orchestratorDigest(tx ports.SharedTx, worker, id string, orchestrator ledger.Orchestrator, e ledger.UploadEvent) error {
	body, err := checkedDigest(e.Payload)
	if err != nil {
		return err
	}
	err = tx.Workers().SaveOrchestratorDigest(ledger.GenerationDigest{
		OwnerKind: ledger.DigestOwnerOrchestrator, OwnerID: orchestrator.ID, Generation: e.Generation, Body: body,
	})
	if err != nil {
		return err
	}
	_, err = tx.Append(ledger.EventDraft{
		ID: id, Type: ledger.EventGenerationDigested, OccurredAt: e.OccurredAt,
		Actor:   ledger.Actor{Kind: ledger.ActorWorker, ID: worker},
		Subject: ledger.Subject{Kind: ledger.SubjectOrchestrator, ID: orchestrator.ID},
	})
	return err
}
