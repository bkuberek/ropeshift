package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// DelegateChecks are what the ledger promises about delegate credentials and confirmation cards
// (ADR-0024): a credential is kept only as its token's digest and acts only while active, signing out
// everywhere ends every session and credential of a member at once, and a card belongs to no lane, is
// decided by a member alone, and never expires.
var DelegateChecks = []Check{
	{"DL1", "a delegate credential is kept only as its token's digest, and its token is what opens it", checkDelegateDigestOnly},
	{"DL2", "a revoked or expired credential acts no more, and the refusal says which", checkDelegateEnds},
	{"DL3", "only a credential that no longer acts can be removed, and removing it leaves no record", checkDelegateRemoval},
	{"DL4", "signing out everywhere ends each session and revokes each active credential of the member, with one event", checkSignOutEverywhere},
	{"DL5", "a card belongs to no lane, only a member decides it, and no sweep expires it", checkCardIsAMembers},
	{"DL6", "a card whose subject moved goes void when a member confirms it, and its outcome is kept", checkCardBinding},
	{"DL7", "a delegate decides a request only within the delegation policy, and the record names it", checkDelegateDecides},
	{"DL8", "past every gate, a delegate still cannot raise merge authority or move review to a pull request", checkDelegateRepositoryBounds},
	{"DL9", "past every gate, a delegate still cannot decide or answer in a one-way category", checkDelegateOneWayBounds},
	{"DL10", "a card a crash left running past its bound fails, naming why, and a younger one keeps running", checkInterruptedCards},
	{"DL11", "a delegate holds at most a bounded number of open cards in a project", checkOpenCardLimit},
}

var theDelegateRequest = authority.DelegateRequest{Member: theMember.ID, Label: "planning assistant", ExpiresInMs: authority.MaxDelegateTTLMs}

func delegateRequest(r *Rig) authority.DelegateRequest {
	request := theDelegateRequest
	request.Project = r.Scope
	return request
}

func checkDelegateDigestOnly(r *Rig) error {
	s := r.Store()
	delegates := control.NewDelegates(s)
	grant, err := delegates.Create(r.Ctx, r.Scope, delegateRequest(r), theMember)
	if err != nil {
		return err
	}
	if grant.Token == "" || !strings.HasPrefix(grant.Credential.ID, authority.DelegateIDPrefix) {
		return Violatedf("the grant is %+v", grant)
	}
	sum := sha256.Sum256([]byte(grant.Token))
	digest := hex.EncodeToString(sum[:])
	err = s.Installation(r.Scope).Read(r.Ctx, func(tx ports.SharedReadTx) error {
		row, err := tx.Members().DelegateByDigest(digest)
		if err != nil {
			return err
		}
		if row.Digest != digest || row.ID != grant.Credential.ID || row.Member != theMember.ID || row.Project != r.Scope {
			return Violatedf("the row is %+v", row)
		}
		if _, err := tx.Members().DelegateByDigest(grant.Token); !errors.Is(err, ports.ErrNotFound) {
			return Violatedf("the token itself opens a row: %v", err)
		}
		events, err := tx.Events(0, 1000)
		for _, event := range events {
			if strings.Contains(string(event.Data), grant.Token) || strings.Contains(string(event.Data), digest) {
				return Violatedf("event %s holds the credential", event.Type)
			}
		}
		return err
	})
	if err != nil {
		return err
	}
	found, err := delegates.Authenticate(r.Ctx, r.Scope, grant.Token)
	if err != nil || found.ID != grant.Credential.ID {
		return Violatedf("the token opened %+v, %v", found, err)
	}
	if _, err := delegates.Authenticate(r.Ctx, r.Scope, grant.Token+"0"); !errors.Is(err, authority.ErrBadDelegate) {
		return Violatedf("another token opened a credential: %v", err)
	}
	return nil
}

// insertDelegate keeps a credential as a store holds one, for a state no use case makes at once.
func insertDelegate(r *Rig, s ports.Store, credential authority.DelegateCredential) error {
	return s.Installation(r.Scope).Write(r.Ctx, "test.delegate", func(tx ports.SharedTx) error {
		if err := tx.Members().InsertDelegate(credential); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.delegate", Actor: r.Actor, Subject: ledger.Subject{Kind: "delegate", ID: credential.ID}})
		return err
	})
}

