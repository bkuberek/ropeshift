package api

import (
	"context"
	"net/http"
)

// MembersPath lists the installation's members with their sessions.
const MembersPath = "/v1/members"

// MemberSession is one sign-in of a member. The ledger keeps only a digest of its token, so this names
// none.
type MemberSession struct {
	CreatedAt int64 `json:"created_at"`
	ExpiresAt int64 `json:"expires_at"`
}

// MemberView is one member, the sessions that still count, and when the newest of them began.
type MemberView struct {
	ID         string          `json:"id"`
	Sessions   []MemberSession `json:"sessions"`
	LastSignIn int64           `json:"last_sign_in,omitempty"`
}

// MemberList is every member that holds a session, and how many members can sign in.
type MemberList struct {
	AsOf     int64        `json:"as_of"`
	Enrolled int64        `json:"enrolled"`
	Members  []MemberView `json:"members"`
}

// Members lists the installation's members.
func (c *Client) Members(ctx context.Context) (MemberList, error) {
	var out MemberList
	err := c.Do(ctx, http.MethodGet, MembersPath, nil, &out)
	return out, err
}
