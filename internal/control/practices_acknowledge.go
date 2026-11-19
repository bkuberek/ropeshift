package control

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
	"ropeshift.com/ropeshift/internal/workorders"
)

// maxPracticesFinding is the most bytes of a finding ID an acknowledgement names.
const maxPracticesFinding = 1024

// CheckPracticesFinding refuses a finding ID the practice table's grammar does not allow, one over 1 KiB, and one
// with a control character, which a card and a view would print on a line of their own.
func CheckPracticesFinding(finding string) error {
	if len(finding) > maxPracticesFinding {
		return invalidPractices("a finding ID is at most %d bytes", maxPracticesFinding)
	}
	if strings.ContainsFunc(finding, unicode.IsControl) {
		return invalidPractices("a finding ID holds no control character")
	}
	if _, err := practices.ParseFindingID(finding); err != nil {
		return invalidPractices("%v", err)
	}
	return nil
}

// CheckPracticesReason reads the reason for an acknowledgement as it is kept: with the shapes of credentials taken out,
// because the assistant's reason is model text, and on one line with no control character, because a card, a view, and
// a terminal print it. It refuses an empty reason and one over ledger.MaxPracticesReason bytes as given, and holds the
// result to that bound, because a marker can be longer than what it replaces. Redaction comes first, because a
// credential can span lines.
func CheckPracticesReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	switch {
	case reason == "":
		return "", invalidPractices("an acknowledgement needs a reason")
	case len(reason) > ledger.MaxPracticesReason:
		return "", invalidPractices("an acknowledgement's reason is at most %d bytes", ledger.MaxPracticesReason)
	}
	flat := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, ledger.RedactCredentials(reason))
	return clipText(strings.Join(strings.Fields(flat), " "), ledger.MaxPracticesReason), nil
}

// Acknowledge records a member's reason for leaving a repository's finding open (FR-PRAC-10), by the repository's ID
// and the finding's ID. The finding need not be in the repository's latest result: a check may not have run since it
// appeared. It refuses an empty reason, one over ledger.MaxPracticesReason bytes, an ID the finding grammar refuses,
// a finding already acknowledged, and a repository the project removed. The acknowledgement is one event, practices.acknowledged. It changes nothing
// any decision reads: a finding blocks nothing, acknowledged or not.
func (p *Practices) Acknowledge(ctx context.Context, scope ledger.WorkspaceID, repository, finding, reason string, actor ledger.Actor) (ledger.PracticesAcknowledgement, error) {
	if !authority.Allowed(actor, authority.ActionAcknowledgePractices) {
		return ledger.PracticesAcknowledgement{}, workorders.ErrForbidden
	}
	if err := CheckPracticesFinding(finding); err != nil {
		return ledger.PracticesAcknowledgement{}, err
	}
	reason, err := CheckPracticesReason(reason)
	if err != nil {
		return ledger.PracticesAcknowledgement{}, err
	}
	var ack ledger.PracticesAcknowledgement
	err = p.store.Write(ctx, scope, "practices.acknowledge", func(tx ports.WriteTx) error {
		record, err := tx.Repositories().Repository(repository)
		if err != nil {
			return err
		}
		if record.Removed() {
			return fmt.Errorf("repository %s was removed: %w", repository, ports.ErrNotFound)
		}
		ack = ledger.PracticesAcknowledgement{Repository: repository, Finding: finding, Reason: reason, Member: actor.ID, AcknowledgedAt: tx.Now()}
		added, err := tx.Practices().Acknowledge(ack)
		if err != nil {
			return err
		}
		if !added {
			return fmt.Errorf("%w: %s of repository %s", ledger.ErrAcknowledged, finding, repository)
		}
		data, err := json.Marshal(map[string]any{
			"finding": finding, "repository": repository, "reason": reason, "member": ack.Member, "acknowledged_at": ack.AcknowledgedAt,
		})
		if err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{
			Type: ledger.EventPracticesAcknowledged, Actor: actor, Subject: ledger.PracticesFindingSubject(repository, finding), Data: data,
		})
		return err
	})
	return ack, err
}

// Withdraw removes a repository's acknowledgement of a finding, so the finding reads as open again, and answers the
// acknowledgement it removed. It refuses a finding with none. The withdrawal is one event, practices.withdrawn, which
// holds the reason that was withdrawn and who gave it.
func (p *Practices) Withdraw(ctx context.Context, scope ledger.WorkspaceID, repository, finding string, actor ledger.Actor) (ledger.PracticesAcknowledgement, error) {
	if !authority.Allowed(actor, authority.ActionWithdrawPractices) {
		return ledger.PracticesAcknowledgement{}, workorders.ErrForbidden
	}
	if err := CheckPracticesFinding(finding); err != nil {
		return ledger.PracticesAcknowledgement{}, err
	}
	var ack ledger.PracticesAcknowledgement
	err := p.store.Write(ctx, scope, "practices.withdraw", func(tx ports.WriteTx) error {
		var err error
		if ack, err = tx.Practices().Acknowledgement(repository, finding); err != nil {
			return err
		}
		if _, err := tx.Practices().Withdraw(repository, finding); err != nil {
			return err
		}
		data, err := json.Marshal(map[string]any{
			"finding": finding, "repository": repository, "reason": ack.Reason, "member": actor.ID,
			"acknowledged_by": ack.Member, "acknowledged_at": ack.AcknowledgedAt, "withdrawn_at": tx.Now(),
		})
		if err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{
			Type: ledger.EventPracticesWithdrawn, Actor: actor, Subject: ledger.PracticesFindingSubject(repository, finding), Data: data,
		})
		return err
	})
	return ack, err
}
