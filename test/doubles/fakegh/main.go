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
	"slices"
	"strconv"
	"strings"
	"time"

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
	case len(args) >= 2 && args[0] == "pr" && args[1] == "merge":
		prMerge(dir, args[2:])
	case len(args) >= 2 && args[0] == "pr" && args[1] == "checks":
		prChecks(dir, args[2:])
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
			out[field] = mergeable(pr, state)
		case "mergeStateStatus":
			out[field] = mergeState(pr, state)
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

// prMerge merges a pull request on the remote the way GitHub does, with the refusals gh prints: a head that
// moved past --match-head-commit, a merge that conflicts, a branch behind its base, and a base whose
// protection refuses it. A merged pull request merges nothing again, and says so with success, as gh does.
func prMerge(dir string, args []string) {
	var repo, match string
	var merge, squash, rebase bool
	_, positional := parse("pr merge", args, func(fs *flag.FlagSet) {
		fs.StringVar(&repo, "repo", "", "")
		fs.StringVar(&match, "match-head-commit", "", "")
		fs.BoolVar(&merge, "merge", false, "")
		fs.BoolVar(&squash, "squash", false, "")
		fs.BoolVar(&rebase, "rebase", false, "")
	})
	method := ""
	for name, set := range map[string]bool{"merge": merge, "squash": squash, "rebase": rebase} {
		if set {
			if method != "" {
				fail(1, "specify only one of --merge, --rebase, or --squash")
			}
			method = name
		}
	}
	if method == "" {
		fail(1, "--merge, --rebase, or --squash required when not running interactively")
	}
	// gh prints a refusal and exits 1, which is a line for a person, not an error string.
	var notice, refusal string
	err := ghstate.Edit(dir, func(s *ghstate.State) error {
		pr := find(*s, repo, positional)
		target := s.Find(pr.Number)
		switch target.State {
		case "MERGED":
			notice = fmt.Sprintf("! Pull request %s#%d was already merged", repo, target.Number)
			return nil
		case "CLOSED":
			refusal = fmt.Sprintf("X Pull request %s#%d is closed", repo, target.Number)
			return nil
		}
		head := remoteHead(*s, target.Branch)
		if match != "" && match != head {
			refusal = "GraphQL: Head branch was modified. Review and try the merge again. (mergePullRequest)"
			return nil
		}
		notMergeable := map[string]string{
			"DIRTY":   "the merge commit cannot be cleanly created",
			"BLOCKED": "the base branch policy prohibits the merge",
			"BEHIND":  "the head branch is not up to date with the base branch",
		}
		if reason, ok := notMergeable[mergeState(*target, *s)]; ok {
			refusal = fmt.Sprintf("X Pull request %s#%d is not mergeable: %s.", repo, target.Number, reason)
			return nil
		}
		commit, err := mergeOnRemote(*s, *target, head, method)
		if errors.Is(err, errConflict) {
			refusal = fmt.Sprintf("X Pull request %s#%d is not mergeable: %s.", repo, target.Number, notMergeable["DIRTY"])
			return nil
		}
		if err != nil {
			return err
		}
		target.State, target.Head, target.MergeCommit, target.MergeMethod = "MERGED", head, commit, method
		target.MergedAt = time.Now().UTC().Format(time.RFC3339)
		notice = fmt.Sprintf("✓ Merged pull request %s#%d (%s)", repo, target.Number, target.Title)
		return nil
	})
	if err != nil {
		fail(1, "%v", err)
	}
	if refusal != "" {
		fail(1, "%s", refusal)
	}
	fmt.Fprintln(os.Stderr, notice)
}

// errConflict says a merge on the remote would conflict.
var errConflict = errors.New("the merge conflicts")

// prChecks lists a pull request's checks as `gh pr checks --json` does, each with its bucket. With
// --required it lists only those the base's protection requires, and refuses as gh does when there are none.
func prChecks(dir string, args []string) {
	var repo, fields string
	var required bool
	_, positional := parse("pr checks", args, func(fs *flag.FlagSet) {
		fs.StringVar(&repo, "repo", "", "")
		fs.StringVar(&fields, "json", "", "")
		fs.BoolVar(&required, "required", false, "")
	})
	state := read(dir)
	pr := find(state, repo, positional)
	if len(pr.Checks) == 0 {
		fail(1, "no checks reported on the '%s' branch", pr.Branch)
	}
	out := []map[string]any{}
	for _, check := range pr.Checks {
		name := cmpOr(check.Name, check.Context)
		if required && !slices.Contains(pr.Required, name) {
			continue
		}
		row := map[string]any{}
		for field := range strings.SplitSeq(fields, ",") {
			switch field {
			case "name":
				row[field] = name
			case "bucket":
				row[field] = bucketOf(check)
			case "state":
				row[field] = cmpOr(check.Conclusion, check.Status, check.State)
			case "link":
				row[field] = cmpOr(check.DetailsURL, check.TargetURL)
			default:
				fail(1, "Unknown JSON field: %q", field)
			}
		}
		out = append(out, row)
	}
	if required && len(out) == 0 {
		fail(1, "no required checks reported on the '%s' branch", pr.Branch)
	}
	emit(out)
}

