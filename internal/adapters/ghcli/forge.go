// Package ghcli is the Forge port's GitHub adapter (ADR-0020). It runs the GitHub CLI on the worker, in
// the repository's checkout, under the person's existing sign-in. It never signs in, never asks for the
// token, and never stores one: the only sign-in command it runs is the status read.
package ghcli

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// Binary is the name the CLI goes by on PATH.
const Binary = "gh"

// pullRequestFields are what a read asks `gh pr view --json` and `gh pr list --json` for
// (https://cli.github.com/manual/gh_pr_view).
const pullRequestFields = "number,url,state,baseRefName,headRefName,headRefOid,mergeable,mergedAt,mergeCommit,statusCheckRollup"

// mergeFields are what a merge reads of a pull request before it merges and after, to say what the merge
// came to: mergeStateStatus is DIRTY for a conflict and BEHIND for a branch an up-to-date base refuses.
const mergeFields = "state,headRefOid,mergeable,mergeStateStatus,mergeCommit,mergedAt"

// requiredFields are what a read asks `gh pr checks --required --json` for: the checks the base branch's
// protection requires (https://cli.github.com/manual/gh_pr_checks).
const requiredFields = "name,bucket,link"

// maxDetail bounds what a failure's text carries into the ledger.
const maxDetail = 2000

// Forge runs `gh`. Each call looks the binary up again, so a `gh` installed after the worker started is
// found, and one removed reads as missing.
type Forge struct {
	// Path is the `gh` to run: an absolute path, or a name looked up on PATH. Empty is Binary.
	Path string
	// Env is the whole environment `gh` runs with, where it finds the person's sign-in. Nil is this
	// process's environment.
	Env []string
	// TempDir is where a body file is written for one command and removed after it, outside every
	// worktree. Empty is the system's.
	TempDir string
}

var _ ports.Forge = Forge{}

// ghError is a run of `gh` that ended badly, with what it wrote to stderr.
type ghError struct {
	args   []string
	code   int
	stderr string
}

func (e *ghError) Error() string {
	return fmt.Sprintf("gh %s: exit %d: %s", strings.Join(e.args, " "), e.code, e.stderr)
}

func (f Forge) binary() (string, error) {
	name := cmp.Or(f.Path, Binary)
	found, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%w: %s is not on PATH: %v", ports.ErrForgeUnavailable, name, err)
	}
	return found, nil
}

func (f Forge) environment() []string {
	base := f.Env
	if base == nil {
		base = os.Environ()
	}
	// A worker has no terminal to prompt on, and a notice of a new version is no answer to parse.
	return append(base[:len(base):len(base)], "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1")
}

// run runs one command in a checkout and returns its stdout.
func (f Forge) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	binary, err := f.binary()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir, cmd.Env = dir, f.environment()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return nil, &ghError{args: args, code: exit.ExitCode(), stderr: clip(stderr.String())}
	}
	if err != nil {
		return nil, fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}
	return stdout.Bytes(), nil
}

func clip(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > maxDetail {
		text = text[:maxDetail]
	}
	return text
}

// withBody writes a body to a file of its own outside every worktree, runs a command that names it, and
// removes it.
func (f Forge) withBody(body string, run func(file string) error) error {
	file, err := os.CreateTemp(f.TempDir, "ropeshift-gh-body-*.md")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.WriteString(body)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return run(file.Name())
}

// Health reads `gh auth status --hostname <host>`, which exits with 1 when the sign-in has a problem
// (https://cli.github.com/manual/gh_auth_status). It never asks for the token.
func (f Forge) Health(ctx context.Context, repo ports.ForgeRepo) (ledger.ForgeHealthReport, error) {
	_, err := f.run(ctx, repo.Dir, "auth", "status", "--hostname", repo.Binding.Host)
	var failed *ghError
	switch {
	case err == nil:
		return ledger.ForgeHealthReport{State: ledger.ForgeHealthy}, nil
	case errors.Is(err, ports.ErrForgeUnavailable):
		return ledger.ForgeHealthReport{State: ledger.ForgeMissing, Detail: err.Error()}, nil
	case errors.As(err, &failed):
		return ledger.ForgeHealthReport{State: ledger.ForgeSignedOut, Detail: cmp.Or(failed.stderr, failed.Error())}, nil
	}
	return ledger.ForgeHealthReport{}, err
}

