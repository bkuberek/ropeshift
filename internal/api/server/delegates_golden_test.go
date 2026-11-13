package server_test

import (
	"fmt"
	"net/http"
	"regexp"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/roster"
	"ropeshift.com/ropeshift/test/apitest"
)

// TestGoldenDelegateReads writes what the confirmation card, the delegates panel, the delegate policy,
// and the audit's delegate rows read, from a real ledger, so the web's tests run on what the API serves.
func TestGoldenDelegateReads(t *testing.T) {
	t.Parallel()
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	ctx := t.Context()
	createReady(t, d, "w1")
	detected(t, d, aRepository, "main")
	name := "harbor-web"
	if _, err := d.Client.SetRepository(ctx, aRepository, api.RepositoryChange{Name: &name}); err != nil {
		t.Fatal(err)
	}
	created, err := d.Client.CreateDelegate(ctx, api.CreateDelegate{Project: apitest.ProjectName, Label: "planning assistant", ExpiresInMs: 8 * 60 * 60 * 1000})
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	revoked := aDelegate(t, d.Client.Client, apitest.ProjectName)
	if _, err := d.Client.RevokeDelegate(ctx, revoked.ID); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	if _, err := d.Client.CreateDelegate(ctx, api.CreateDelegate{Project: apitest.ProjectName, Label: "release notes bot", ExpiresInMs: 7 * 24 * 60 * 60 * 1000}); err != nil {
		t.Fatal(err)
	}
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	raised := ledger.PersonApproves
	_, err = delegate.SetRepository(ctx, name, api.RepositoryChange{MergeAuthority: &raised})
	preparedCard(t, err)
	clock.Advance(time.Minute)
	_, err = delegate.ChangeRoster(ctx, roster.Change{Op: roster.OpSet, Agent: "explore", Fields: map[string]string{"effort": "high"}})
	preparedCard(t, err)
	clock.Advance(time.Minute)
	err = delegate.Do(ctx, http.MethodPost, apitest.Route("/v1/panes/lane-w1/input"), api.PaneInput{Text: "yes", Enter: true}, nil)
	preparedCard(t, err)
	clock.Advance(time.Minute)
	_, err = delegate.CancelWork(ctx, "w1", api.CancelWork{Reason: "The plan dropped it."})
	cancel := preparedCard(t, err)
	needs, err := d.Client.NeedsYou(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.DecideApproval(ctx, cancel.ID, api.DecideApproval{Digest: cancel.SubjectDigest, Allow: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.PublishPolicy(ctx, "[delegate_policy]\nversion = 2\nexempt = [\"work_order.accept\"]\n"); err != nil {
		t.Fatal(err)
	}
	reads := map[string]func() (any, error){
		"needs-you-cards":   func() (any, error) { return needs, nil },
		"card-confirmed":    func() (any, error) { return d.Client.Approval(ctx, cancel.ID) },
		"delegates":         func() (any, error) { return d.Client.Delegates(ctx) },
		"operation-classes": func() (any, error) { return d.Client.Operations(ctx) },
		"audit-delegates": func() (any, error) {
			return d.Client.Audit(ctx, api.AuditQuery{Actor: string(ledger.ActorDelegate), Limit: 6})
		},
		"policies-delegate": func() (any, error) {
			var out api.PolicyList
			err := d.Client.Do(ctx, http.MethodGet, apitest.Route("/v1/policies?kind=delegate"), nil, &out)
			return out, err
		},
	}
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			value, err := read()
			if err != nil {
				t.Fatal(err)
			}
			holdsGolden(t, name, steadyCredentials(steadyTimes(steady(pretty(t, value)))))
		})
	}
}

var credentialID = regexp.MustCompile(`dlg_[0-9A-HJKMNP-TV-Z]{26}`)

// steadyCredentials names each delegate credential by where it first appears, as steady names an ID.
func steadyCredentials(raw []byte) []byte {
	seen := map[string]string{}
	return credentialID.ReplaceAllFunc(raw, func(id []byte) []byte {
		name, ok := seen[string(id)]
		if !ok {
			name = fmt.Sprintf("dlg_credential-%d", len(seen)+1)
			seen[string(id)] = name
		}
		return []byte(name)
	})
}