func checkDelegateEnds(r *Rig) error {
	s := r.Store()
	delegates := control.NewDelegates(s)
	grant, err := delegates.Create(r.Ctx, r.Scope, delegateRequest(r), theMember)
	if err != nil {
		return err
	}
	if _, err := delegates.Revoke(r.Ctx, r.Scope, grant.Credential.ID, theMember); err != nil {
		return err
	}
	if _, err := delegates.Authenticate(r.Ctx, r.Scope, grant.Token); !errors.Is(err, authority.ErrBadDelegate) || !strings.Contains(err.Error(), "revoked") {
		return Violatedf("a revoked credential: %v", err)
	}
	sum := sha256.Sum256([]byte("an expired token"))
	expired := authority.DelegateCredential{
		ID: authority.DelegateIDPrefix + "expired", Digest: hex.EncodeToString(sum[:]), Member: theMember.ID, Project: r.Scope,
		Label: "old", Kind: authority.DelegateExternal, CreatedAt: 1, ExpiresAt: 2,
	}
	if err := insertDelegate(r, s, expired); err != nil {
		return err
	}
	if _, err := delegates.Authenticate(r.Ctx, r.Scope, "an expired token"); !errors.Is(err, authority.ErrBadDelegate) || !strings.Contains(err.Error(), "expired") {
		return Violatedf("an expired credential: %v", err)
	}
	if _, err := delegates.Authenticate(r.Ctx, r.Scope, "a token nobody made"); !errors.Is(err, authority.ErrBadDelegate) || !strings.Contains(err.Error(), "not known") {
		return Violatedf("an unknown credential: %v", err)
	}
	other := ledger.Actor{Kind: ledger.ActorMember, ID: "member-2"}
	if _, err := delegates.Revoke(r.Ctx, r.Scope, expired.ID, other); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("another member revoked a credential: %v", err)
	}
	return nil
}

func checkDelegateRemoval(r *Rig) error {
	s := r.Store()
	delegates := control.NewDelegates(s)
	grant, err := delegates.Create(r.Ctx, r.Scope, delegateRequest(r), theMember)
	if err != nil {
		return err
	}
	if err := delegates.Remove(r.Ctx, r.Scope, grant.Credential.ID, theMember); !errors.Is(err, authority.ErrDelegateActive) {
		return Violatedf("an active credential was removed: %v", err)
	}
	if _, err := delegates.Revoke(r.Ctx, r.Scope, grant.Credential.ID, theMember); err != nil {
		return err
	}
	if err := delegates.Remove(r.Ctx, r.Scope, grant.Credential.ID, theMember); err != nil {
		return err
	}
	listed, _, err := delegates.List(r.Ctx, r.Scope, theMember)
	if err != nil {
		return err
	}
	if len(listed) != 0 {
		return Violatedf("a removed credential still lists: %+v", listed)
	}
	for _, agent := range append(theAgents, authority.DelegateActor(theMember.ID, grant.Credential.ID)) {
		if _, err := delegates.Create(r.Ctx, r.Scope, delegateRequest(r), agent); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s made a credential: %v", agent.Kind, err)
		}
	}
	return nil
}

