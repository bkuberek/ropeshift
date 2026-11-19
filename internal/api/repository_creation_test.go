package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
)

// repositoryCreationServer answers every request with status and body, and keeps the last request it saw.
func repositoryCreationServer(t *testing.T, status int, body string) (*api.ProjectClient, *http.Request, *[]byte) {
	t.Helper()
	var seen http.Request
	var seenBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = *r.Clone(context.Background())
		seenBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	client, err := api.NewTCPClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client.Project("tt"), &seen, &seenBody
}

// Retrying a repository creation posts to the entry's own route and reads back the decided approval.
func TestRetryRepositoryPostsToTheEntrysRoute(t *testing.T) {
	t.Parallel()
	client, seen, _ := repositoryCreationServer(t, http.StatusOK,
		`{"id":"ap1","repositories":[{"name":"web","manifest_path":"repos/web","path":"/p/web","outcome":"registered"}]}`)
	decided, err := client.RetryRepository(t.Context(), "ap1", "web")
	if err != nil {
		t.Fatal(err)
	}
	if seen.Method != http.MethodPost || seen.URL.Path != "/v1/projects/tt/approvals/ap1/repositories/web/retry" {
		t.Errorf("the server saw %s %s", seen.Method, seen.URL.Path)
	}
	if decided.ID != "ap1" || len(decided.Repositories) != 1 || decided.Repositories[0].Name != "web" {
		t.Errorf("the decided approval reads %+v", decided)
	}
}

// Skipping one carries the reason, and a name that is not a path segment stays one.
func TestSkipRepositoryPostsTheReasonToTheEntrysRoute(t *testing.T) {
	t.Parallel()
	client, seen, body := repositoryCreationServer(t, http.StatusOK, `{"id":"ap1","repositories":[]}`)
	if _, err := client.SkipRepository(t.Context(), "ap1", "we/b", api.SkipRepository{Reason: "built by hand"}); err != nil {
		t.Fatal(err)
	}
	if seen.Method != http.MethodPost || seen.URL.EscapedPath() != "/v1/projects/tt/approvals/ap1/repositories/we%2Fb/skip" {
		t.Errorf("the server saw %s %s", seen.Method, seen.URL.EscapedPath())
	}
	var sent map[string]string
	if err := json.Unmarshal(*body, &sent); err != nil || len(sent) != 1 || sent["reason"] != "built by hand" {
		t.Errorf("the body reads %s: %v", *body, err)
	}
}

// A refusal comes back as the API's own error, for both routes.
func TestRetryAndSkipReadTheServersRefusal(t *testing.T) {
	t.Parallel()
	client, _, _ := repositoryCreationServer(t, http.StatusConflict, `{"error":{"code":"illegal_move","message":"api is being created now"}}`)
	for name, call := range map[string]func() error{
		"retry": func() error { _, err := client.RetryRepository(t.Context(), "ap1", "api"); return err },
		"skip": func() error {
			_, err := client.SkipRepository(t.Context(), "ap1", "api", api.SkipRepository{Reason: "r"})
			return err
		},
	} {
		var refused *api.Error
		if err := call(); !errors.As(err, &refused) || refused.Status != http.StatusConflict || refused.Detail.Code != api.CodeIllegalMove {
			t.Errorf("%s: the refusal reads %v", name, err)
		}
	}
}

// A repository creation that needs a member reads under the names the design gives its fields, and its states and
// actions are the ones every surface is held to knowing.
func TestARepositoryCreationNeedReadsUnderItsFieldNames(t *testing.T) {
	t.Parallel()
	item := api.NeedItem{
		Kind: api.NeedRepositoryCreation, ID: "ap1/api", Since: 7,
		RepositoryCreation: &api.RepositoryCreationNeed{
			Approval: "ap1", Plan: "tt", PlanVersion: 2, Name: "api", ManifestPath: "repos/api", Path: "/work/tt/repos/api",
			State: api.RepositoryCreationFailed, Reason: "push failed", Remedy: "clone it", Actions: api.RepositoryCreationActions,
		},
	}
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	var need map[string]any
	if err := json.Unmarshal(fields["repository_creation"], &need); err != nil {
		t.Fatalf("the item holds no repository_creation: %s", raw)
	}
	for _, key := range []string{"approval", "plan", "plan_version", "name", "manifest_path", "path", "state", "reason", "remedy", "actions"} {
		if _, ok := need[key]; !ok {
			t.Errorf("the need has no %q: %s", key, fields["repository_creation"])
		}
	}
	if api.NeedRepositoryCreation != "repository_creation" {
		t.Errorf("the kind is %q", api.NeedRepositoryCreation)
	}
	if !slices.Equal([]string{api.RepositoryCreationRefused, api.RepositoryCreationFailed, api.RepositoryCreationInterrupted, api.RepositoryCreationRunning},
		[]string{"refused", "failed", "interrupted", "running"}) {
		t.Error("the states are not the four the design names")
	}
	if !slices.Equal(api.RepositoryCreationActions, []string{api.ActRepositoryRetry, api.ActRepositorySkip}) {
		t.Errorf("the actions are %v", api.RepositoryCreationActions)
	}
}
