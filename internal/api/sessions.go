package api

import (
	"context"
	"net/http"
)

// HeaderSession carries a member's session. It is apart from Authorization, which is the
// worker's, because a worker and a member can both claim on one route.
const HeaderSession = "Ropeshift-Session"

// SignIn exchanges a passphrase for a session. TTLMs asks for a shorter one.
type SignIn struct {
	Passphrase string `json:"passphrase"`
	TTLMs      int64  `json:"ttl_ms,omitempty"`
}

// Session is a new session. The server keeps a digest, so this is the one time anyone
// sees the token.
type Session struct {
	Token     string `json:"token"`
	Member    string `json:"member"`
	ExpiresAt int64  `json:"expires_at"`
}

// SetPassphrase sets the passphrase. The first needs no session.
type SetPassphrase struct {
	Passphrase string `json:"passphrase"`
}

// PassphrasePath is where the passphrase is read and set on the socket, and changed on the web listener.
const PassphrasePath = "/v1/passphrase"

// ChangePassphrase is the web listener's form of setting the passphrase, which it serves only as a change: Current
// is the passphrase now in force, which proves the person at the page knows it, and Passphrase is the new one.
// The socket's route takes SetPassphrase alone and refuses a body that names a current passphrase.
type ChangePassphrase struct {
	Current    string `json:"current"`
	Passphrase string `json:"passphrase"`
}

// PassphraseChanged says how many other sessions of the member the change ended, and how many delegate credentials
// of the member still act, which a change leaves alone and signing out of every session ends. The session the change
// came from goes on under a new token, which the answer sets as cookies and never states.
type PassphraseChanged struct {
	SessionsEnded int64 `json:"sessions_ended"`
	Delegates     int64 `json:"delegates"`
}

// PassphraseState says whether a passphrase is set. It says nothing else of it.
type PassphraseState struct {
	Set bool `json:"set"`
}

// Passphrase reads whether a passphrase is set, which needs no session.
func (c *Client) Passphrase(ctx context.Context) (PassphraseState, error) {
	var out PassphraseState
	err := c.Do(ctx, http.MethodGet, PassphrasePath, nil, &out)
	return out, err
}

// SignIn opens a session.
func (c *Client) SignIn(ctx context.Context, in SignIn) (Session, error) {
	var out Session
	err := c.Do(ctx, http.MethodPost, "/v1/sessions", in, &out)
	return out, err
}

// SetPassphrase sets the passphrase.
func (c *Client) SetPassphrase(ctx context.Context, in SetPassphrase) error {
	return c.Do(ctx, http.MethodPut, PassphrasePath, in, nil)
}

// SignInPath opens a session, and SignOutPath ends the one a call presents.
const (
	SignInPath  = "/v1/sessions"
	SignOutPath = "/v1/sessions/end"
)

// SignOut ends this client's session, so its token opens nothing again.
func (c *Client) SignOut(ctx context.Context) error {
	return c.Do(ctx, http.MethodPost, SignOutPath, struct{}{}, nil)
}
