package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/BurntSushi/toml"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// delegatePolicyTable names the table of a delegate policy.
const delegatePolicyTable = "delegate_policy"

// delegatePolicyKeys are the settings a delegate policy must state. A list left out would read as one
// nobody chose.
var delegatePolicyKeys = []string{"version", "exempt"}

// readDelegatePolicy reads a delegate policy as a member wrote it, on the terms of every other policy.
func readDelegatePolicy(body string) (authority.DelegatePolicyDraft, error) {
	var file authority.DelegatePolicyFile
	meta, err := toml.Decode(body, &file)
	if err != nil {
		return authority.DelegatePolicyDraft{}, fmt.Errorf("%w: %v", authority.ErrInvalid, err)
	}
	if unknown := meta.Undecoded(); len(unknown) > 0 {
		return authority.DelegatePolicyDraft{}, fmt.Errorf("%w: unknown setting %s", authority.ErrInvalid, unknown[0])
	}
	for _, key := range delegatePolicyKeys {
		if !meta.IsDefined(delegatePolicyTable, key) {
			return authority.DelegatePolicyDraft{}, fmt.Errorf("%w: a delegate policy states %s", authority.ErrInvalid, key)
		}
	}
	return authority.DelegatePolicyDraft{Body: body, Policy: file.DelegatePolicy}, nil
}

// projectNames reads the name of every project by its ID, so a credential names its project as a person
// does.
func (h *handler) projectNames(ctx context.Context) (map[ledger.WorkspaceID]string, error) {
	projects, err := h.Projects.List(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[ledger.WorkspaceID]string, len(projects))
	for _, p := range projects {
		names[p.ID] = p.Name
	}
	return names, nil
}

func delegateView(credential authority.DelegateCredential, names map[ledger.WorkspaceID]string, now int64) api.Delegate {
	view := api.Delegate{
		ID: credential.ID, Label: credential.Label, Project: names[credential.Project], Kind: credential.Kind,
		CreatedAt: credential.CreatedAt, ExpiresAt: credential.ExpiresAt, State: credential.State(now),
	}
	if view.Project == "" {
		view.Project = string(credential.Project)
	}
	if credential.LastUsedAt != 0 {
		view.LastUsedAt = &credential.LastUsedAt
	}
	if credential.RevokedAt != 0 {
		view.RevokedAt = &credential.RevokedAt
	}
	return view
}

