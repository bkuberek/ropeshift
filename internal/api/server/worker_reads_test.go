package server_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
)

// TestAWorkersTemplateWriteAndForgeAccountReadsAreAMembersAlone holds that the two reads of a request a member made of
// a worker, the template write's and the forge account's, answer a member's session only. The socket admits whoever
// runs as the owning user, a lane among them, and the forge account read tells a GitHub login and its organizations,
// so a call with no session, a delegate, and a worker's credential are each refused, and the member still reads both.
func TestAWorkersTemplateWriteAndForgeAccountReadsAreAMembersAlone(t *testing.T) {
	d := apitest.Start(t)
	credential, _ := joinWorker(t, d)
	own := capsServe(t, d, nil, aSubscriptionWorker)
	write, err := d.Client.RequestTemplateWrite(t.Context(), aSubscriptionWorker, api.TemplateWrite{Shape: "monorepo", Path: "/srv/harbor", FirstCommit: true})
	if err != nil {
		t.Fatal(err)
	}
	status, raw := rawCall(t, own, d.Session, http.MethodPost, readsPath, ledger.ForgeAccountRequest{Forge: "github"})
	var read ledger.ForgeAccountReadView
	if status != http.StatusAccepted || json.Unmarshal(raw, &read) != nil || read.ID == "" {
		t.Fatalf("the forge account read is answered %d: %s", status, raw)
	}
	paths := map[string]string{"a template write": writesPath + "/" + write.ID, "a forge account read": readsPath + "/" + read.ID}
	delegate := asDelegate(t, d.Socket, aDelegate(t, d.Client.Client, apitest.ProjectName).Token, apitest.ProjectName)
	anonymous := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer anonymous.Close()
	worker := workerOf(t, d, credential)
	for name, path := range paths {
		t.Run(name+" to a member", func(t *testing.T) {
			if status, raw := rawCall(t, own, d.Session, http.MethodGet, path, nil); status != http.StatusOK {
				t.Errorf("a member's read is answered %d: %s", status, raw)
			}
		})
		t.Run(name+" to a call with no session", func(t *testing.T) {
			refusedWith(t, anonymous.Do(t.Context(), http.MethodGet, path, nil, nil), http.StatusUnauthorized, api.CodeSessionRequired)
		})
		t.Run(name+" to a delegate", func(t *testing.T) {
			refusedWith(t, delegate.Do(t.Context(), http.MethodGet, path, nil, nil), http.StatusForbidden, api.CodeMemberOnly)
		})
		t.Run(name+" to a worker's credential", func(t *testing.T) {
			var refused *api.Error
			if err := worker.Do(t.Context(), http.MethodGet, path, nil, nil); !errors.As(err, &refused) || refused.Status < http.StatusBadRequest {
				t.Errorf("a worker's credential reads it: %v", err)
			}
		})
	}
}