// OpenPullRequest finds the open pull request of the branch with `gh pr list --head`, which answers an
// empty list when there is none, and opens one with `gh pr create` only then
// (https://cli.github.com/manual/gh_pr_list, https://cli.github.com/manual/gh_pr_create).
func (f Forge) OpenPullRequest(ctx context.Context, spec ports.PullRequestSpec) (ledger.PullRequest, error) {
	repo := spec.Repo.Binding.String()
	open, err := f.openFor(ctx, spec.Repo, spec.Branch)
	if err != nil || open != nil {
		return deref(open), err
	}
	var created []byte
	err = f.withBody(spec.Body, func(file string) error {
		var err error
		created, err = f.run(ctx, spec.Repo.Dir, "pr", "create", "--repo", repo, "--base", spec.Base, "--head", spec.Branch, "--title", spec.Title, "--body-file", file)
		return err
	})
	if err != nil {
		return ledger.PullRequest{}, err
	}
	number, err := numberOf(created)
	if err != nil {
		return ledger.PullRequest{}, err
	}
	return f.PullRequest(ctx, ports.PullRequestRef{Repo: spec.Repo, Number: number})
}

func deref(pr *ledger.PullRequest) ledger.PullRequest {
	if pr == nil {
		return ledger.PullRequest{}
	}
	return *pr
}

func (f Forge) openFor(ctx context.Context, repo ports.ForgeRepo, branch string) (*ledger.PullRequest, error) {
	out, err := f.run(ctx, repo.Dir, "pr", "list", "--repo", repo.Binding.String(), "--head", branch, "--state", "open", "--json", pullRequestFields)
	if err != nil {
		return nil, err
	}
	var listed []viewed
	if err := json.Unmarshal(out, &listed); err != nil {
		return nil, fmt.Errorf("reading gh pr list: %w", err)
	}
	if len(listed) == 0 {
		return nil, nil
	}
	pr := listed[0].pullRequest()
	return &pr, nil
}

// numberOf reads the number from the URL `gh pr create` prints on its last line.
func numberOf(out []byte) (int64, error) {
	lines := strings.Fields(string(out))
	if len(lines) == 0 {
		return 0, errors.New("gh pr create printed no pull request")
	}
	url := lines[len(lines)-1]
	number, err := strconv.ParseInt(path.Base(url), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("gh pr create printed %q, which names no pull request", url)
	}
	return number, nil
}

// PullRequest reads one pull request with `gh pr view <number> --json`, then the checks the base branch
// requires with `gh pr checks <number> --required`, which it marks on the head's checks.
func (f Forge) PullRequest(ctx context.Context, ref ports.PullRequestRef) (ledger.PullRequest, error) {
	number, repo := strconv.FormatInt(ref.Number, 10), ref.Repo.Binding.String()
	out, err := f.run(ctx, ref.Repo.Dir, "pr", "view", number, "--repo", repo, "--json", pullRequestFields)
	if err != nil {
		return ledger.PullRequest{}, err
	}
	var read viewed
	if err := json.Unmarshal(out, &read); err != nil {
		return ledger.PullRequest{}, fmt.Errorf("reading gh pr view: %w", err)
	}
	pr := read.pullRequest()
	if pr.State != ledger.PullRequestOpen {
		return pr, nil
	}
	required, err := f.required(ctx, ref.Repo.Dir, repo, number)
	if err != nil {
		return ledger.PullRequest{}, err
	}
	for i := range pr.Checks {
		pr.Checks[i].Required = required[pr.Checks[i].Name]
	}
	return pr, nil
}

// required names the checks the base branch's protection requires of a pull request. gh refuses with "no
// required checks reported" when there are none, and with "no checks reported" when nothing ran, and each
// of those is an empty answer. With --json, gh exits 0 whatever the checks say.
func (f Forge) required(ctx context.Context, dir, repo, number string) (map[string]bool, error) {
	out, err := f.run(ctx, dir, "pr", "checks", number, "--repo", repo, "--required", "--json", requiredFields)
	var failed *ghError
	if errors.As(err, &failed) && strings.Contains(failed.stderr, "checks reported on the") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var listed []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		return nil, fmt.Errorf("reading gh pr checks: %w", err)
	}
	names := make(map[string]bool, len(listed))
	for _, check := range listed {
		names[check.Name] = true
	}
	return names, nil
}

// methodFlags are the flags of `gh pr merge` that name each merge method.
var methodFlags = map[ledger.MergeMethod]string{ledger.MergeCommit: "--merge", ledger.MergeSquash: "--squash", ledger.MergeRebase: "--rebase"}

