package main

import (
	"flag"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/test/doubles/ghstate"
)

// actions answers the workflow runs of a repository: the list of a head's runs, and the rerun of a run's failed jobs,
// which GitHub takes only for a run that finished (https://docs.github.com/en/rest/actions/workflow-runs).
func (r *response) actions(repo, method string, parts []string, query string) {
	headers := map[string]string{}
	r.rateHeaders(headers)
	switch {
	case method == "GET" && len(parts) == 5 && parts[4] == "runs":
		values, _ := url.ParseQuery(query)
		runs := []map[string]any{}
		for _, run := range r.state.Runs {
			if run.Repo == repo && run.Head == values.Get("head_sha") {
				runs = append(runs, map[string]any{"id": run.ID, "status": run.Status, "conclusion": run.Conclusion})
			}
		}
		r.send(200, headers, map[string]any{"total_count": len(runs), "workflow_runs": runs})
	case method == "GET" && len(parts) == 6 && parts[4] == "runs":
		id, err := strconv.ParseInt(parts[5], 10, 64)
		if err != nil {
			fail(2, "fakegh: %q is not a run", parts[5])
		}
		run := r.state.FindRun(id)
		if run == nil || run.Repo != repo {
			r.send(404, headers, map[string]string{"message": "Not Found"})
		}
		r.send(200, headers, map[string]any{"id": run.ID, "head_sha": run.Head, "status": run.Status, "conclusion": run.Conclusion})
	case method == "POST" && len(parts) == 7 && parts[4] == "runs" && parts[6] == "rerun-failed-jobs":
		id, err := strconv.ParseInt(parts[5], 10, 64)
		if err != nil {
			fail(2, "fakegh: %q is not a run", parts[5])
		}
		run := r.state.FindRun(id)
		if run == nil || run.Repo != repo {
			r.send(404, headers, map[string]string{"message": "Not Found"})
		}
		if r.state.FailRerun == id {
			if err := ghstate.Edit(r.dir, func(s *ghstate.State) error { s.FailRerun = 0; return nil }); err != nil {
				fail(2, "fakegh: %v", err)
			}
			r.send(500, headers, map[string]string{"message": "Server Error"})
		}
		if run.Status != "completed" {
			r.send(403, headers, map[string]string{"message": "Workflow is already running"})
		}
		err = ghstate.Edit(r.dir, func(s *ghstate.State) error {
			held := s.FindRun(id)
			held.Status, held.Conclusion = "queued", ""
			held.Reruns++
			return nil
		})
		if err != nil {
			fail(2, "fakegh: %v", err)
		}
		r.send(201, headers, nil)
	}
	fail(2, "fakegh: api actions endpoint %q", strings.Join(parts, "/"))
}

// baseRuns answers the check runs of a branch's tip, for a repository that holds some, each with the commit it ran on.
func baseRuns(state ghstate.State, repo, branch string) (map[string]any, bool) {
	runs := []map[string]any{}
	for _, check := range state.Bases {
		if check.Repo == repo && check.Branch == branch {
			runs = append(runs, map[string]any{
				"name": check.Name, "status": "completed", "conclusion": strings.ToLower(check.Conclusion), "head_sha": check.Commit,
			})
		}
	}
	return map[string]any{"total_count": len(runs), "check_runs": runs}, len(runs) > 0
}

// runView prints the log of what failed in a run, as `gh run view <id> --log-failed` does, or says no run is there.
func runView(dir string, args []string) {
	var repo, job string
	var logFailed bool
	_, positional := parse("run view", args, func(fs *flag.FlagSet) {
		fs.StringVar(&repo, "repo", "", "")
		fs.StringVar(&job, "job", "", "")
		fs.BoolVar(&logFailed, "log-failed", false, "")
	})
	if len(positional) != 1 || !logFailed {
		fail(2, "fakegh: run view names a run and asks for --log-failed")
	}
	id, err := strconv.ParseInt(positional[0], 10, 64)
	if err != nil {
		fail(2, "fakegh: %q is not a run", positional[0])
	}
	state := read(dir)
	run := state.FindRun(id)
	if run == nil || run.Repo != repo {
		fail(1, "could not find any workflow run with ID %d", id)
	}
	fmt.Print(run.Log)
}