func cmpOr(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// bucketOf sorts a check into the buckets `gh pr checks` names: pass, fail, pending, skipping, or cancel.
func bucketOf(check ghstate.Check) string {
	if check.Typename == "StatusContext" || (check.Name == "" && check.Context != "") {
		switch check.State {
		case "SUCCESS":
			return "pass"
		case "PENDING", "EXPECTED":
			return "pending"
		}
		return "fail"
	}
	switch {
	case check.Status != "COMPLETED":
		return "pending"
	case check.Conclusion == "SUCCESS" || check.Conclusion == "NEUTRAL":
		return "pass"
	case check.Conclusion == "SKIPPED":
		return "skipping"
	case check.Conclusion == "CANCELLED":
		return "cancel"
	}
	return "fail"
}

// mergeable is what GitHub says of merging the head into the base: CONFLICTING when the merge on the remote
// would conflict, unless the state names an answer.
func mergeable(pr ghstate.PullRequest, state ghstate.State) string {
	if pr.Mergeable != "" {
		return pr.Mergeable
	}
	if pr.State != "OPEN" {
		return "UNKNOWN"
	}
	bare, err := remotePath(state)
	if err != nil {
		return "MERGEABLE"
	}
	if _, clean, err := mergeTree(bare, pr.Base, remoteHead(state, pr.Branch)); err == nil && !clean {
		return "CONFLICTING"
	}
	return "MERGEABLE"
}

// mergeState is the pull request's mergeStateStatus: DIRTY for a conflict, BLOCKED while a required check
// has not passed, and CLEAN otherwise, unless the state names one.
func mergeState(pr ghstate.PullRequest, state ghstate.State) string {
	if pr.MergeState != "" {
		return pr.MergeState
	}
	if mergeable(pr, state) == "CONFLICTING" {
		return "DIRTY"
	}
	for _, check := range pr.Checks {
		if slices.Contains(pr.Required, cmpOr(check.Name, check.Context)) && bucketOf(check) != "pass" && bucketOf(check) != "skipping" {
			return "BLOCKED"
		}
	}
	return "CLEAN"
}

// remotePath is the directory of the remote that holds the pull requests' branches, which is a bare
// repository on this machine in every test.
func remotePath(state ghstate.State) (string, error) {
	remote := cmpOr(state.Remote, "origin")
	out, err := exec.Command("git", "remote", "get-url", remote).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// mergeTree merges head into the base branch on the remote with no worktree, and returns the tree it
// would make and whether it merged with no conflict.
func mergeTree(bare, base, head string) (string, bool, error) {
	if head == "" {
		return "", false, errors.New("no head to merge")
	}
	out, err := exec.Command("git", "-C", bare, "merge-tree", "--write-tree", "refs/heads/"+base, head).Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	tree, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(tree), true, nil
}

// mergeOnRemote lands head on the base branch of the remote with the method: a merge commit with both
// parents, or for a squash or a rebase one commit on the base's tip, as the forge would. The base moves only
// from the tip the merge read.
func mergeOnRemote(state ghstate.State, pr ghstate.PullRequest, head, method string) (string, error) {
	bare, err := remotePath(state)
	if err != nil {
		return "", err
	}
	tree, clean, err := mergeTree(bare, pr.Base, head)
	if err != nil {
		return "", err
	}
	if !clean {
		return "", errConflict
	}
	tip, err := exec.Command("git", "-C", bare, "rev-parse", "refs/heads/"+pr.Base).Output()
	if err != nil {
		return "", err
	}
	base := strings.TrimSpace(string(tip))
	args := []string{"-C", bare, "commit-tree", tree, "-p", base}
	if method == "merge" {
		args = append(args, "-p", head)
	}
	args = append(args, "-m", fmt.Sprintf("Merge pull request #%d from %s", pr.Number, pr.Branch))
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=GitHub", "GIT_AUTHOR_EMAIL=noreply@github.invalid", "GIT_COMMITTER_NAME=GitHub", "GIT_COMMITTER_EMAIL=noreply@github.invalid")
	made, err := cmd.Output()
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(string(made))
	if err := exec.Command("git", "-C", bare, "update-ref", "refs/heads/"+pr.Base, commit, base).Run(); err != nil {
		return "", err
	}
	return commit, nil
}
