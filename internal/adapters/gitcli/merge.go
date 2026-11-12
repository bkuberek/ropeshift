package gitcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

var _ ports.BranchMerger = Worktrees{}

// noHooks keeps every git command of a merge from running a hook the repository holds, through git's
// configuration in the environment (https://git-scm.com/docs/git-config#ENVIRONMENT): a merge is the worker's
// effect, and runs no code from the repository it merges.
var noHooks = []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=" + os.DevNull}

// The trailers a commit a merge makes carries, which is how a repeat finds it.
const (
	trailerWorkOrder = "Ropeshift-Work-Order: "
	trailerReviewed  = "Ropeshift-Reviewed-Commit: "
)

// ran is one git command's result, with what it wrote to stderr and how it exited.
type ran struct {
	out    string
	stderr string
	code   int
}

// run runs one command and keeps a non-zero exit as a result, because a merge reads how git refused. Only a
// git that could not run is an error.
func (g Git) run(ctx context.Context, args ...string) (ran, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir, cmd.Env = g.Dir, append(g.environment(), noHooks...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := ran{out: strings.TrimSpace(stdout.String()), stderr: strings.TrimSpace(stderr.String())}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		result.code = exit.ExitCode()
		return result, nil
	}
	return result, err
}

// must runs a command that has to succeed.
func (g Git) must(ctx context.Context, args ...string) (string, error) {
	result, err := g.run(ctx, args...)
	if err != nil {
		return "", err
	}
	if result.code != 0 {
		return "", fmt.Errorf("git %s: exit %d: %s", strings.Join(args, " "), result.code, result.stderr)
	}
	return result.out, nil
}

// MergeBranch merges a reviewed branch into its base (ADR-0020). It reads the base's tip, from the remote when
// there is one, and answers merged when a commit on the base already names the reviewed commit, or the base
// already holds it. Otherwise it checks that the branch is still at the reviewed commit, merges in a worktree
// detached at the base's tip with the method, and publishes the result: a push to the remote's base branch
// that never forces and runs no hook, or with no remote a compare-and-swap of the local base branch, which it
// refuses while any worktree has the base checked out.
func (w Worktrees) MergeBranch(ctx context.Context, repository string, merge ports.BranchMerge) (ledger.MergeResult, error) {
	git := w.git(repository)
	remote, err := remoteOf(ctx, git, merge.Base)
	if err != nil {
		return ledger.MergeResult{}, err
	}
	tip, err := baseTip(ctx, git, remote, merge.Base)
	if err != nil {
		return ledger.MergeResult{}, err
	}
	if done, landed, err := alreadyMerged(ctx, git, merge.Commit, tip); err != nil || landed {
		return done, err
	}
	branch, err := git.Commit(ctx, "refs/heads/"+merge.Branch)
	if err != nil {
		return ledger.MergeResult{Outcome: ledger.MergeStaleHead, Detail: fmt.Sprintf("branch %s is gone, and the reviewed commit was %s", merge.Branch, merge.Commit)}, nil
	}
	if branch != merge.Commit {
		return ledger.MergeResult{Outcome: ledger.MergeStaleHead, Detail: fmt.Sprintf("branch %s is at %s, not the reviewed commit %s", merge.Branch, branch, merge.Commit)}, nil
	}
	if remote == "" {
		if holder, err := checkedOut(ctx, git, merge.Base); err != nil || holder != "" {
			detail := fmt.Sprintf("the base branch %s is checked out in %s, and moving it would change files under you; merge it there, or switch that worktree to another branch", merge.Base, holder)
			return ledger.MergeResult{Outcome: ledger.MergeRefused, Detail: detail}, err
		}
	}
	made, refusal, err := w.mergeAt(ctx, repository, tip, merge)
	if err != nil || refusal != nil {
		return deref(refusal), err
	}
	return publish(ctx, git, remote, merge.Base, tip, made)
}

func deref(result *ledger.MergeResult) ledger.MergeResult {
	if result == nil {
		return ledger.MergeResult{}
	}
	return *result
}

// baseTip reads the base's tip: the remote's, fetched so this repository holds it, or with no remote the
// local branch's.
func baseTip(ctx context.Context, git Git, remote, base string) (string, error) {
	if remote == "" {
		return git.Commit(ctx, "refs/heads/"+base)
	}
	listed, err := git.must(ctx, "ls-remote", remote, "refs/heads/"+base)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(listed)
	if len(fields) == 0 {
		return "", fmt.Errorf("the remote %s holds no branch %s", remote, base)
	}
	if _, err := git.must(ctx, "fetch", "--quiet", "--no-tags", remote, "refs/heads/"+base); err != nil {
		return "", err
	}
	return fields[0], nil
}

// alreadyMerged finds the merge an earlier attempt made: a commit on the base since the branch left it that
// names the reviewed commit as a trailer. A base that holds the reviewed commit itself, which a person who
// merged it by hand leaves, is merged too.
func alreadyMerged(ctx context.Context, git Git, commit, tip string) (ledger.MergeResult, bool, error) {
	span := tip
	if fork, err := git.run(ctx, "merge-base", commit, tip); err != nil {
		return ledger.MergeResult{}, false, err
	} else if fork.code == 0 && fork.out != "" {
		span = fork.out + ".." + tip
	}
	found, err := git.must(ctx, "log", "--format=%H", "-n", "1", "--fixed-strings", "--grep="+trailerReviewed+commit, span)
	if err != nil {
		return ledger.MergeResult{}, false, err
	}
	if found != "" {
		return merged(found), true, nil
	}
	held, err := git.run(ctx, "merge-base", "--is-ancestor", commit, tip)
	if err != nil || held.code != 0 {
		return ledger.MergeResult{}, false, err
	}
	return merged(tip), true, nil
}

