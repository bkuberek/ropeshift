package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"ropeshift.com/ropeshift/internal/authority"
)

// HeaderDelegate carries a delegate credential (ADR-0024). A request carries it or a member's session,
// never both, and only on the local socket.
const HeaderDelegate = "Ropeshift-Delegate"

// HeaderWhy carries the reason a delegate gives for a consent operation, which the card it becomes holds.
const HeaderWhy = "Ropeshift-Why"

// The refusals of a delegate.
const (
	// CodeBadDelegate is a delegate credential that is not known, was revoked, or expired.
	CodeBadDelegate = "bad_delegate"
	// CodeOutsideProject is a delegate calling a route outside the one project its credential names.
	CodeOutsideProject = "outside_project"
	// CodeMemberOnly is a delegate calling an operation only a member runs.
	CodeMemberOnly = "member_only"
	// CodeDelegateActive is a request to remove a credential that still acts.
	CodeDelegateActive = "delegate_active"
)

// The installation's routes for delegates, signing out everywhere, and the class table.
const (
	DelegatesPath   = "/v1/delegates"
	SignOutAllPath  = "/v1/sessions/end-all"
	OperationsPath  = "/v1/operations"
	delegateRevoke  = "revoke"
	delegateRemoval = "remove"
)

// CreateDelegate asks for a delegate credential bound to one active project, by its name.
type CreateDelegate struct {
	Project     string `json:"project"`
	Label       string `json:"label"`
	ExpiresInMs int64  `json:"expires_in_ms"`
	Kind        string `json:"kind,omitempty"`
}

// Delegate is one delegate credential as its member reads it. LastUsedAt and RevokedAt are absent until
// each happens.
type Delegate struct {
	ID         string                  `json:"id"`
	Label      string                  `json:"label"`
	Project    string                  `json:"project"`
	Kind       string                  `json:"kind"`
	CreatedAt  int64                   `json:"created_at"`
	ExpiresAt  int64                   `json:"expires_at"`
	LastUsedAt *int64                  `json:"last_used_at"`
	RevokedAt  *int64                  `json:"revoked_at"`
	State      authority.DelegateState `json:"state"`
}

// DelegateCreated is a new credential with its token, which this is the one time anyone sees.
type DelegateCreated struct {
	Delegate
	Token string `json:"token"`
}

// DelegateList is the calling member's credentials, oldest first.
type DelegateList struct {
	AsOf      int64      `json:"as_of"`
	Delegates []Delegate `json:"delegates"`
}

// SignedOutAll is what signing out of every session ended.
type SignedOutAll struct {
	Sessions  int64    `json:"sessions"`
	Delegates []string `json:"delegates"`
	// Conversations are the member's running conversations with the assistant it stopped.
	Conversations []string `json:"conversations"`
}

// OperationList is the class table.
type OperationList struct {
	Operations []authority.Operation `json:"operations"`
}

// Prepared is the answer to a delegate's consent call: the card it recorded, which runs only when a member
// confirms it. The client returns it as an error, because the call ran nothing.
type Prepared struct {
	Card authority.Approval `json:"card"`
}

func (p *Prepared) Error() string {
	return fmt.Sprintf("prepared card %s: %s", p.Card.ID, p.Card.Summary)
}

// preparedOf reads a 202 answer that holds a card. Any other answer holds none.
func preparedOf(status int, body []byte) *Prepared {
	if status != http.StatusAccepted {
		return nil
	}
	var prepared Prepared
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&prepared); err != nil || prepared.Card.ID == "" || prepared.Card.Kind != authority.KindConfirm {
		return nil
	}
	return &prepared
}

// UseDelegate sends a delegate credential with each call, in place of a session.
func (c *Client) UseDelegate(token string) { c.delegate = token }

// UseWhy sends the reason a delegate gives with each call.
func (c *Client) UseWhy(why string) { c.why = why }

// Delegates lists the calling member's delegate credentials.
func (c *Client) Delegates(ctx context.Context) (DelegateList, error) {
	var out DelegateList
	err := c.Do(ctx, http.MethodGet, DelegatesPath, nil, &out)
	return out, err
}

// CreateDelegate makes a delegate credential.
func (c *Client) CreateDelegate(ctx context.Context, in CreateDelegate) (DelegateCreated, error) {
	var out DelegateCreated
	err := c.Do(ctx, http.MethodPost, DelegatesPath, in, &out)
	return out, err
}

// RevokeDelegate ends a delegate credential for good.
func (c *Client) RevokeDelegate(ctx context.Context, id string) (Delegate, error) {
	var out Delegate
	err := c.Do(ctx, http.MethodPost, DelegatesPath+"/"+url.PathEscape(id)+"/"+delegateRevoke, struct{}{}, &out)
	return out, err
}

// RemoveDelegate deletes the record of a credential that no longer acts.
func (c *Client) RemoveDelegate(ctx context.Context, id string) error {
	return c.Do(ctx, http.MethodPost, DelegatesPath+"/"+url.PathEscape(id)+"/"+delegateRemoval, struct{}{}, nil)
}

// SignOutAll ends every session of the calling member and revokes every delegate credential of theirs.
func (c *Client) SignOutAll(ctx context.Context) (SignedOutAll, error) {
	var out SignedOutAll
	err := c.Do(ctx, http.MethodPost, SignOutAllPath, struct{}{}, &out)
	return out, err
}

// Operations reads the class table.
func (c *Client) Operations(ctx context.Context) (OperationList, error) {
	var out OperationList
	err := c.Do(ctx, http.MethodGet, OperationsPath, nil, &out)
	return out, err
}
