package server_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
)

const aDay = 24 * time.Hour

func (r *webRig) plane() *control.Workers {
	return control.NewWorkers(r.d.Store, apitest.Scope, control.WorkerOptions{HeartbeatMs: apitest.HeartbeatMs, LeaseTTLMs: apitest.LeaseTTL})
}

// A join token made through the API lives a day unless the member names another lifetime, up to thirty days, and the
// answer says when it ends and which token it is, which a revocation names.
func TestAJoinTokenLivesADayUnlessItsMemberNamesAnotherLifetime(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	for name, test := range map[string]struct {
		lifetime time.Duration
		want     time.Duration
	}{
		"the default":     {0, aDay},
		"an hour":         {time.Hour, time.Hour},
		"the longest":     {control.MaxJoinTokenLifetime, control.MaxJoinTokenLifetime},
		"a few seconds":   {5 * time.Second, 5 * time.Second},
		"just over a day": {aDay + time.Minute, aDay + time.Minute},
	} {
		before := time.Now()
		made, err := d.Client.NewJoinTokenFor(t.Context(), test.lifetime)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		lives := time.UnixMilli(made.ExpiresAt).Sub(before)
		if made.ID == "" || !strings.HasPrefix(made.Token, made.ID+".") || lives < test.want-time.Minute || lives > test.want+time.Minute {
			t.Errorf("%s: the answer is %+v, which lives %s", name, made, lives)
		}
	}
}

// A lifetime of more than thirty days, or one below nothing, is no lifetime, and no token is made for it.
func TestAJoinTokenOfMoreThanThirtyDaysIsRefusedAndMakesNoToken(t *testing.T) {
	t.Parallel()
	d := apitest.StartWith(t, apitest.Options{Mode: api.ModeGroup, WorkerURL: "https://ropeshift.lan:7400"})
	before := joinTokensMade(t, d)
	for _, lifetime := range []time.Duration{control.MaxJoinTokenLifetime + time.Millisecond, 365 * aDay, -time.Hour} {
		_, err := d.Client.NewJoinTokenFor(t.Context(), lifetime)
		var refused *api.Error
		if !errors.As(err, &refused) || refused.Status != http.StatusBadRequest || refused.Detail.Code != api.CodeInvalid || !strings.Contains(refused.Detail.Message, "720h") {
			t.Errorf("a token of %s answers %v", lifetime, err)
		}
	}
	if after := joinTokensMade(t, d); after != before {
		t.Errorf("a join token was made, %d before and %d after", before, after)
	}
}

// A member revokes a token no worker used, by its ID, and the token is refused with its own code from then on. One a
// worker used is refused with its own code too, and a token that is not there is not found.
func TestAMemberRevokesAJoinTokenNoWorkerUsed(t *testing.T) {
	t.Parallel()
	d := apitest.StartWith(t, apitest.Options{Mode: api.ModeGroup, WorkerURL: "https://ropeshift.lan:7400"})
	r := &webRig{d: d, web: d.ServeNetworkWith(t, apitest.NetworkOptions{Assets: builtAssets, Recheck: 20 * time.Millisecond})}

	revoked, err := d.Client.NewJoinToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Client.RevokeJoinToken(t.Context(), revoked.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.Client.RevokeJoinToken(t.Context(), revoked.ID); err != nil {
		t.Errorf("revoking a token again: %v", err)
	}
	response, raw := r.registerAs(t, "", revoked.Token, "worker-a")
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeJoinTokenRevoked)

	used, err := d.Client.NewJoinToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	response, raw = r.registerAs(t, "", used.Token, "worker-b")
	wantStatus(t, response, raw, http.StatusOK, "")
	var refused *api.Error
	if err := d.Client.RevokeJoinToken(t.Context(), used.ID); !errors.As(err, &refused) || refused.Status != http.StatusConflict || refused.Detail.Code != api.CodeJoinTokenUsed {
		t.Errorf("revoking a used token answers %v", err)
	}
	if err := d.Client.RevokeJoinToken(t.Context(), "no-such-token"); !errors.As(err, &refused) || refused.Status != http.StatusNotFound {
		t.Errorf("revoking a token that is not there answers %v", err)
	}
}

// An expired token and a revoked one each count as a wrong guess, with the wrong tokens and passphrases of the same
// address, so a client address that is handed old join strings is as limited as one that guesses.
func TestAnExpiredOrRevokedJoinTokenCountsAsAWrongGuess(t *testing.T) {
	t.Parallel()
	r := limitedNetwork(t, byHeader)
	client := "203.0.113.7"
	plane := r.plane()
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}

	revoked, err := plane.MakeJoinToken(t.Context(), member, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := plane.RevokeJoinToken(t.Context(), member, revoked.ID); err != nil {
		t.Fatal(err)
	}
	short, err := plane.MakeJoinToken(t.Context(), member, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)

	response, raw := r.registerAs(t, client, revoked.Token, "worker-a")
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeJoinTokenRevoked)
	response, raw = r.registerAs(t, client, short.Token, "worker-b")
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeJoinTokenExpired)
	response, raw = r.registerAs(t, client, "no-such-token.and-no-secret", "worker-c")
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadJoinToken)

	// Three wrong guesses, and the next is refused whatever it carries.
	response, raw = r.registerAs(t, client, r.joinToken(t), "worker-d")
	wantStatus(t, response, raw, http.StatusTooManyRequests, api.CodeTooManyAttempts)
	// Another address is untouched.
	response, raw = r.registerAs(t, "203.0.113.8", r.joinToken(t), "worker-e")
	wantStatus(t, response, raw, http.StatusOK, "")
}

// Only the holder of a token's secret learns that it expired or was revoked: a wrong secret for the same ID is a bad
// token, as any wrong token is.
func TestAWrongSecretForARevokedJoinTokenIsABadToken(t *testing.T) {
	t.Parallel()
	r := limitedNetwork(t, byHeader)
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	made, err := r.plane().MakeJoinToken(t.Context(), member, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.plane().RevokeJoinToken(t.Context(), member, made.ID); err != nil {
		t.Fatal(err)
	}
	response, raw := r.registerAs(t, "203.0.113.7", made.ID+".the-wrong-secret", "worker-a")
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadJoinToken)
}
