package ghcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
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

// runTail runs one command in a checkout and returns the last max bytes of its stdout. A command the forge refused for
// a rate limit is a RateLimitedError.
func (f Forge) runTail(ctx context.Context, dir string, max int, args ...string) (string, error) {
	binary, err := f.binary()
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir, cmd.Env = dir, f.environment()
	tail, stderr := &tailBuffer{max: max}, &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = tail, stderr
	err = cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		failed := &ghError{args: args, code: exit.ExitCode(), stderr: clip(stderr.String())}
		if limited, ok := rateLimitedText(failed.stderr); ok {
			return "", limited
		}
		return "", failed
	}
	if err != nil {
		return "", fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}
	return tail.text(), nil
}

// FailedLog reads what failed in a check's run with `gh run view <run> --log-failed`, narrowed to the check's job
// when its address names one (https://cli.github.com/manual/gh_run_view). Only the last maxBytes of it are kept. A
// check whose address is no page of this repository's GitHub Actions has no log to read, and answers none.
func (f Forge) FailedLog(ctx context.Context, repo ports.ForgeRepo, check ledger.CICheckRef, maxBytes int) (string, error) {
	run, job, ok := ledger.ActionsRun(check.URL, repo.Binding)
	if !ok {
		return "", nil
	}
	args := []string{"run", "view", run, "--repo", repo.Binding.String(), "--log-failed"}
	if job != "" {
		args = append(args, "--job", job)
	}
	return f.runTail(ctx, repo.Dir, maxBytes, args...)
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
	if len(runs) == 0 {
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
	for _, run := range runs {
		if !digitsOnly(run) {
			return ledger.CIRerun{}, fmt.Errorf("%q names no workflow run", run)
		}
		_, err := f.api(ctx, ref.Repo.Dir, b.Host, "--method", "POST", fmt.Sprintf("repos/%s/%s/actions/runs/%s/rerun-failed-jobs", b.Owner, b.Repo, run))
		var refused *apiError
		switch {
		case err == nil:
			result.Runs = append(result.Runs, run)
		case errors.As(err, &refused) && refused.refused():
			refusals = append(refusals, fmt.Sprintf("run %s: HTTP %d: %s", run, refused.Status, refused.Message))
		default:
			return ledger.CIRerun{}, err
		}
	}
	result.Refused = clip(strings.Join(refusals, "; "))
	return result, nil
}

func digitsOnly(text string) bool {
	return text != "" && strings.Trim(text, "0123456789") == ""
}

// BaseChecks reads GET /repos/{owner}/{repo}/commits/{branch}/check-runs, the check runs of the branch's tip
// (https://docs.github.com/en/rest/checks/runs#list-check-runs-for-a-git-reference), and names those that concluded
// `failure`. It reads check runs only, so a commit status that failed on the base is not seen.
func (f Forge) BaseChecks(ctx context.Context, repo ports.ForgeRepo, branch string) (ports.BaseChecksRead, error) {
	b := repo.Binding
	response, err := f.api(ctx, repo.Dir, b.Host, fmt.Sprintf("repos/%s/%s/commits/%s/check-runs?per_page=100", b.Owner, b.Repo, url.PathEscape(branch)))
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