func (h *handler) listDelegates(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	credentials, now, err := h.delegates.List(r.Context(), h.Scope, actor)
	if err != nil {
		fail(w, err)
		return
	}
	names, err := h.projectNames(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out := api.DelegateList{AsOf: now, Delegates: make([]api.Delegate, 0, len(credentials))}
	for _, credential := range credentials {
		// An assistant's credentials are the control plane's, one for each generation of a session, and
		// a member's list holds the ones the member made.
		if credential.Kind != authority.DelegateExternal {
			continue
		}
		out.Delegates = append(out.Delegates, delegateView(credential, names, now))
	}
	reply(w, http.StatusOK, out)
}

// createDelegate makes a credential for the calling member, bound to an active project it names. The
// answer holds the token, which is the one time anyone sees it.
func (h *handler) createDelegate(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.CreateDelegate
	if !decode(w, r, &in) {
		return
	}
	project, err := h.Projects.Named(r.Context(), in.Project)
	if errors.Is(err, ports.ErrNotFound) || (err == nil && project.State != ledger.ProjectActive) {
		invalid(w, "a delegate names an active project, and %q is none", in.Project)
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	grant, err := h.delegates.Create(r.Context(), h.Scope, authority.DelegateRequest{
		Member: actor.ID, Project: project.ID, Label: in.Label, Kind: in.Kind, ExpiresInMs: in.ExpiresInMs,
	}, actor)
	if err != nil {
		fail(w, err)
		return
	}
	names := map[ledger.WorkspaceID]string{project.ID: project.Name}
	reply(w, http.StatusCreated, api.DelegateCreated{Delegate: delegateView(grant.Credential, names, grant.Credential.CreatedAt), Token: grant.Token})
}

func (h *handler) revokeDelegate(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	credential, err := h.delegates.Revoke(r.Context(), h.Scope, r.PathValue("id"), actor)
	if err != nil {
		fail(w, err)
		return
	}
	h.voidCardsOf(r.Context(), []authority.DelegateCredential{credential}, actor)
	names, err := h.projectNames(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, delegateView(credential, names, credential.RevokedAt))
}

func (h *handler) removeDelegate(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	if err := h.delegates.Remove(r.Context(), h.Scope, r.PathValue("id"), actor); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// signOutAll ends every session of the calling member, this one with them, and revokes every credential
// of theirs that still acts.
func (h *handler) signOutAll(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	ended, err := h.sessions.EndAll(r.Context(), h.Scope, actor)
	if err != nil {
		fail(w, err)
		return
	}
	var revoked []authority.DelegateCredential
	err = h.Installation.Read(r.Context(), func(tx ports.SharedReadTx) error {
		for _, id := range ended.Ended {
			credential, err := tx.Members().Delegate(id)
			if err != nil {
				return err
			}
			revoked = append(revoked, credential)
		}
		return nil
	})
	if err == nil {
		h.voidCardsOf(r.Context(), revoked, actor)
	}
	stopped := h.stopConversationsOf(r.Context(), actor)
	reply(w, http.StatusOK, api.SignedOutAll{Sessions: ended.Sessions, Delegates: ended.Delegates, Conversations: stopped})
}

// stopConversationsOf stops every running conversation of a member, in every active project, once their
// sessions ended. Their credentials were revoked with the sessions, so a project this cannot reach still
// runs no credential of theirs.
func (h *handler) stopConversationsOf(ctx context.Context, actor ledger.Actor) []string {
	stopped := []string{}
	projects, err := h.Projects.List(ctx)
	if err != nil {
		return stopped
	}
	for _, project := range projects {
		if project.State != ledger.ProjectActive {
			continue
		}
		store, err := h.Projects.Open(ctx, project)
		if err != nil {
			continue
		}
		ids, err := control.NewConversations(store, h.Windows).StopMember(ctx, project.ID, actor.ID, actor)
		if err == nil {
			stopped = append(stopped, ids...)
		}
	}
	return stopped
}

// voidCardsOf voids the open cards each revoked credential prepared, in the active project it acts in. The
// revocation has committed by now, and a confirmation refuses a card whose delegate no longer acts, so a
// project this cannot reach still runs none of them.
func (h *handler) voidCardsOf(ctx context.Context, credentials []authority.DelegateCredential, actor ledger.Actor) {
	if len(credentials) == 0 {
		return
	}
	projects, err := h.Projects.List(ctx)
	if err != nil {
		return
	}
	for _, credential := range credentials {
		for _, project := range projects {
			if project.ID != credential.Project || project.State != ledger.ProjectActive {
				continue
			}
			store, err := h.Projects.Open(ctx, project)
			if err != nil {
				continue
			}
			_, _ = control.NewApprovals(store).VoidCardsOf(ctx, project.ID, credential.Actor(), actor)
		}
	}
}

// labelsOf reads the label of each delegate a page of events names, by its credential, so the audit names
// a delegate as its member does. A credential whose record was removed keeps its ID as its label.
func labelsOf(members ports.MemberReader, events []api.StreamEvent) error {
	labels := map[string]string{}
	for i := range events {
		_, credential, ok := authority.ActingFor(ledger.Actor{Kind: ledger.ActorKind(events[i].ActorKind), ID: events[i].ActorID})
		if !ok {
			continue
		}
		label, seen := labels[credential]
		if !seen {
			record, err := members.Delegate(credential)
			switch {
			case errors.Is(err, ports.ErrNotFound):
				label = credential
			case err != nil:
				return err
			default:
				label = record.Label
			}
			labels[credential] = label
		}
		events[i].ActorLabel = label
	}
	return nil
}