func checkSignOutEverywhere(r *Rig) error {
	s := r.Store()
	delegates := control.NewDelegates(s)
	first, err := delegates.Create(r.Ctx, r.Scope, delegateRequest(r), theMember)
	if err != nil {
		return err
	}
	second, err := delegates.Create(r.Ctx, r.Scope, delegateRequest(r), theMember)
	if err != nil {
		return err
	}
	theirs, err := delegates.Create(r.Ctx, r.Scope, authority.DelegateRequest{Member: "member-2", Project: r.Scope, Label: "theirs", ExpiresInMs: authority.MinDelegateTTLMs}, ledger.Actor{Kind: ledger.ActorMember, ID: "member-2"})
	if err != nil {
		return err
	}
	err = s.Installation(r.Scope).Write(r.Ctx, "test.sessions", func(tx ports.SharedTx) error {
		for _, session := range []authority.Session{
			{Digest: "d1", Member: theMember.ID, CreatedAt: tx.Now(), ExpiresAt: tx.Now() + 60_000},
			{Digest: "d2", Member: theMember.ID, CreatedAt: tx.Now(), ExpiresAt: tx.Now() + 60_000},
			{Digest: "d3", Member: "member-2", CreatedAt: tx.Now(), ExpiresAt: tx.Now() + 60_000},
		} {
			if err := tx.Members().InsertSession(session); err != nil {
				return err
			}
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.sessions", Actor: r.Actor, Subject: ledger.Subject{Kind: "member", ID: theMember.ID}})
		return err
	})
	if err != nil {
		return err
	}
	before, err := sharedEvents(r, s)
	if err != nil {
		return err
	}
	ended, err := control.NewSessions(s, nil).EndAll(r.Ctx, r.Scope, theMember)
	if err != nil {
		return err
	}
	if ended.Sessions != 2 || len(ended.Delegates) != 2 {
		return Violatedf("signing out everywhere ended %+v", ended)
	}
	after, err := sharedEvents(r, s)
	if err != nil {
		return err
	}
	if len(after) != len(before)+1 || after[len(after)-1].Actor != theMember {
		return Violatedf("signing out everywhere left %d events", len(after)-len(before))
	}
	err = s.Installation(r.Scope).Read(r.Ctx, func(tx ports.SharedReadTx) error {
		sessions, err := tx.Members().Sessions()
		if err != nil {
			return err
		}
		if len(sessions) != 1 || sessions[0].Member != "member-2" {
			return Violatedf("the sessions left are %+v", sessions)
		}
		now, err := tx.Now()
		if err != nil {
			return err
		}
		for _, id := range []string{first.Credential.ID, second.Credential.ID} {
			row, err := tx.Members().Delegate(id)
			if err != nil {
				return err
			}
			if row.State(now) != authority.DelegateRevoked {
				return Violatedf("credential %s is %s", id, row.State(now))
			}
		}
		row, err := tx.Members().Delegate(theirs.Credential.ID)
		if err != nil {
			return err
		}
		if row.State(now) != authority.DelegateActive {
			return Violatedf("another member's credential is %s", row.State(now))
		}
		return nil
	})
	return err
}

func sharedEvents(r *Rig, s ports.Store) ([]ledger.Event, error) {
	var events []ledger.Event
	err := s.Installation(r.Scope).Read(r.Ctx, func(tx ports.SharedReadTx) error {
		var err error
		events, err = tx.Events(0, 10_000)
		return err
	})
	return events, err
}

// cardDelegate makes the credential of a delegate that prepares cards, and returns who it acts as.
func cardDelegate(r *Rig, s ports.Store) (ledger.Actor, error) {
	grant, err := control.NewDelegates(s).Create(r.Ctx, r.Scope, delegateRequest(r), theMember)
	return grant.Credential.Actor(), err
}

// prepareCard records the card a new delegate's consent call makes, bound as the server binds it.
func prepareCard(r *Rig, s ports.Store, operation, kind, id, arguments string) (authority.Approval, error) {
	actor, err := cardDelegate(r, s)
	if err != nil {
		return authority.Approval{}, err
	}
	return prepareCardAs(r, s, actor, operation, kind, id, arguments)
}

// prepareCardAs records the card one delegate's consent call makes.
func prepareCardAs(r *Rig, s ports.Store, actor ledger.Actor, operation, kind, id, arguments string) (authority.Approval, error) {
	approvals := control.NewApprovals(s)
	binding, err := control.BindingFor(operation, kind, id, json.RawMessage(arguments))
	if err != nil {
		return authority.Approval{}, err
	}
	digest, err := approvals.Bind(r.Ctx, r.Scope, binding)
	if err != nil {
		return authority.Approval{}, err
	}
	detail, err := json.Marshal(authority.CardDetail{
		Operation: operation, Label: "Raise merge authority", Method: "PATCH", Path: "/v1/projects/default/repositories/" + id,
		Arguments: json.RawMessage(arguments), Why: "It passed every review.", Changes: json.RawMessage("[]"),
	})
	if err != nil {
		return authority.Approval{}, err
	}
	return approvals.Prepare(r.Ctx, r.Scope, authority.Approval{
		Subject: binding.Subject(), SubjectDigest: digest, Summary: "Raise merge authority: web", Detail: string(detail),
	}, actor)
}

const raiseArguments = `{"merge_authority":"review_approves"}`

func checkCardIsAMembers(r *Rig) error {
	s := r.Store()
	card, err := prepareCard(r, s, authority.OpRepositoryRaiseMergeAuthority, "repository", "r1", raiseArguments)
	if err != nil {
		return err
	}
	if card.State != authority.ApprovalPending || card.ExpiresAt != 0 || card.Lane != "" || card.WorkOrder != "" {
		return Violatedf("the card is %+v", card)
	}
	approvals := control.NewApprovals(s)
	if expired, err := approvals.ExpireDue(r.Ctx, r.Scope); err != nil || len(expired) != 0 {
		return Violatedf("the sweep expired %v, %v", expired, err)
	}
	decision := authority.Decision{ID: card.ID, Digest: card.SubjectDigest, Allow: true}
	for _, actor := range append(theAgents, authority.DelegateActor(theMember.ID, "dlg_1"), authority.DelegateActor(theMember.ID, "dlg_2")) {
		if _, err := approvals.Confirm(r.Ctx, r.Scope, decision, actor, authority.ChannelCLI); err == nil {
			return Violatedf("%s confirmed a card", actor.Kind)
		}
		if _, err := approvals.Decide(r.Ctx, r.Scope, decision, actor); err == nil {
			return Violatedf("%s decided a card as a request", actor.Kind)
		}
	}
	err = s.Write(r.Ctx, r.Scope, "test.card", func(tx ports.WriteTx) error {
		forged := card
		forged.State, forged.DecidedBy, forged.DecidedKind, forged.DecidedDigest, forged.DecidedAt = authority.ApprovalApproved, "delegate:member-1/dlg_2", string(ledger.ActorDelegate), card.SubjectDigest, 1
		_, err := tx.Approvals().Save(forged, authority.ApprovalPending)
		return err
	})
	if err == nil {
		return Violatedf("the store kept a card a delegate decided")
	}
	row, err := approvals.Get(r.Ctx, r.Scope, card.ID)
	if err != nil {
		return err
	}
	if row.State != authority.ApprovalPending {
		return Violatedf("the refused decisions left the card %s", row.State)
	}
	confirmed, err := approvals.Confirm(r.Ctx, r.Scope, authority.Decision{ID: card.ID, Digest: card.SubjectDigest, Allow: false, Reason: "Not now."}, theMember, authority.ChannelWeb)
	declined := confirmed.Card
	if err != nil || !confirmed.Decided || declined.State != authority.ApprovalDenied || declined.Channel != authority.ChannelWeb || declined.DecidedBy != authority.Principal(theMember) {
		return Violatedf("a member's decline: %+v %v", declined, err)
	}
	return nil
}

func checkCardBinding(r *Rig) error {
	s := r.Store()
	approvals := control.NewApprovals(s)
	card, err := prepareCard(r, s, authority.OpRepositoryRaiseMergeAuthority, "repository", "r1", raiseArguments)
	if err != nil {
		return err
	}
	err = s.Write(r.Ctx, r.Scope, "test.move", func(tx ports.WriteTx) error {
		_, err := tx.Append(ledger.EventDraft{Type: "test.moved", Actor: theMember, Subject: ledger.Subject{Kind: "repository", ID: "r1"}})
		return err
	})
	if err != nil {
		return err
	}
	voided, err := approvals.Confirm(r.Ctx, r.Scope, authority.Decision{ID: card.ID, Digest: card.SubjectDigest, Allow: true}, theMember, authority.ChannelCLI)
	if !errors.Is(err, authority.ErrStaleDigest) || voided.Card.State != authority.ApprovalVoid || voided.Decided {
		return Violatedf("a card whose subject moved: %+v %v", voided, err)
	}
	fresh, err := prepareCard(r, s, authority.OpRepositoryRaiseMergeAuthority, "repository", "r1", raiseArguments)
	if err != nil {
		return err
	}
	if _, err := approvals.Confirm(r.Ctx, r.Scope, authority.Decision{ID: fresh.ID, Digest: "sha256:another", Allow: true}, theMember, authority.ChannelCLI); !errors.Is(err, authority.ErrStaleDigest) {
		return Violatedf("a confirmation naming another digest: %v", err)
	}
	approved, err := approvals.Confirm(r.Ctx, r.Scope, authority.Decision{ID: fresh.ID, Digest: fresh.SubjectDigest, Allow: true}, theMember, authority.ChannelCLI)
	if err != nil || !approved.Decided || approved.Card.State != authority.ApprovalApproved || approved.Card.Channel != authority.ChannelCLI {
		return Violatedf("a member's confirmation: %+v %v", approved, err)
	}
	again, err := approvals.Confirm(r.Ctx, r.Scope, authority.Decision{ID: fresh.ID, Digest: fresh.SubjectDigest, Allow: true}, theMember, authority.ChannelCLI)
	if err != nil || again.Decided || again.Card.State != authority.ApprovalApproved {
		return Violatedf("a repeated confirmation decided again: %+v %v", again, err)
	}
	failed, err := approvals.Finish(r.Ctx, r.Scope, fresh.ID, false, "forbidden: no", theMember)
	if err != nil || failed.State != authority.ApprovalFailed || failed.Reason != "forbidden: no" || failed.DecidedBy != authority.Principal(theMember) {
		return Violatedf("a replay the API refused: %+v %v", failed, err)
	}
	created, err := prepareCard(r, s, authority.OpQuestionRecord, "question", "q9", `{"id": "q9"}`)
	if err != nil {
		return err
	}
	confirmed, err := approvals.Confirm(r.Ctx, r.Scope, authority.Decision{ID: created.ID, Digest: created.SubjectDigest, Allow: true}, theMember, authority.ChannelCLI)
	if err != nil || confirmed.Card.State != authority.ApprovalApproved {
		return Violatedf("a card for a record it creates: %+v %v", confirmed, err)
	}
	used, err := approvals.Finish(r.Ctx, r.Scope, created.ID, true, "", theMember)
	if err != nil || used.State != authority.ApprovalUsed {
		return Violatedf("a replay that ran: %+v %v", used, err)
	}
	return nil
}

func checkDelegateDecides(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	actor := authority.DelegateActor(theMember.ID, "dlg_1")
	outside, err := s.raised(gatedInput)
	if err != nil {
		return err
	}
	if _, err := s.decide(outside, true, actor); !errors.Is(err, authority.ErrNotDelegated) {
		return Violatedf("a delegate decided outside the delegation policy: %v", err)
	}
	if _, err := s.delegating(2); err != nil {
		return err
	}
	inside, err := s.raised(`{"command":"go test ./..."}`)
	if err != nil {
		return err
	}
	decided, err := s.decide(inside, true, actor)
	if err != nil || decided.DecidedKind != string(ledger.ActorDelegate) || decided.DecidedBy != authority.Principal(actor) {
		return Violatedf("a delegate's covered decision: %+v %v", decided, err)
	}
	return nil
}

func checkDelegateRepositoryBounds(r *Rig) error {
	s := r.Store()
	if _, err := r.detect(s, "repo-1", "", "main"); err != nil {
		return err
	}
	actor := authority.DelegateActor(theMember.ID, "dlg_1")
	raised, approves := ledger.ReviewApproves, ledger.PersonApproves
	if _, err := r.change(s, "repo-1", control.RepositoryChange{MergeAuthority: &raised}, actor); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a delegate raised merge authority: %v", err)
	}
	pullRequest, binding := ledger.ReviewOnPullRequest, "github.com/example/repo-1"
	if _, err := r.change(s, "repo-1", control.RepositoryChange{ReviewTarget: &pullRequest, ForgeBinding: &binding}, actor); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a delegate moved review to a pull request: %v", err)
	}
	base := "trunk"
	changed, err := r.change(s, "repo-1", control.RepositoryChange{Base: &base}, actor)
	if err != nil || changed.Base() != "trunk" {
		return Violatedf("a delegate's operational change: %+v %v", changed, err)
	}
	if _, err := r.change(s, "repo-1", control.RepositoryChange{MergeAuthority: &approves}, r.Actor); err != nil {
		return err
	}
	lowered := ledger.PersonMerges
	if changed, err := r.change(s, "repo-1", control.RepositoryChange{MergeAuthority: &lowered}, actor); err != nil || changed.MergeAuthority != ledger.PersonMerges {
		return Violatedf("a delegate lowering merge authority: %+v %v", changed, err)
	}
	return nil
}

