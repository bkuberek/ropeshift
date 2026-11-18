package ledger

import (
	"strconv"
	"strings"
)

// CICheckClass is what the result of a required check means for what happens next (ADR-0037). The class decides
// whether an agent spends at all, so no setting changes it: only a test failure ever starts a lane.
type CICheckClass string

// The classes of a required check.
const (
	// CIClassPending is a check that has not finished.
	CIClassPending CICheckClass = "pending"
	// CIClassPass is a check that passed, as the forge counts it for a required check.
	CIClassPass CICheckClass = "pass"
	// CIClassTestFailure is a failure of the code, which sends the work back with the failing job's log.
	CIClassTestFailure CICheckClass = "test_failure"
	// CIClassBaseRed is a failure the base branch's tip shares, which no change of this work can mend.
	CIClassBaseRed CICheckClass = "base_red"
	// CIClassInfrastructure is a fault of the runner or the forge, which is rerun a bounded number of times.
	CIClassInfrastructure CICheckClass = "infrastructure"
	// CIClassActionRequired is a check that waits for a person on the forge.
	CIClassActionRequired CICheckClass = "action_required"
)

// Class classifies a check on a head by the map of ClassifyConclusion, which is ADR-0037's: `failure` is a test
// failure, `cancelled`, `timed_out`, `startup_failure`, and `stale` are infrastructure, and `action_required` waits for
// a person. A conclusion the design does not name is infrastructure, the class that starts no agent and is bounded by
// `ci_auto_rerun` before a person is asked. A commit status has no conclusion, and its failure is a test failure, as it
// always was. A `failure` that the base branch's tip shares is a red base.
func (c PullRequestCheck) Class() CICheckClass {
	switch c.State {
	case CheckPending:
		return CIClassPending
	case CheckPassed:
		return CIClassPass
	}
	switch ClassifyConclusion(strings.ToLower(strings.TrimSpace(c.Conclusion))) {
	case CIFailure:
		if c.RedOnBase != "" {
			return CIClassBaseRed
		}
		return CIClassTestFailure
	case CIActionRequired:
		return CIClassActionRequired
	case CIPass:
		// A check that failed with a conclusion that reads as a pass says something the design did not expect, which
		// a person reads.
		return CIClassActionRequired
	}
	return CIClassInfrastructure
}

// CompletedAfter says a check finished after a time in database milliseconds, which tells a failure that a rerun has
// not yet replaced from one a rerun is for. With no time, because none was asked for, every finish counts. A finish the
// forge gave in a form this cannot read is never after a rerun, so it is never rerun a second time.
func (c PullRequestCheck) CompletedAfter(at int64) bool {
	if at == 0 {
		return true
	}
	done, ok := millis(c.CompletedAt)
	return ok && done > at
}

// CICheckRef names one check a forge request is about, with the address the forge gave its run.
type CICheckRef struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// ActionsRun reads the run and the job a check's address names, when it is a page of the binding's own GitHub
// Actions: https://HOST/OWNER/REPO/actions/runs/RUN[/job/JOB]. Anything else, a check of another CI among them,
// names no run a rerun or a log read can reach.
func ActionsRun(address string, binding ForgeBinding) (run, job string, ok bool) {
	rest, secure := strings.CutPrefix(address, "https://")
	if !secure {
		return "", "", false
	}
	host, path, _ := strings.Cut(rest, "/")
	if !strings.EqualFold(host, binding.Host) {
		return "", "", false
	}
	path, _, _ = strings.Cut(path, "#")
	path, _, _ = strings.Cut(path, "?")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 5 && len(parts) != 7 {
		return "", "", false
	}
	if !strings.EqualFold(parts[0], binding.Owner) || !strings.EqualFold(parts[1], binding.Repo) || parts[2] != "actions" || parts[3] != "runs" || !digits(parts[4]) {
		return "", "", false
	}
	if len(parts) == 7 {
		if parts[5] != "job" || !digits(parts[6]) {
			return "", "", false
		}
		return parts[4], parts[6], true
	}
	return parts[4], "", true
}

func digits(text string) bool {
	if text == "" || len(text) > 20 {
		return false
	}
	_, err := strconv.ParseUint(text, 10, 64)
	return err == nil
}

// CIRerun is what the forge did with a rerun: the runs whose failed jobs it started again, and why it refused when
// it refused. A refusal is a result and not an error, because asking again would be refused again.
type CIRerun struct {
	Runs    []string `json:"runs,omitempty"`
	Refused string   `json:"refused,omitempty"`
}

// CICheckLog is the log of one failed check as a worker read it. A worker bounds and scrubs it before it reports it,
// and the control plane does both again by the repository's own keys before it keeps it, so a log is never trusted for
// being on the ledger's side of a boundary.
type CICheckLog struct {
	Check string `json:"check"`
	URL   string `json:"url,omitempty"`
	Text  string `json:"text,omitempty"`
	// Note says why Text is empty, or that it is the end of a longer log.
	Note string `json:"note,omitempty"`
}