// Merge reads the pull request's merge state, and runs `gh pr merge <number> --match-head-commit <head>`
// with the method's flag only when it is open at that head and merges cleanly, then reads it again to say
// what the merge came to (https://cli.github.com/manual/gh_pr_merge). It never passes --admin, --auto, or
// --delete-branch. A pull request merged at that head is a merge an earlier attempt made, and merges nothing
// again. What gh refuses is a refusal with what it said; a gh that cannot run, or a read that fails, is an
// error, which a later attempt asks again.
func (f Forge) Merge(ctx context.Context, ref ports.PullRequestRef, head string, method ledger.MergeMethod) (ledger.MergeResult, error) {
	flag, ok := methodFlags[method]
	if !ok {
		return ledger.MergeResult{}, fmt.Errorf("no forge merges with the method %q", method)
	}
	before, err := f.mergeState(ctx, ref)
	if err != nil {
		return ledger.MergeResult{}, err
	}
	if result, settled := before.settled(head); settled {
		return result, nil
	}
	_, merged := f.run(ctx, ref.Repo.Dir, "pr", "merge", strconv.FormatInt(ref.Number, 10), "--repo", ref.Repo.Binding.String(), "--match-head-commit", head, flag)
	var refused *ghError
	if merged != nil && !errors.As(merged, &refused) {
		return ledger.MergeResult{}, merged
	}
	after, err := f.mergeState(ctx, ref)
	if err != nil {
		return ledger.MergeResult{}, err
	}
	if result, settled := after.settled(head); settled {
		return result, nil
	}
	if refused == nil {
		return ledger.MergeResult{}, fmt.Errorf("gh merged pull request %d, which does not read as merged yet", ref.Number)
	}
	return ledger.MergeResult{Outcome: ledger.MergeRefused, Detail: cmp.Or(refused.stderr, refused.Error())}, nil
}

// mergeStateOf is a pull request as `gh pr view --json` writes the fields a merge reads.
type mergeStateOf struct {
	State            string `json:"state"`
	HeadRefOid       string `json:"headRefOid"`
	Mergeable        string `json:"mergeable"`
	MergeStateStatus string `json:"mergeStateStatus"`
	MergedAt         string `json:"mergedAt"`
	MergeCommit      *struct {
		Oid string `json:"oid"`
	} `json:"mergeCommit"`
}

func (f Forge) mergeState(ctx context.Context, ref ports.PullRequestRef) (mergeStateOf, error) {
	out, err := f.run(ctx, ref.Repo.Dir, "pr", "view", strconv.FormatInt(ref.Number, 10), "--repo", ref.Repo.Binding.String(), "--json", mergeFields)
	if err != nil {
		return mergeStateOf{}, err
	}
	var read mergeStateOf
	if err := json.Unmarshal(out, &read); err != nil {
		return mergeStateOf{}, fmt.Errorf("reading gh pr view: %w", err)
	}
	return read, nil
}

// settled says what a pull request's state answers for a merge at head with no merge run: merged at that
// head, merged or closed otherwise, a head that moved, a conflict, or a branch behind its base. An open pull
// request at head that merges cleanly answers nothing.
func (s mergeStateOf) settled(head string) (ledger.MergeResult, bool) {
	switch {
	case s.State == "MERGED" && s.HeadRefOid == head:
		result := ledger.MergeResult{Outcome: ledger.MergeMerged, MergedAt: s.MergedAt}
		if s.MergeCommit != nil {
			result.MergeCommit = s.MergeCommit.Oid
		}
		return result, true
	case s.State == "MERGED":
		return ledger.MergeResult{Outcome: ledger.MergeRefused, Detail: fmt.Sprintf("the pull request merged at %s, not at the reviewed commit %s", s.HeadRefOid, head)}, true
	case s.State == "CLOSED":
		return ledger.MergeResult{Outcome: ledger.MergeRefused, Detail: "the pull request is closed"}, true
	case s.HeadRefOid != head:
		return ledger.MergeResult{Outcome: ledger.MergeStaleHead, Detail: fmt.Sprintf("the pull request's head is %s, not the reviewed commit %s", s.HeadRefOid, head)}, true
	case s.Mergeable == "CONFLICTING" || s.MergeStateStatus == "DIRTY":
		return ledger.MergeResult{Outcome: ledger.MergeConflict, Detail: "the merge commit cannot be cleanly created"}, true
	case s.MergeStateStatus == "BEHIND":
		return ledger.MergeResult{Outcome: ledger.MergeBehind, Detail: "the head branch is not up to date with the base branch"}, true
	}
	return ledger.MergeResult{}, false
}

