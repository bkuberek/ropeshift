// Command fakegh stands for the GitHub CLI in tests, so no test reaches GitHub. It answers the commands
// the forge adapter runs from the state beside it (ghstate), records every call it takes, and reads an
// open pull request's head from the remote of the checkout it runs in, so a push moves the head as it
// would on GitHub. Anything it does not know exits 2.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/test/doubles/ghstate"
)

func main() {
	dir := filepath.Dir(os.Args[0])
	args := os.Args[1:]
	if err := record(dir, args); err != nil {
		fail(2, "fakegh: recording the call: %v", err)
	}
	switch {
	case len(args) >= 2 && args[0] == "auth" && args[1] == "status":
		authStatus(dir, args[2:])
	case len(args) >= 2 && args[0] == "pr" && args[1] == "list":
		prList(dir, args[2:])
	case len(args) >= 2 && args[0] == "pr" && args[1] == "create":
		prCreate(dir, args[2:])
	case len(args) >= 2 && args[0] == "pr" && args[1] == "view":
		prView(dir, args[2:])
	case len(args) >= 2 && args[0] == "pr" && args[1] == "comment":
		prComment(dir, args[2:])
	default:
		fail(2, "fakegh: unsupported command %q", strings.Join(args, " "))
	}
}

func fail(code int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(code)
}

func record(dir string, args []string) error {
	var seq int64
	err := ghstate.Edit(dir, func(s *ghstate.State) error {
		s.Calls++
		seq = s.Calls
		return nil
	})
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	env := map[string]string{}
	for _, name := range ghstate.Watched {
		if value, ok := os.LookupEnv(name); ok {
			env[name] = value
		}
	}
	return ghstate.Record(dir, ghstate.Call{Seq: seq, Args: args, Dir: cwd, Env: env})
}

func parse(name string, args []string, set func(*flag.FlagSet)) (*flag.FlagSet, []string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	set(fs)
	var positional []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			fail(2, "fakegh: %s: %v", name, err)
		}
		args = fs.Args()
		if len(args) > 0 {
			positional = append(positional, args[0])
			args = args[1:]
		}
	}
	return fs, positional
}

func authStatus(dir string, args []string) {
	var host string
	parse("auth status", args, func(fs *flag.FlagSet) { fs.StringVar(&host, "hostname", "", "") })
	state, err := ghstate.Read(dir)
	if err != nil {
		fail(2, "fakegh: %v", err)
	}
	if state.SignedOut {
		fail(1, "%s\n  X Failed to log in to %s: %s", host, host, state.AuthError)
	}
	fmt.Printf("%s\n  ✓ Logged in to %s account test (keyring)\n", host, host)
}

func prList(dir string, args []string) {
	var repo, head, stateName, fields string
	parse("pr list", args, func(fs *flag.FlagSet) {
		fs.StringVar(&repo, "repo", "", "")
		fs.StringVar(&head, "head", "", "")
		fs.StringVar(&stateName, "state", "open", "")
		fs.StringVar(&fields, "json", "", "")
	})
	state := read(dir)
	out := []map[string]any{}
	for _, pr := range state.PullRequests {
		if pr.Repo == repo && pr.Branch == head && (stateName == "all" || strings.EqualFold(pr.State, stateName)) {
			out = append(out, render(pr, state, fields))
		}
	}
	emit(out)
}

func prCreate(dir string, args []string) {
	var repo, base, head, title, bodyFile string
	parse("pr create", args, func(fs *flag.FlagSet) {
		fs.StringVar(&repo, "repo", "", "")
		fs.StringVar(&base, "base", "", "")
		fs.StringVar(&head, "head", "", "")
		fs.StringVar(&title, "title", "", "")
		fs.StringVar(&bodyFile, "body-file", "", "")
	})
	body, err := os.ReadFile(bodyFile)
	if err != nil {
		fail(1, "fakegh: reading the body: %v", err)
	}
	var created ghstate.PullRequest
	err = ghstate.Edit(dir, func(s *ghstate.State) error {
		if remoteHead(*s, head) == "" {
			return fmt.Errorf("pull request create failed: the remote holds no branch %s", head)
		}
		var number int64
		for _, pr := range s.PullRequests {
			if pr.Repo == repo && pr.Branch == head && pr.State == "OPEN" {
				return fmt.Errorf("a pull request for branch %q into branch %q already exists", head, base)
			}
			number = max(number, pr.Number)
		}
		created = ghstate.PullRequest{Number: number + 1, Repo: repo, Branch: head, Base: base, Title: title, Body: string(body), State: "OPEN"}
		s.PullRequests = append(s.PullRequests, created)
		return nil
	})
	if err != nil {
		fail(1, "%v", err)
	}
	fmt.Println(pullURL(repo, created.Number))
}

