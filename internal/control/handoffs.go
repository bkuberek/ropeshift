package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// startReason reads why a worker began a generation. An event with no payload is a crash
// restart, which is what every journal held before handoffs.
func startReason(e ledger.UploadEvent) ledger.StartReason {
	var start ledger.GenerationStart
	if len(e.Payload) == 0 || json.Unmarshal(e.Payload, &start) != nil || start.Reason == "" {
		return ledger.StartCrashRestart
	}
	return start.Reason
}

// bindGeneration records what the vendor calls a generation's session. Nothing is unique on
// that name, because a resume keeps it across generations.
func bindGeneration(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	var binding workorders.GenerationBinding
	if err := json.Unmarshal(e.Payload, &binding); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	draft, err := uploadedDraft(tx, worker, id, e)
	if err != nil {
		return err
	}
	if err := tx.Lanes().BindSession(e.Lane, e.Generation, binding.SessionID); err != nil {
		return err
	}
	if binding.FellBack {
		if err := fellBack(tx, e); err != nil {
			return err
		}
	}
	if err := tx.Lanes().ReadPack(e.Lane, e.Generation, binding.PackID, binding.PackVersion); err != nil {
		return err
	}
	if err := carryMessages(tx, worker, e.Lane, e.Generation, binding.Carried); err != nil {
		return err
	}
	_, err = tx.Append(draft)
	return err
}

// fellBack corrects a generation that began as a resume and started from its handoff, because a
// start reason is a fact an experience record reads.
func fellBack(tx ports.WriteTx, e ledger.UploadEvent) error {
	left, err := tx.Lanes().Handoff(e.Lane, e.Generation-1)
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		return err
	}
	return tx.Lanes().FellBack(e.Lane, e.Generation, left.ArtifactID)
}

// respondGeneration stamps a generation's last model response with database time, when this
// report arrives. It can run late by the time the report took, and never early.
func respondGeneration(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	draft, err := uploadedDraft(tx, worker, id, e)
	if err != nil {
		return err
	}
	if err := tx.Lanes().Responded(e.Lane, e.Generation); err != nil {
		return err
	}
	_, err = tx.Append(draft)
	return err
}

// recordBaseline keeps what a generation held at its first call, which is what a pack's size is
// judged by and what the ratio of the next estimate follows.
func recordBaseline(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	var baseline ledger.GenerationBaseline
	if err := json.Unmarshal(e.Payload, &baseline); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	draft, err := uploadedDraft(tx, worker, id, e)
	if err != nil {
		return err
	}
	if err := tx.Lanes().Baseline(e.Lane, e.Generation, baseline.Tokens); err != nil {
		return err
	}
	_, err = tx.Append(draft)
	return err
}

// storeHandoff keeps a generation's document in the ledger, so a later generation on another
// machine can read it. It holds to the writer's fence and generation, and the digest is
// checked here, because the ledger is what a later generation trusts.
func storeHandoff(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	var upload ledger.HandoffUpload
	if err := json.Unmarshal(e.Payload, &upload); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	sum := sha256.Sum256(upload.Body)
	if upload.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return fmt.Errorf("%w: a handoff does not match its digest", workorders.ErrInvalid)
	}
	e.Payload = nil
	draft, err := uploadedDraft(tx, worker, id, e)
	if err != nil {
		return err
	}
	artifact := ledger.Artifact{
		ID: id, Lane: e.Lane, Kind: ledger.ArtifactHandoff, URI: ledger.HandoffURI(e.Lane, e.Generation), Digest: upload.Digest, Fence: e.Fence,
	}
	if err := tx.Lanes().AddArtifact(artifact); err != nil {
		return err
	}
	err = tx.Lanes().SaveHandoff(ledger.HandoffRecord{
		ArtifactID: id, Lane: e.Lane, Generation: e.Generation, Digest: upload.Digest, Degraded: upload.Degraded, Body: upload.Body,
	})
	if err != nil {
		return err
	}
	_, err = tx.Append(draft)
	return err
}

// LaneFacts reads what a handoff states and only the ledger knows: what a lane still waits
// on, and how its checks stand. It holds to the same test as a question, because a lane
// reaches the ledger only through the worker that holds it.
func (w *Workers) LaneFacts(ctx context.Context, request ledger.LaneFactsRequest) (ledger.LaneFacts, error) {
	facts := ledger.LaneFacts{OpenQuestions: []string{}, OpenAssumptions: []string{}, Checks: []ledger.CheckState{}}
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		if err := w.checkSession(tx, request.Auth, request.Epoch); err != nil {
			return err
		}
		order, err := tx.WorkOrders().Get(request.WorkOrder)
		if err != nil {
			return err
		}
		if order.LeaseWorker != request.Auth.WorkerID || order.Fence != request.Fence {
			return &workorders.FencedError{Current: order.Fence, State: order.State}
		}
		if facts.OpenQuestions, err = openQuestions(tx, request.Lane); err != nil {
			return err
		}
		if facts.OpenAssumptions, err = tx.Questions().OpenAssumptions(request.WorkOrder); err != nil {
			return err
		}
		checks, err := tx.Evidence().Attempt(request.WorkOrder, request.Fence)
		if err != nil {
			return err
		}
		for _, check := range checks {
			facts.Checks = append(facts.Checks, ledger.CheckState{Name: check.Name, Status: string(check.Status)})
		}
		claim, err := tx.Claims().Find(order.ClaimID)
		switch {
		case err == nil:
			if facts.Pack, err = desiredPack(tx.Packs(), claim.PackID); err != nil {
				return err
			}
		case !errors.Is(err, ports.ErrNotFound):
			return err
		}
		generation, err := tx.Lanes().Generation(request.Lane)
		if errors.Is(err, ports.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		facts.Facts, err = messageFacts(tx.Messages(), request.Lane, generation-1)
		return err
	})
	if err != nil {
		return ledger.LaneFacts{}, err
	}
	return facts, nil
}

// openQuestions lists what a lane asked and has no answer to.
func openQuestions(tx ports.ReadTx, lane string) ([]string, error) {
	attached, err := tx.Questions().Attachments("")
	if err != nil {
		return nil, err
	}
	open := []string{}
	for _, attachment := range attached {
		if attachment.Lane != lane {
			continue
		}
		question, err := tx.Questions().Question(attachment.QuestionID)
		if err != nil {
			return nil, err
		}
		if question.Status.Unresolved() {
			open = append(open, question.ID)
		}
	}
	return open, nil
}
