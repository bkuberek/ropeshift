package server_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

const caPin = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// A group server answers a new join token with a join string: the token, the URL its workers reach it at, and the
// fingerprint of its own certificate authority when it has one. The token in it registers one worker once, and the
// string is no one else's than the member who made it.
func TestAGroupServerAnswersAJoinTokenWithAJoinString(t *testing.T) {
	t.Parallel()
	d := apitest.StartWith(t, apitest.Options{Mode: api.ModeGroup, WorkerURL: "https://ropeshift.lan:7400", CAFingerprint: caPin})
	made, err := d.Client.NewJoinToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	joined, err := api.ParseJoinString(made.JoinString)
	if err != nil {
		t.Fatalf("the answer holds %v", err)
	}
	if joined.WorkerURL != "https://ropeshift.lan:7400" || joined.CASHA256 != caPin || joined.Token != made.Token || made.Token == "" {
		t.Errorf("the join string reads as %+v beside the token", joined)
	}

	// The token in the string registers a worker once, and another worker that presents it later is refused.
	plane := control.NewWorkers(d.Store, apitest.Scope, control.WorkerOptions{HeartbeatMs: apitest.HeartbeatMs, LeaseTTLMs: apitest.LeaseTTL})
	if _, err := plane.Register(t.Context(), ledger.Registration{JoinToken: joined.Token, WorkerID: "worker-a", Name: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := plane.Register(t.Context(), ledger.Registration{JoinToken: joined.Token, WorkerID: "worker-b", Name: "b"}); !errors.Is(err, ledger.ErrBadJoinToken) {
		t.Errorf("a token that was used joins a second worker: %v", err)
	}
	if _, err := plane.Register(t.Context(), ledger.Registration{JoinToken: joined.Token, WorkerID: "worker-a", Name: "a"}); err != nil {
		t.Errorf("a retry by the worker that used the token is refused: %v", err)
	}
}

// A join string made by a server that is its own authority carries the fingerprint, and one made behind a proxy or
// with a supplied certificate carries none, so the worker verifies through the system's roots.
func TestAJoinStringCarriesAFingerprintOnlyWhenTheServerHasAnAuthority(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		options apitest.Options
		url     string
		pin     string
	}{
		"its own authority":  {apitest.Options{Mode: api.ModeGroup, WorkerURL: "https://ropeshift.lan:7400", CAFingerprint: caPin}, "https://ropeshift.lan:7400", caPin},
		"the system's roots": {apitest.Options{Mode: api.ModeGroup, WorkerURL: "https://ropeshift.example:443"}, "https://ropeshift.example:443", ""},
		"loopback over http": {apitest.Options{Mode: api.ModeGroup, WorkerURL: "http://127.0.0.1:7400"}, "http://127.0.0.1:7400", ""},
	} {
		d := apitest.StartWith(t, test.options)
		made, err := d.Client.NewJoinToken(t.Context())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		joined, err := api.ParseJoinString(made.JoinString)
		if err != nil || joined.WorkerURL != test.url || joined.CASHA256 != test.pin {
			t.Errorf("%s: the join string is %+v, %v", name, joined, err)
		}
	}
}

// A solo daemon answers the token alone, as it did, because its one worker is the daemon's own and no join string
// is made for a worker on another machine.
func TestASoloDaemonAnswersAJoinTokenAlone(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	made, err := d.Client.NewJoinToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if made.Token == "" || made.JoinString != "" {
		t.Errorf("a solo daemon answers %+v", made)
	}
}

// A group server that does not know the URL its workers reach it at makes no join string and no join token, and
// says what to set, because a token with no string around it is no way to join one.
func TestAGroupServerWithNoWorkerURLMakesNoJoinToken(t *testing.T) {
	t.Parallel()
	d := apitest.StartWith(t, apitest.Options{Mode: api.ModeGroup})
	before := joinTokensMade(t, d)
	_, err := d.Client.NewJoinToken(t.Context())
	var refused *api.Error
	if !errors.As(err, &refused) || refused.Status != http.StatusConflict || refused.Detail.Code != api.CodeNoWorkerURL {
		t.Fatalf("a group server with no worker URL answers %v", err)
	}
	if !strings.Contains(refused.Detail.Message, "--worker-url") || !strings.Contains(refused.Detail.Message, "ROPESHIFT_WORKER_URL") {
		t.Errorf("the refusal does not say what to set: %s", refused.Detail.Message)
	}
	if after := joinTokensMade(t, d); after != before {
		t.Errorf("a join token was made, %d before and %d after", before, after)
	}
}

// Every join string is personal to the member who made it: the token's record names that member, and nothing in
// the string or its answer makes it shared.
func TestAJoinStringIsPersonalToTheMemberWhoMadeIt(t *testing.T) {
	t.Parallel()
	d := apitest.StartWith(t, apitest.Options{Mode: api.ModeGroup, WorkerURL: "https://ropeshift.lan:7400", CAFingerprint: caPin})
	if _, err := d.Client.NewJoinToken(t.Context()); err != nil {
		t.Fatal(err)
	}
	sessions := control.NewSessions(d.Store, d.Config.Identity)
	member, err := sessions.Member(t.Context(), apitest.Scope, d.Session)
	if err != nil {
		t.Fatal(err)
	}
	var events []ledger.Event
	err = d.Store.Installation(apitest.Scope).Read(t.Context(), func(tx ports.SharedReadTx) error {
		var err error
		events, err = tx.Events(0, 10_000)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var made []ledger.Event
	for _, event := range events {
		if event.Type == "worker.join_token_created" {
			made = append(made, event)
		}
	}
	if len(made) != 1 || made[0].Actor != member || member.Kind != ledger.ActorMember {
		t.Errorf("the join token's record names %+v, and the member is %+v", made, member)
	}
	if strings.Contains(string(made[0].Data), "shared") {
		t.Errorf("a record of a join token says it is shared: %s", made[0].Data)
	}
}

func joinTokensMade(t *testing.T, d *apitest.Daemon) int {
	t.Helper()
	var events []ledger.Event
	err := d.Store.Installation(apitest.Scope).Read(t.Context(), func(tx ports.SharedReadTx) error {
		var err error
		events, err = tx.Events(0, 10_000)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Type == "worker.join_token_created" {
			count++
		}
	}
	return count
}
