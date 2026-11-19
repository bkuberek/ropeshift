package ghcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// What the control plane asks of a forge once a head's checks finish (ADR-0037): the failed jobs rerun, the log of what
// failed, and whether the base branch's tip fails the same checks.

// apiError is an answer of `gh api` with a status past 299 that is no rate limit. The statuses of the 4xx are the
// forge's word on the request itself, which asking again repeats.
type apiError struct {
	args    []string
	Status  int
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("gh api %s: HTTP %d: %s", strings.Join(e.args, " "), e.Status, e.Message)
}

// refused says the forge answered the request and said no, as a 4xx other than a rate limit does.
func (e *apiError) refused() bool { return e.Status >= 400 && e.Status < 500 && e.Status != 429 }

// tailBuffer keeps the last max bytes written to it, so a log of any length is read in bounded memory.
type tailBuffer struct {
	max  int
	data []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.data = append(t.data, p...)
	if len(t.data) > 2*t.max {
		t.data = append([]byte(nil), t.data[len(t.data)-t.max:]...)
	}
	return len(p), nil
}

// text is the last max bytes, starting on a character.
func (t *tailBuffer) text() string {
	data := t.data
	if len(data) > t.max {
		data = data[len(data)-t.max:]
	}
	for len(data) > 0 && !utf8.RuneStart(data[0]) {
		data = data[1:]
	}
	return string(data)
}

// runTail runs one command in a checkout and returns the last max bytes of its stdout. Its stderr is read to
// maxErrorOutput, and what a failure carries of it is sanitized and clipped. A command the forge refused for a rate
// limit is a RateLimitedError.
func (f Forge) runTail(ctx context.Context, dir string, max int, args ...string) (string, error) {
	binary, err := f.binary()
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir, cmd.Env = dir, f.environment()
	cmd.WaitDelay = time.Second
	tail, stderr := &tailBuffer{max: max}, &boundedWriter{limit: maxErrorOutput, truncate: true}
	cmd.Stdout, cmd.Stderr = tail, stderr
	err = cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		failed := &ghError{args: args, code: exit.ExitCode(), stderr: clip(ledger.SanitizeCIText(stderr.buf.String()))}
		if limited, ok := rateLimitedText(failed.stderr); ok {
			return "", limited
		}
		return "", failed
	}
	// A command that exited with success and left a child holding its output open has answered: what it wrote is read.
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return "", fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}
	return tail.text(), nil
}

// FailedLog reads what failed in a check's run with `gh run view <run> --log-failed`, narrowed to the check's job
// when its address names one (https://cli.github.com/manual/gh_run_view). Only the last maxBytes of it are kept. A
// check whose address is no page of this repository's GitHub Actions has no log to read, and answers none.
func (f Forge) FailedLog(ctx context.Context, repo ports.ForgeRepo, head string, check ledger.CICheckRef, maxBytes int) (string, error) {
	run, job, ok := ledger.ActionsRun(check.URL, repo.Binding)
	if !ok {
		return "", nil
	}
	if err := f.runOfHead(ctx, repo, run, head); err != nil {
		return "", err
	}
	args := []string{"run", "view", run, "--repo", repo.Binding.String(), "--log-failed"}
	if job != "" {
		args = append(args, "--job", job)
	}
	return f.runTail(ctx, repo.Dir, maxBytes, args...)
}

// runOfHead says a workflow run belongs to a head, by GET /repos/{owner}/{repo}/actions/runs/{run_id}, whose
// head_sha is the commit the run ran on (https://docs.github.com/en/rest/actions/workflow-runs#get-a-workflow-run). The
// address of a check names a run, and anyone who can create a check can write any address, so a run is acted on only
// when the forge itself says it ran on the head under review. A run the forge does not hold, or answers a 4xx for, is
// no run of the head.
func (f Forge) runOfHead(ctx context.Context, repo ports.ForgeRepo, run, head string) error {
	if !commitID.MatchString(head) || !digitsOnly(run) {
		return fmt.Errorf("%w: %q is no run of %q", ports.ErrRunNotOfHead, run, head)
	}
	b := repo.Binding
	response, err := f.api(ctx, repo.Dir, b.Host, fmt.Sprintf("repos/%s/%s/actions/runs/%s", b.Owner, b.Repo, run))
	var refused *apiError
	if errors.As(err, &refused) && refused.refused() {
		return fmt.Errorf("%w: run %s: HTTP %d", ports.ErrRunNotOfHead, run, refused.Status)
	}
	if err != nil {
		return err
	}
	var got struct {
		HeadSHA string `json:"head_sha"`
	}
	if err := json.Unmarshal(response.Body, &got); err != nil {
		return fmt.Errorf("reading the workflow run %s: %w", run, err)
	}
	if !strings.EqualFold(got.HeadSHA, head) {
		return fmt.Errorf("%w: run %s ran on %s", ports.ErrRunNotOfHead, run, shortCommit(got.HeadSHA))
	}
	return nil
}

func shortCommit(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}