func merged(commit string) ledger.MergeResult {
	return ledger.MergeResult{Outcome: ledger.MergeMerged, MergeCommit: commit, MergedAt: time.Now().UTC().Format(time.RFC3339)}
}

// checkedOut names a worktree of the repository that has a branch checked out, or nothing.
func checkedOut(ctx context.Context, git Git, branch string) (string, error) {
	listed, err := git.must(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	var path string
	for line := range strings.SplitSeq(listed, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			path = p
		}
		if line == "branch refs/heads/"+branch {
			return path, nil
		}
	}
	return "", nil
}

// mergeAt makes the merge in a worktree of its own, detached at the base's tip, and returns the commit it
// made, or the conflict that stopped it. The worktree goes when it returns, whatever happened in it.
func (w Worktrees) mergeAt(ctx context.Context, repository, tip string, merge ports.BranchMerge) (string, *ledger.MergeResult, error) {
	git := w.git(repository)
	dir, err := os.MkdirTemp("", "ropeshift-merge-")
	if err != nil {
		return "", nil, err
	}
	defer func() {
		_, _ = git.run(context.WithoutCancel(ctx), "worktree", "remove", "--force", dir)
		_ = os.RemoveAll(dir)
		_, _ = git.run(context.WithoutCancel(ctx), "worktree", "prune")
	}()
	if _, err := git.must(ctx, "worktree", "add", "--quiet", "--detach", dir, tip); err != nil {
		return "", nil, err
	}
	in := w.git(dir)
	if name, err := in.run(ctx, "config", "user.email"); err != nil {
		return "", nil, err
	} else if name.code != 0 || name.out == "" {
		in.Extra = snapshotIdentity
	}
	message := fmt.Sprintf("%s\n\n%s%s\n%s%s\n", merge.Title, trailerWorkOrder, merge.WorkOrder, trailerReviewed, merge.Commit)
	var stopped ran
	switch merge.Method {
	case ledger.MergeCommit:
		stopped, err = in.run(ctx, "merge", "--no-ff", "--no-edit", "-m", message, merge.Commit)
	case ledger.MergeSquash:
		if stopped, err = in.run(ctx, "merge", "--squash", merge.Commit); err == nil && stopped.code == 0 {
			_, err = in.must(ctx, "commit", "--allow-empty", "-m", message)
		}
	case ledger.MergeRebase:
		stopped, err = replay(ctx, in, tip, merge, message)
	default:
		return "", nil, fmt.Errorf("git merges with no method %q", merge.Method)
	}
	if err != nil {
		return "", nil, err
	}
	if stopped.code != 0 {
		files, err := in.must(ctx, "diff", "--name-only", "--diff-filter=U")
		if err != nil {
			return "", nil, err
		}
		if files == "" {
			return "", nil, fmt.Errorf("git could not merge %s into %s: %s", merge.Branch, merge.Base, stopped.stderr+stopped.out)
		}
		return "", &ledger.MergeResult{Outcome: ledger.MergeConflict, Files: strings.Fields(files), Detail: "the branch does not merge cleanly into " + merge.Base}, nil
	}
	made, err := in.Commit(ctx, "HEAD")
	return made, nil, err
}

// replay puts the branch's commits since it left the base onto the base's tip, one by one, and adds the
// trailers to the last, so the base's history is one line and a repeat still finds the merge.
func replay(ctx context.Context, in Git, tip string, merge ports.BranchMerge, message string) (ran, error) {
	fork, err := in.must(ctx, "merge-base", merge.Commit, tip)
	if err != nil {
		return ran{}, err
	}
	commits, err := in.must(ctx, "rev-list", "--reverse", "--no-merges", fork+".."+merge.Commit)
	if err != nil {
		return ran{}, err
	}
	for _, commit := range strings.Fields(commits) {
		picked, err := in.run(ctx, "cherry-pick", "--allow-empty", "--keep-redundant-commits", commit)
		if err != nil || picked.code != 0 {
			return picked, err
		}
	}
	last, err := in.must(ctx, "log", "-1", "--format=%B")
	if err != nil {
		return ran{}, err
	}
	trailers := strings.TrimPrefix(message, merge.Title+"\n\n")
	_, err = in.must(ctx, "commit", "--amend", "--allow-empty", "-m", strings.TrimRight(last, "\n")+"\n\n"+trailers)
	return ran{}, err
}

// publish lands the merge on the base: a push to the remote that never forces and runs no hook, where a
// refusal for any reason but a base that moved is the remote's answer; or with no remote, a move of the local
// base branch from the tip the merge started at. A base that moved meanwhile is an error, and the next attempt
// merges onto where it went.
func publish(ctx context.Context, git Git, remote, base, tip, made string) (ledger.MergeResult, error) {
	if remote == "" {
		if _, err := git.must(ctx, "update-ref", "refs/heads/"+base, made, tip); err != nil {
			return ledger.MergeResult{}, fmt.Errorf("the base branch %s moved while the merge ran: %w", base, err)
		}
		return merged(made), nil
	}
	pushed, err := git.run(ctx, "push", "--quiet", "--no-verify", remote, made+":refs/heads/"+base)
	if err != nil {
		return ledger.MergeResult{}, err
	}
	if pushed.code == 0 {
		return merged(made), nil
	}
	if strings.Contains(pushed.stderr, "fetch first") || strings.Contains(pushed.stderr, "non-fast-forward") {
		return ledger.MergeResult{}, fmt.Errorf("the base branch %s on %s moved while the merge ran: %s", base, remote, pushed.stderr)
	}
	return ledger.MergeResult{Outcome: ledger.MergeRefused, Detail: fmt.Sprintf("the remote %s refused the push to %s: %s", remote, base, pushed.stderr)}, nil
}
