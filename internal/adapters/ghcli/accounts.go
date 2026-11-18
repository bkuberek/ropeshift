package ghcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// Accounts is the ForgeAccounts port's GitHub adapter: it reads the person's sign-in and creates repositories under
// it with the `gh` CLI. It never signs in, never reads the token, and never passes --internal, --source, --clone, or
// --push.
type Accounts struct {
	Forge
}

var _ ports.ForgeAccounts = Accounts{}

// forHost returns the forge pointed at one host, so a command that takes no --hostname reaches it and never a
// default host the person configured.
func (a Accounts) forHost(host string) Forge {
	forge := a.Forge
	base := forge.Env
	if base == nil {
		base = os.Environ()
	}
	forge.Env = append(base[:len(base):len(base)], "GH_HOST="+host)
	return forge
}

// authEntry is one account of `gh auth status --json hosts`
// (https://cli.github.com/manual/gh_auth_status).
type authEntry struct {
	State       string `json:"state"`
	Error       string `json:"error"`
	Active      bool   `json:"active"`
	Login       string `json:"login,omitempty"`
	Scopes      string `json:"scopes"`
	GitProtocol string `json:"gitProtocol"`
}

// Account reads `gh auth status --hostname <host> --json hosts`, which exits 0 whatever the sign-in's state, so an
// exit with any other status is a `gh` too old for --json.
func (a Accounts) Account(ctx context.Context, host string) (ports.ForgeAccount, error) {
	binary, err := a.binary()
	if err != nil {
		return ports.ForgeAccount{State: ledger.ForgeAccountMissing, Detail: err.Error()}, nil
	}
	out, err := a.run(ctx, "", "auth", "status", "--hostname", host, "--json", "hosts")
	var failed *ghError
	if errors.As(err, &failed) {
		return ports.ForgeAccount{State: ledger.ForgeAccountOutdated, Binary: binary, Detail: failed.stderr}, nil
	}
	if err != nil {
		return ports.ForgeAccount{}, err
	}
	var status struct {
		Hosts map[string][]authEntry `json:"hosts"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return ports.ForgeAccount{State: ledger.ForgeAccountOutdated, Binary: binary, Detail: "gh auth status --json hosts answered no JSON object of hosts"}, nil
	}
	entries := status.Hosts[host]
	active := slices.IndexFunc(entries, func(e authEntry) bool { return e.Active })
	if active < 0 && len(entries) > 0 {
		active = 0
	}
	if active < 0 {
		return ports.ForgeAccount{State: ledger.ForgeAccountSignedOut, Binary: binary, Detail: "gh holds no account for " + host}, nil
	}
	entry := entries[active]
	if entry.State != "success" {
		return ports.ForgeAccount{State: ledger.ForgeAccountSignedOut, Binary: binary, Login: entry.Login, Detail: clip(entry.Error)}, nil
	}
	return ports.ForgeAccount{State: ledger.ForgeAccountOK, Binary: binary, Login: entry.Login, GitProtocol: entry.GitProtocol, Scopes: splitScopes(entry.Scopes)}, nil
}

// splitScopes reads the scopes of a classic token as `gh` lists them, comma separated and sometimes quoted. A token
// that lists none, as a fine-grained one does, has none.
func splitScopes(text string) []string {
	var scopes []string
	for _, part := range strings.Split(text, ",") {
		if scope := strings.Trim(strings.TrimSpace(part), `'"`); scope != "" {
			scopes = append(scopes, scope)
		}
	}
	return scopes
}

// Owners lists the organizations of the signed-in account with `gh api --paginate user/orgs`
// (https://docs.github.com/en/rest/orgs/orgs#list-organizations-for-the-authenticated-user), which answers 403
// to a classic token without read:org or user.
func (a Accounts) Owners(ctx context.Context, host string) ([]string, error) {
	out, err := a.run(ctx, "", "api", "--hostname", host, "--paginate", "user/orgs", "--jq", ".[].login")
	var failed *ghError
	if errors.As(err, &failed) && strings.Contains(failed.stderr, "HTTP 403") {
		return nil, fmt.Errorf("%w: %s", ports.ErrForgeScope, failed.stderr)
	}
	if err != nil {
		return nil, err
	}
	var owners []string
	for _, line := range strings.Split(string(out), "\n") {
		if login := strings.TrimSpace(line); login != "" {
			owners = append(owners, login)
		}
	}
	slices.Sort(owners)
	return owners, nil
}

// Exists runs `gh api repos/<owner>/<name> --silent`: an exit 0 says the repository exists, and any other exit says
// nothing more than that it was not read, so the create step decides.
func (a Accounts) Exists(ctx context.Context, host, owner, name string) (bool, error) {
	_, err := a.run(ctx, "", "api", "--hostname", host, "repos/"+owner+"/"+name, "--silent")
	var failed *ghError
	if errors.As(err, &failed) {
		return false, nil
	}
	return err == nil, err
}

// Create runs `gh repo create <owner>/<name>` with --private, or with --public only when asked
// (https://cli.github.com/manual/gh_repo_create). It never asks for the repository to be cloned, a remote added, or a
// push, so each of those is a step of its own.
func (a Accounts) Create(ctx context.Context, host string, repository ports.CreateRepository) error {
	visibility := "--private"
	if repository.Public {
		visibility = "--public"
	}
	_, err := a.forHost(host).run(ctx, "", "repo", "create", repository.Owner+"/"+repository.Name, visibility)
	var failed *ghError
	if errors.As(err, &failed) {
		return errors.New(cmpDetail(failed))
	}
	return err
}

// cmpDetail is what a failed run said, or its command line when it said nothing.
func cmpDetail(failed *ghError) string {
	if failed.stderr != "" {
		return failed.stderr
	}
	return failed.Error()
}