func prView(dir string, args []string) {
	var repo, fields string
	_, positional := parse("pr view", args, func(fs *flag.FlagSet) {
		fs.StringVar(&repo, "repo", "", "")
		fs.StringVar(&fields, "json", "", "")
	})
	state := read(dir)
	pr := find(state, repo, positional)
	emit(render(*pr, state, fields))
}

func prComment(dir string, args []string) {
	var repo, bodyFile string
	_, positional := parse("pr comment", args, func(fs *flag.FlagSet) {
		fs.StringVar(&repo, "repo", "", "")
		fs.StringVar(&bodyFile, "body-file", "", "")
	})
	body, err := os.ReadFile(bodyFile)
	if err != nil {
		fail(1, "fakegh: reading the body: %v", err)
	}
	var number int64
	err = ghstate.Edit(dir, func(s *ghstate.State) error {
		pr := find(*s, repo, positional)
		number = pr.Number
		target := s.Find(pr.Number)
		target.Comments = append(target.Comments, string(body))
		return nil
	})
	if err != nil {
		fail(1, "%v", err)
	}
	fmt.Println(pullURL(repo, number) + "#issuecomment-1")
}

func read(dir string) ghstate.State {
	state, err := ghstate.Read(dir)
	if err != nil {
		fail(2, "fakegh: %v", err)
	}
	return state
}

func find(state ghstate.State, repo string, positional []string) *ghstate.PullRequest {
	if len(positional) != 1 {
		fail(2, "fakegh: name one pull request")
	}
	number, err := strconv.ParseInt(positional[0], 10, 64)
	if err != nil {
		fail(2, "fakegh: %q is not a pull request number", positional[0])
	}
	pr := state.Find(number)
	if pr == nil || pr.Repo != repo {
		fail(1, "GraphQL: Could not resolve to a PullRequest with the number of %d.", number)
	}
	return pr
}

func pullURL(repo string, number int64) string {
	return "https://" + repo + "/pull/" + strconv.FormatInt(number, 10)
}

func emit(value any) {
	raw, err := json.Marshal(value)
	if err != nil {
		fail(2, "fakegh: %v", err)
	}
	fmt.Println(string(raw))
}

// render gives the fields --json asked for, and refuses one this double does not know, as gh refuses a
// field it does not have.
func render(pr ghstate.PullRequest, state ghstate.State, fields string) map[string]any {
	head := pr.Head
	if pr.State == "OPEN" {
		if live := remoteHead(state, pr.Branch); live != "" {
			head = live
		}
	}
	out := map[string]any{}
	for field := range strings.SplitSeq(fields, ",") {
		switch field {
		case "number":
			out[field] = pr.Number
		case "url":
			out[field] = pullURL(pr.Repo, pr.Number)
		case "state":
			out[field] = pr.State
		case "baseRefName":
			out[field] = pr.Base
		case "headRefName":
			out[field] = pr.Branch
		case "headRefOid":
			out[field] = head
		case "mergeable":
			out[field] = "MERGEABLE"
		case "mergedAt":
			out[field] = pr.MergedAt
		case "mergeCommit":
			if pr.MergeCommit == "" {
				out[field] = nil
			} else {
				out[field] = map[string]string{"oid": pr.MergeCommit}
			}
		case "statusCheckRollup":
			checks := pr.Checks
			if checks == nil {
				checks = []ghstate.Check{}
			}
			out[field] = checks
		case "comments":
			comments := []map[string]string{}
			for _, body := range pr.Comments {
				comments = append(comments, map[string]string{"body": body})
			}
			out[field] = comments
		default:
			fail(1, "Unknown JSON field: %q", field)
		}
	}
	return out
}

// remoteHead reads what the checkout's remote holds for a branch, or nothing.
func remoteHead(state ghstate.State, branch string) string {
	remote := state.Remote
	if remote == "" {
		remote = "origin"
	}
	out, err := exec.Command("git", "ls-remote", remote, "refs/heads/"+branch).Output()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return ""
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
