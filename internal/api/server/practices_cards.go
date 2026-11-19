package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
)

// An acknowledgement's card names the repository's finding it binds to, so a card goes void once another
// acknowledgement or withdrawal of that finding happens.

// practicesFinding reads the repository and the finding a body names, which the two routes share.
func practicesFinding(body []byte) (repository, finding string) {
	var in api.PracticesWithdraw
	if err := json.Unmarshal(body, &in); err != nil {
		return "", ""
	}
	return in.Repository, in.Finding
}

func practicesFindingSubject(_ *handler, _ *http.Request, body []byte) string {
	repository, finding := practicesFinding(body)
	return repository + ":" + finding
}

// practicesRepositoryAsID says an acknowledgement's or a withdrawal's body by the repository's identity, as the card
// binds to, shows, and replays it, so a rename or a new prefix since the card was prepared cannot change which
// repository it names.
func practicesRepositoryAsID(h *handler, r *http.Request, body []byte) ([]byte, error) {
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		return body, nil
	}
	named, _ := fields["repository"].(string)
	if named == "" {
		return body, nil
	}
	id, err := h.acknowledgedRepository(r, named)
	if err != nil {
		return nil, err
	}
	fields["repository"] = id
	return json.Marshal(fields)
}

// practicesAcknowledgeCheck refuses a call that cannot make an acknowledgement before it becomes a card.
func practicesAcknowledgeCheck(body []byte) error {
	var in api.PracticesAcknowledge
	if err := json.Unmarshal(body, &in); err != nil {
		return fmt.Errorf("%w: %v", authority.ErrInvalid, err)
	}
	if err := control.CheckPracticesFinding(in.Finding); err != nil {
		return err
	}
	_, err := control.CheckPracticesReason(in.Reason)
	return err
}

// practicesWithdrawCheck refuses a call that cannot name an acknowledgement before it becomes a card.
func practicesWithdrawCheck(body []byte) error {
	var in api.PracticesWithdraw
	if err := json.Unmarshal(body, &in); err != nil {
		return fmt.Errorf("%w: %v", authority.ErrInvalid, err)
	}
	return control.CheckPracticesFinding(in.Finding)
}

// practicesWhy is the table's words for why the practice a finding belongs to matters.
func practicesWhy(finding string) string {
	id, err := practices.ParseFindingID(finding)
	if err != nil {
		return ""
	}
	practice, _ := practices.Lookup(id)
	return practice.Why
}

// practicesAcknowledgeChanges is what a card for an acknowledgement shows a member: the finding, why its practice
// matters, and the reason it would be left open for.
func practicesAcknowledgeChanges(_ *handler, _ *http.Request, body []byte) (any, error) {
	var in api.PracticesAcknowledge
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("%w: %v", authority.ErrInvalid, err)
	}
	reason, err := control.CheckPracticesReason(in.Reason)
	if err != nil {
		return nil, err
	}
	return []authority.FieldChange{
		{Field: "finding", To: in.Finding}, {Field: "why", To: practicesWhy(in.Finding)}, {Field: "reason", To: reason},
	}, nil
}

// practicesWithdrawChanges is what a card for a withdrawal shows: the finding, why its practice matters, and the
// reason that stops holding. A finding with no acknowledgement has no card to show.
func practicesWithdrawChanges(h *handler, r *http.Request, body []byte) (any, error) {
	var in api.PracticesWithdraw
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("%w: %v", authority.ErrInvalid, err)
	}
	repository, err := h.acknowledgedRepository(r, in.Repository)
	if err != nil {
		return nil, err
	}
	var ack ledger.PracticesAcknowledgement
	err = h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) (err error) {
		ack, err = tx.Practices().Acknowledgement(repository, in.Finding)
		return err
	})
	if err != nil {
		return nil, err
	}
	return []authority.FieldChange{
		{Field: "finding", To: in.Finding}, {Field: "why", To: practicesWhy(in.Finding)}, {Field: "reason", From: ack.Reason},
	}, nil
}

// practicesFindingName is how a card's title names a finding: the finding, and the repository by its name.
func (h *handler) practicesFindingName(ctx context.Context, id string) string {
	repository, finding, ok := strings.Cut(id, ":")
	if !ok {
		return id
	}
	var name string
	err := h.Store.Read(ctx, h.Scope, func(tx ports.ReadTx) error {
		record, err := tx.Repositories().Repository(repository)
		name = record.Name
		return err
	})
	if err != nil || name == "" {
		name = repository
	}
	return finding + " in " + name
}