func checkDelegateOneWayBounds(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	if _, err := r.publish(s, 2, 5, questions.CategorySecurity); err != nil {
		return err
	}
	actor := authority.DelegateActor(theMember.ID, "dlg_1")
	questionsOps := control.NewQuestions(s, questions.BatchPolicy{})
	oneWay := questions.Decision{ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Keep audit data in the region.", Categories: []string{questions.CategorySecurity}}
	if _, err := questionsOps.RecordDecision(r.Ctx, r.Scope, oneWay, actor); !errors.Is(err, questions.ErrOneWay) {
		return Violatedf("a delegate decided in a one-way category: %v", err)
	}
	standing, err := questionsOps.Weigh(r.Ctx, r.Scope, oneWay)
	if err != nil || !standing.OneWay || standing.WithinPolicy {
		return Violatedf("a one-way decision weighs %+v, %v", standing, err)
	}
	reversible := questions.Decision{ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Retry twice.", Categories: []string{questions.CategoryProduct}}
	if standing, err := questionsOps.Weigh(r.Ctx, r.Scope, reversible); err != nil || standing.OneWay {
		return Violatedf("a reversible decision weighs %+v, %v", standing, err)
	}
	if _, err := questionsOps.RecordDecision(r.Ctx, r.Scope, reversible, actor); err != nil {
		return Violatedf("a delegate's exempt decision outside every one-way category: %v", err)
	}
	asked, err := r.ask(s, "May audit data leave the region?", "lane-a", func(a *questions.Ask) { a.Categories = []string{questions.CategorySecurity} })
	if err != nil {
		return err
	}
	if answering, err := questionsOps.AnswerStanding(r.Ctx, r.Scope, asked.QuestionID); err != nil || !answering {
		return Violatedf("a one-way question reads as %v, %v", answering, err)
	}
	if _, err := ops.Answer(r.Ctx, r.Scope, asked.QuestionID, questions.Answer{DecisionID: r.ID("d"), Statement: "Never."}, actor); !errors.Is(err, questions.ErrOneWay) {
		return Violatedf("a delegate answered a one-way question: %v", err)
	}
	if _, err := ops.Answer(r.Ctx, r.Scope, asked.QuestionID, questions.Answer{DecisionID: r.ID("d"), Statement: "Never."}, theMember); err != nil {
		return Violatedf("the same answer from a member: %v", err)
	}
	return nil
}

func checkInterruptedCards(r *Rig) error {
	s := r.Store()
	approvals := control.NewApprovals(s)
	card, err := prepareCard(r, s, authority.OpRepositoryRaiseMergeAuthority, "repository", "r1", raiseArguments)
	if err != nil {
		return err
	}
	if _, err := approvals.Confirm(r.Ctx, r.Scope, authority.Decision{ID: card.ID, Digest: card.SubjectDigest, Allow: true}, theMember, authority.ChannelCLI); err != nil {
		return err
	}
	if failed, err := approvals.FailInterrupted(r.Ctx, r.Scope, 60*60*1000); err != nil || len(failed) != 0 {
		return Violatedf("a card approved a moment ago was taken for interrupted: %v %v", failed, err)
	}
	failed, err := approvals.FailInterrupted(r.Ctx, r.Scope, 0)
	if err != nil || len(failed) != 1 || failed[0] != card.ID {
		return Violatedf("the sweep failed %v: %v", failed, err)
	}
	row, err := approvals.Get(r.Ctx, r.Scope, card.ID)
	if err != nil {
		return err
	}
	if row.State != authority.ApprovalFailed || row.Reason != "interrupted; check the audit" || row.DecidedBy != authority.Principal(theMember) {
		return Violatedf("the interrupted card is %+v", row)
	}
	if again, err := approvals.FailInterrupted(r.Ctx, r.Scope, 0); err != nil || len(again) != 0 {
		return Violatedf("a second sweep failed %v: %v", again, err)
	}
	return nil
}

func checkOpenCardLimit(r *Rig) error {
	s := r.Store()
	actor, err := cardDelegate(r, s)
	if err != nil {
		return err
	}
	var first authority.Approval
	for i := range control.MaxOpenCards {
		card, err := prepareCardAs(r, s, actor, authority.OpRepositoryRaiseMergeAuthority, "repository", fmt.Sprintf("r%d", i), raiseArguments)
		if err != nil {
			return Violatedf("card %d: %v", i+1, err)
		}
		if i == 0 {
			first = card
		}
	}
	if _, err := prepareCard(r, s, authority.OpRepositoryRaiseMergeAuthority, "repository", "another", raiseArguments); err != nil {
		return Violatedf("another delegate's card: %v", err)
	}
	if _, err := prepareCardAs(r, s, actor, authority.OpRepositoryRaiseMergeAuthority, "repository", "one-more", raiseArguments); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a card past the limit: %v", err)
	}
	if _, err := control.NewApprovals(s).Confirm(r.Ctx, r.Scope, authority.Decision{ID: first.ID, Digest: first.SubjectDigest, Allow: false}, theMember, authority.ChannelCLI); err != nil {
		return err
	}
	if _, err := prepareCardAs(r, s, actor, authority.OpRepositoryRaiseMergeAuthority, "repository", "one-more", raiseArguments); err != nil {
		return Violatedf("a card once a member declined one: %v", err)
	}
	return nil
}