// Comment reads the pull request's comments first and posts with `gh pr comment --body-file` only when
// none carries the key, which the comment ends with as a line nobody reads in the rendered page
// (https://cli.github.com/manual/gh_pr_comment).
func (f Forge) Comment(ctx context.Context, ref ports.PullRequestRef, key, body string) error {
	repo, number := ref.Repo.Binding.String(), strconv.FormatInt(ref.Number, 10)
	mark := marker(key)
	out, err := f.run(ctx, ref.Repo.Dir, "pr", "view", number, "--repo", repo, "--json", "comments")
	if err != nil {
		return err
	}
	var read struct {
		Comments []struct {
			Body string `json:"body"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(out, &read); err != nil {
		return fmt.Errorf("reading gh pr view: %w", err)
	}
	for _, comment := range read.Comments {
		if strings.Contains(comment.Body, mark) {
			return nil
		}
	}
	return f.withBody(strings.TrimRight(body, "\n")+"\n\n"+mark+"\n", func(file string) error {
		_, err := f.run(ctx, ref.Repo.Dir, "pr", "comment", number, "--repo", repo, "--body-file", file)
		return err
	})
}

// marker is the line that names a comment's key. A key cannot close the comment it sits in.
func marker(key string) string {
	return "<!-- ropeshift:" + strings.ReplaceAll(key, "--", "-") + " -->"
}

// viewed is a pull request as `gh pr view --json` writes it.
type viewed struct {
	Number      int64  `json:"number"`
	URL         string `json:"url"`
	State       string `json:"state"`
	BaseRefName string `json:"baseRefName"`
	HeadRefName string `json:"headRefName"`
	HeadRefOid  string `json:"headRefOid"`
	Mergeable   string `json:"mergeable"`
	MergedAt    string `json:"mergedAt"`
	MergeCommit *struct {
		Oid string `json:"oid"`
	} `json:"mergeCommit"`
	StatusCheckRollup []rollup `json:"statusCheckRollup"`
}

// rollup is one entry of statusCheckRollup: a check run, with a name, a status, and a conclusion, or a
// commit status, with a context and a state.
type rollup struct {
	Typename   string `json:"__typename"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	DetailsURL string `json:"detailsUrl"`
	Context    string `json:"context"`
	State      string `json:"state"`
	TargetURL  string `json:"targetUrl"`
}

func (v viewed) pullRequest() ledger.PullRequest {
	pr := ledger.PullRequest{
		Number: v.Number, URL: v.URL, State: stateOf(v.State), Base: v.BaseRefName, Branch: v.HeadRefName,
		HeadCommit: v.HeadRefOid, Mergeable: v.Mergeable, MergedAt: v.MergedAt,
	}
	if v.MergeCommit != nil {
		pr.MergeCommit = v.MergeCommit.Oid
	}
	for _, entry := range v.StatusCheckRollup {
		pr.Checks = append(pr.Checks, entry.check())
	}
	slices.SortFunc(pr.Checks, func(a, b ledger.PullRequestCheck) int { return strings.Compare(a.Name, b.Name) })
	return pr
}

func stateOf(state string) ledger.PullRequestState {
	switch state {
	case "MERGED":
		return ledger.PullRequestMerged
	case "CLOSED":
		return ledger.PullRequestClosed
	}
	return ledger.PullRequestOpen
}

func (r rollup) check() ledger.PullRequestCheck {
	if r.Typename == "StatusContext" || (r.Name == "" && r.Context != "") {
		check := ledger.PullRequestCheck{Name: r.Context, DetailsURI: r.TargetURL, State: ledger.CheckFailed}
		switch r.State {
		case "SUCCESS":
			check.State = ledger.CheckPassed
		case "PENDING", "EXPECTED":
			check.State = ledger.CheckPending
		}
		return check
	}
	check := ledger.PullRequestCheck{Name: r.Name, DetailsURI: r.DetailsURL, State: ledger.CheckFailed}
	switch {
	case r.Status != "COMPLETED":
		check.State = ledger.CheckPending
	case r.Conclusion == "SUCCESS" || r.Conclusion == "NEUTRAL" || r.Conclusion == "SKIPPED":
		check.State = ledger.CheckPassed
	}
	return check
}