// workflowRuns is the answer of GET /repos/{owner}/{repo}/actions/runs.
type workflowRuns struct {
	Runs []struct {
		ID         int64  `json:"id"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	} `json:"workflow_runs"`
}

// failedRuns lists the runs of a head that finished without passing, which are the ones whose failed jobs a rerun
// starts again (https://docs.github.com/en/rest/actions/workflow-runs#list-workflow-runs-for-a-repository).
func (f Forge) failedRuns(ctx context.Context, repo ports.ForgeRepo, head string) ([]string, error) {
	b := repo.Binding
	response, err := f.api(ctx, repo.Dir, b.Host, fmt.Sprintf("repos/%s/%s/actions/runs?head_sha=%s&per_page=100", b.Owner, b.Repo, head))
	if err != nil {
		return nil, err
	}
	var listed workflowRuns
	if err := json.Unmarshal(response.Body, &listed); err != nil {
		return nil, fmt.Errorf("reading the workflow runs: %w", err)
	}
	var runs []string
	for _, run := range listed.Runs {
		if run.Status != "completed" {
			continue
		}
		switch ledger.ClassifyConclusion(run.Conclusion) {
		case ledger.CIFailure, ledger.CIInfrastructure:
			runs = append(runs, fmt.Sprint(run.ID))
		}
	}
	return runs, nil
}

// RerunChecks starts the failed jobs of runs again with POST /repos/{owner}/{repo}/actions/runs/{run_id}/rerun-failed-jobs
// (https://docs.github.com/en/rest/actions/workflow-runs#re-run-failed-jobs-from-a-workflow-run). With no runs named it
// reruns each run of the head that finished without passing. The forge takes the rerun of a run that finished, and
// refuses one that is still going, which is also what a rerun that already went finds: a refusal is a result, so a
// repeat after a crash starts nothing twice.
func (f Forge) RerunChecks(ctx context.Context, ref ports.PullRequestRef, head string, runs []string) (ledger.CIRerun, error) {
	named := len(runs) != 0
	if !named {
		if !commitID.MatchString(head) {
			return ledger.CIRerun{Refused: "the head names no commit"}, nil
		}
		found, err := f.failedRuns(ctx, ref.Repo, head)
		if err != nil {
			return ledger.CIRerun{}, err
		}
		if len(found) == 0 {
			return ledger.CIRerun{Refused: "no run of the head finished without passing"}, nil
		}
		runs = found
	}
	b := ref.Repo.Binding
	var result ledger.CIRerun
	var refusals []string
	for i, run := range runs {
		if !digitsOnly(run) {
			return ledger.CIRerun{}, fmt.Errorf("%q names no workflow run", run)
		}
		// A run the head's own listing gave is the head's. A run an address named is checked before it is touched.
		if named {
			if err := f.runOfHead(ctx, ref.Repo, run, head); errors.Is(err, ports.ErrRunNotOfHead) {
				refusals = append(refusals, fmt.Sprintf("run %s is not a run of the head: %s", run, clip(strings.TrimPrefix(err.Error(), ports.ErrRunNotOfHead.Error()+": "))))
				continue
			} else if err != nil {
				return stopped(result, refusals, runs[i:], err)
			}
		}
		_, err := f.api(ctx, ref.Repo.Dir, b.Host, "--method", "POST", fmt.Sprintf("repos/%s/%s/actions/runs/%s/rerun-failed-jobs", b.Owner, b.Repo, run))
		var refused *apiError
		switch {
		case err == nil:
			result.Runs = append(result.Runs, run)
		case errors.As(err, &refused) && refused.refused():
			refusals = append(refusals, fmt.Sprintf("run %s: HTTP %d: %s", run, refused.Status, refused.Message))
		default:
			return stopped(result, refusals, runs[i:], err)
		}
	}
	result.Refused = clip(strings.Join(refusals, "; "))
	return result, nil
}

// stopped is what a rerun came to when the forge failed in the middle of it: the runs it started again, the refusals so
// far, and the runs it did not reach, with the error. A caller that started none asks for all of it again, and one that
// started some asks for the rest only.
func stopped(done ledger.CIRerun, refusals, rest []string, err error) (ledger.CIRerun, error) {
	done.Refused = clip(strings.Join(refusals, "; "))
	done.Pending = slices.Clone(rest)
	return done, err
}

func digitsOnly(text string) bool {
	return text != "" && strings.Trim(text, "0123456789") == ""
}

// refPath writes a branch name as the path of a ref in an API address: its own slashes stay slashes, because the API
// reads a branch such as release/1.2 as a path, and every segment is escaped, so nothing of it can start a query, a
// fragment, or another endpoint. A segment that is empty, or climbs, is no branch.
func refPath(branch string) (string, bool) {
	segments := strings.Split(branch, "/")
	for i, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", false
		}
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/"), true
}

// BaseChecks reads GET /repos/{owner}/{repo}/commits/{branch}/check-runs, the check runs of the branch's tip
// (https://docs.github.com/en/rest/checks/runs#list-check-runs-for-a-git-reference), and names those that concluded
// `failure`. It reads check runs only, so a commit status that failed on the base is not seen.
func (f Forge) BaseChecks(ctx context.Context, repo ports.ForgeRepo, branch string) (ports.BaseChecksRead, error) {
	b := repo.Binding
	path, ok := refPath(branch)
	if !ok {
		return ports.BaseChecksRead{}, fmt.Errorf("%q is no branch to read the checks of", branch)
	}
	response, err := f.api(ctx, repo.Dir, b.Host, fmt.Sprintf("repos/%s/%s/commits/%s/check-runs?per_page=100", b.Owner, b.Repo, path))
	if err != nil {
		return ports.BaseChecksRead{}, err
	}
	var listed struct {
		Runs []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HeadSHA    string `json:"head_sha"`
		} `json:"check_runs"`
	}
	if err := json.Unmarshal(response.Body, &listed); err != nil {
		return ports.BaseChecksRead{}, fmt.Errorf("reading the check runs of %s: %w", branch, err)
	}
	read := ports.BaseChecksRead{Rate: rateOf(response.Header, false)}
	for _, run := range listed.Runs {
		if read.Commit == "" {
			read.Commit = run.HeadSHA
		}
		if run.Status == "completed" && run.Conclusion == "failure" {
			read.Failed = append(read.Failed, run.Name)
		}
	}
	return read, nil
}
