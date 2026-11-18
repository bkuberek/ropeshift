package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"ropeshift.com/ropeshift/test/doubles/ghstate"
)

// preReceive is the argument the pre-receive hook of a repository `repo create` made runs the double with, which
// refuses the push while the state says RejectPush.
const preReceive = "__pre-receive"

func loginOf(state ghstate.State) string {
	if state.Login != "" {
		return state.Login
	}
	return "test"
}

// authStatusJSON answers `gh auth status --json hosts`, which exits 0 whatever the sign-in's state: one entry for
// the host, in the shape gh prints it (https://cli.github.com/manual/gh_auth_status). A signed-out account has an
// entry in the error state.
func authStatusJSON(state ghstate.State, host, fields string) {
	if state.NoJSON {
		fail(1, "unknown flag: --json")
	}
	if fields != "hosts" {
		fail(1, "Unknown JSON field: %q", fields)
	}
	entry := map[string]any{
		"state": "success", "active": true, "host": host, "login": loginOf(state), "tokenSource": "keyring",
		"scopes": strings.Join(state.Scopes, ", "), "gitProtocol": cmpProtocol(state.GitProtocol),
	}
	if state.SignedOut {
		entry["state"], entry["error"] = "error", state.AuthError
	}
	emit(map[string]any{"hosts": map[string]any{host: []any{entry}}})
}

func cmpProtocol(protocol string) string {
	if protocol == "" {
		return "https"
	}
	return protocol
}

// account answers the reads of `gh api` the forge account checks make: the signed-in user, its organizations, and
// whether a repository exists. It reports whether it answered.
func account(state ghstate.State, endpoint, jq string, silent, paginate bool) bool {
	switch endpoint {
	case "user":
		if jq == ".login" {
			fmt.Println(loginOf(state))
			os.Exit(0)
		}
		emit(map[string]string{"login": loginOf(state)})
		os.Exit(0)
	case "user/orgs":
		if state.OrgsForbidden {
			fmt.Fprintln(os.Stderr, "gh: Resource not accessible by personal access token (HTTP 403)")
			os.Exit(1)
		}
		if paginate && jq == ".[].login" {
			for _, org := range state.Orgs {
				fmt.Println(org)
			}
			os.Exit(0)
		}
		orgs := []map[string]string{}
		for _, org := range state.Orgs {
			orgs = append(orgs, map[string]string{"login": org})
		}
		emit(orgs)
		os.Exit(0)
	}
	parts := strings.Split(endpoint, "/")
	if len(parts) != 3 || parts[0] != "repos" || !silent {
		return false
	}
	for _, repository := range state.Repositories {
		if strings.EqualFold(repository.Owner, parts[1]) && strings.EqualFold(repository.Name, parts[2]) {
			os.Exit(0)
		}
	}
	fmt.Fprintln(os.Stderr, "gh: Not Found (HTTP 404)")
	os.Exit(1)
	return true //nolint:govet // os.Exit ends the process; the return keeps the compiler's flow analysis honest
}

// repoCreate answers `gh repo create <owner>/<name> --private|--public`: it records the repository with its
// visibility and makes a bare repository for it under the state's remotes folder, with a pre-receive hook that
// refuses a push while the state says RejectPush. It never clones, adds a remote, or pushes, and it takes no
// --internal, --source, or --push.
func repoCreate(dir string, args []string) {
	var private, public bool
	_, positional := parse("repo create", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&private, "private", false, "")
		fs.BoolVar(&public, "public", false, "")
	})
	if len(positional) != 1 || private == public {
		fail(1, "specify the repository as OWNER/NAME and exactly one of --public or --private")
	}
	owner, name, found := strings.Cut(positional[0], "/")
	if !found || owner == "" || name == "" {
		fail(1, "the repository is OWNER/NAME")
	}
	visibility := "private"
	if public {
		visibility = "public"
	}
	err := ghstate.Edit(dir, func(s *ghstate.State) error {
		if s.CreateError != "" {
			return fmt.Errorf("%s", s.CreateError)
		}
		for _, repository := range s.Repositories {
			if strings.EqualFold(repository.Owner, owner) && strings.EqualFold(repository.Name, name) {
				return fmt.Errorf("GraphQL: Name already exists on this account (createRepository)")
			}
		}
		bare := filepath.Join(dir, ghstate.RemotesDir, owner, name+".git")
		if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
			return err
		}
		if out, err := exec.Command("git", "init", "--bare", "--quiet", "--initial-branch=main", bare).CombinedOutput(); err != nil {
			return fmt.Errorf("git init --bare: %v: %s", err, out)
		}
		hook := fmt.Sprintf("#!/bin/sh\nexec %q %s\n", filepath.Join(dir, "gh"), preReceive)
		if err := os.MkdirAll(filepath.Join(bare, "hooks"), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(bare, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
			return err
		}
		s.Repositories = append(s.Repositories, ghstate.Repository{Owner: owner, Name: name, Visibility: visibility})
		return nil
	})
	if err != nil {
		fail(1, "%v", err)
	}
	fmt.Printf("https://github.com/%s/%s\n", owner, name)
}

// rejectPush is the pre-receive hook's answer: it refuses the push while the state says RejectPush.
func rejectPush(dir string) {
	state, err := ghstate.Read(dir)
	if err != nil {
		fail(2, "fakegh: %v", err)
	}
	if state.RejectPush {
		fail(1, "remote: push refused by the fake forge")
	}
	os.Exit(0)
}

// gitCredential answers `gh auth git-credential get`, which git runs as a credential helper: it prints a
// username and a password that name no real credential.
func gitCredential() {
	fmt.Println("username=x-access-token")
	fmt.Println("password=fakegh-token")
}
