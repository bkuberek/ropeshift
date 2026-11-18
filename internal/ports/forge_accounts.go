package ports

import (
	"context"
	"errors"
)

// ErrForgeScope means the forge refused a read because the sign-in lacks the scope it needs, such as read:org
// to list a person's organizations.
var ErrForgeScope = errors.New("the forge sign-in lacks a scope")

// ForgeAccount is what a worker's forge CLI says of the person's sign-in.
type ForgeAccount struct {
	// State is ok, gh_missing, gh_outdated, or gh_signed_out, as ledger.ForgeAccountOK and its siblings name them.
	State string
	// Detail is what the CLI said, when it said something.
	Detail string
	// Binary is the absolute path of the CLI the lookup found.
	Binary string
	// Login is the active account of the host, and GitProtocol the protocol its git operations use.
	Login       string
	GitProtocol string
	// Scopes are the scopes of a classic token. A token that reports none, as a fine-grained one does, has none.
	Scopes []string
}

// CreateRepository is one repository to create on a forge.
type CreateRepository struct {
	Owner  string
	Name   string
	Public bool
}

// ForgeAccounts is a worker's access to the person's forge account, to create the repositories of a new project
// (FR-TPL-6). It runs the forge's CLI under the person's own sign-in, never signs in, and never reads the token.
type ForgeAccounts interface {
	// Account reads the sign-in on the host.
	Account(ctx context.Context, host string) (ForgeAccount, error)
	// Owners lists the organizations the signed-in account belongs to, sorted. A sign-in that lacks the scope to
	// list them is ErrForgeScope.
	Owners(ctx context.Context, host string) ([]string, error)
	// Exists says a repository already exists at owner/name.
	Exists(ctx context.Context, host, owner, name string) (bool, error)
	// Create creates a repository, private unless it asks for public. It creates only the repository: no clone,
	// no remote, and no push.
	Create(ctx context.Context, host string, repository CreateRepository) error
}
