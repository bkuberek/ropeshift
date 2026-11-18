package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/test/doubles/ghstate"
)

// multi is a flag that may repeat, as gh api's -H and -f do.
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// api answers `gh api` for the REST reads the forge adapter makes with ETags, and for the one GraphQL query it
// makes over several pull requests. With -i it prints the status line and the headers before the body, and a
// status past 299 ends with `gh: HTTP <status>` on stderr and exit 1, which is what gh does.
func api(dir string, args []string) {
	var include bool
	var headers, fields multi
	var hostname, method string
	_, positional := parse("api", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&include, "i", false, "")
		fs.BoolVar(&include, "include", false, "")
		fs.Var(&headers, "H", "")
		fs.Var(&fields, "f", "")
		fs.Var(&fields, "F", "")
		fs.StringVar(&hostname, "hostname", "", "")
		fs.StringVar(&method, "method", "GET", "")
		fs.StringVar(&method, "X", "GET", "")
	})
	if len(positional) != 1 {
		fail(2, "fakegh: api names one endpoint")
	}
	state := readRaw(dir)
	reply := &response{include: include, state: state, dir: dir}
	if limit := state.RateLimit; limit != nil {
		reply.limited(limit)
		return
	}
	endpoint, query, _ := strings.Cut(positional[0], "?")
	switch {
	case endpoint == "graphql":
		reply.graphql(hostname, fields)
	case strings.HasPrefix(endpoint, "repos/"):
		reply.rest(hostname, method, endpoint, query, headers)
	default:
		fail(2, "fakegh: api endpoint %q", positional[0])
	}
}

// response is one reply of the API: its status, its headers, and its body.
type response struct {
	include bool
	state   ghstate.State
	dir     string
}

func (r *response) rateHeaders(out map[string]string) {
	remaining, reset := r.state.Remaining, r.state.Reset
	if remaining == 0 && reset == 0 {
		remaining = 4999
	}
	out["X-Ratelimit-Remaining"] = strconv.FormatInt(remaining, 10)
	if reset != 0 {
		out["X-Ratelimit-Reset"] = strconv.FormatInt(reset, 10)
	}
	if r.state.PollInterval > 0 {
		out["X-Poll-Interval"] = strconv.FormatInt(r.state.PollInterval, 10)
	}
}

// send writes a reply and ends the process: a body for a 2xx status, and the failure gh prints for any other.
func (r *response) send(status int, headers map[string]string, body any) {
	if r.include {
		fmt.Printf("HTTP/2.0 %d %s\r\n", status, statusText(status))
		for name, value := range headers {
			fmt.Printf("%s: %s\r\n", name, value)
		}
		fmt.Print("\r\n")
	}
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			fail(2, "fakegh: %v", err)
		}
		fmt.Println(string(raw))
	}
	if status > 299 {
		fail(1, "gh: HTTP %d", status)
	}
	os.Exit(0)
}

func statusText(status int) string {
	switch status {
	case 200:
		return "OK"
	case 201:
		return "Created"
	case 304:
		return "Not Modified"
	case 403:
		return "Forbidden"
	case 404:
		return "Not Found"
	}
	return "Status"
}

// limited answers as GitHub does over a limit: a primary limit with no request left until a reset, or a secondary
// limit that asks for a wait.
func (r *response) limited(limit *ghstate.RateLimit) {
	headers := map[string]string{}
	message := "API rate limit exceeded for user ID 1."
	if limit.Secondary {
		message = "You have exceeded a secondary rate limit. Please wait a few minutes before you try again."
	}
	if limit.RetryAfter > 0 {
		headers["Retry-After"] = strconv.FormatInt(limit.RetryAfter, 10)
	}
	if limit.Exhausted {
		headers["X-Ratelimit-Remaining"] = "0"
		headers["X-Ratelimit-Reset"] = strconv.FormatInt(limit.Reset, 10)
	}
	r.send(403, headers, map[string]string{"message": message})
}

// validator makes a response's ETag from its body, and answers 304 when the request carried it.
func (r *response) validated(request map[string]string, body any) {
	raw, err := json.Marshal(body)
	if err != nil {
		fail(2, "fakegh: %v", err)
	}
	sum := sha256.Sum256(raw)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	headers := map[string]string{"Etag": etag}
	r.rateHeaders(headers)
	if request["If-None-Match"] == etag {
		r.send(304, headers, nil)
	}
	r.send(200, headers, body)
}

// rest answers a pull request, and the check runs and the commit statuses of a commit, each with an ETag.
func (r *response) rest(hostname, method, endpoint, query string, headers multi) {
	request := map[string]string{}
	for _, header := range headers {
		name, value, _ := strings.Cut(header, ":")
		request[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	parts := strings.Split(endpoint, "/")
	// repos/{owner}/{repo}/pulls/{number}, repos/{owner}/{repo}/commits/{sha}/{check-runs|status}, and the workflow runs
	if len(parts) < 5 {
		fail(2, "fakegh: api endpoint %q", endpoint)
	}
	repo := hostname + "/" + parts[1] + "/" + parts[2]
	if parts[3] == "actions" {
		r.actions(repo, method, parts, query)
		return
	}
	if len(parts) == 6 && parts[3] == "commits" && parts[5] == "check-runs" {
		if branch, err := url.PathUnescape(parts[4]); err == nil {
			if runs, ok := baseRuns(r.state, repo, branch); ok {
				r.validated(request, runs)
				return
			}
		}
	}
	switch {
	case parts[3] == "pulls" && len(parts) == 5:
		number, err := strconv.ParseInt(parts[4], 10, 64)
		if err != nil {
			fail(2, "fakegh: %q is not a pull request number", parts[4])
		}
		pr := r.state.Find(number)
		if pr == nil || pr.Repo != repo {
			r.send(404, map[string]string{}, map[string]string{"message": "Not Found"})
		}
		r.validated(request, restPull(*pr, r.state))
	case parts[3] == "commits" && len(parts) == 6 && (parts[5] == "check-runs" || parts[5] == "status"):
		pr := onCommit(r.state, repo, parts[4])
		var checks []ghstate.Check
		if pr != nil {
			checks = pr.Checks
		}
		if parts[5] == "check-runs" {
			r.validated(request, restRuns(checks))
			return
		}
		r.validated(request, restStatuses(parts[4], checks))
	default:
		fail(2, "fakegh: api endpoint %q", endpoint)
	}
}

// onCommit finds the open pull request of a repository whose head is the commit, as the commits endpoints see it.
func onCommit(state ghstate.State, repo, sha string) *ghstate.PullRequest {
	for i := range state.PullRequests {
		pr := &state.PullRequests[i]
		if pr.Repo == repo && headOf(*pr, state) == sha {
			return pr
		}
	}
	return nil
}

func headOf(pr ghstate.PullRequest, state ghstate.State) string {
	if pr.State == "OPEN" {
		if live := remoteHead(state, pr.Branch); live != "" {
			return live
		}
	}
	return pr.Head
}

func restPull(pr ghstate.PullRequest, state ghstate.State) map[string]any {
	return map[string]any{
		"number": pr.Number, "state": strings.ToLower(pr.State), "merged": pr.State == "MERGED", "merge_commit_sha": pr.MergeCommit,
		"head": map[string]string{"sha": headOf(pr, state), "ref": pr.Branch}, "base": map[string]string{"ref": pr.Base},
		"mergeable_state": mergeState(pr, state),
	}
}

func restRuns(checks []ghstate.Check) map[string]any {
	runs := []map[string]any{}
	for _, check := range checks {
		if check.Typename == "StatusContext" || (check.Name == "" && check.Context != "") {
			continue
		}
		runs = append(runs, map[string]any{
			"name": check.Name, "status": strings.ToLower(check.Status), "conclusion": strings.ToLower(check.Conclusion),
			"details_url": check.DetailsURL, "started_at": check.StartedAt,
		})
	}
	return map[string]any{"total_count": len(runs), "check_runs": runs}
}

func restStatuses(sha string, checks []ghstate.Check) map[string]any {
	statuses := []map[string]any{}
	for _, check := range checks {
		if check.Typename == "StatusContext" || (check.Name == "" && check.Context != "") {
			statuses = append(statuses, map[string]any{"context": check.Context, "state": strings.ToLower(check.State), "target_url": check.TargetURL})
		}
	}
	return map[string]any{"sha": sha, "total_count": len(statuses), "statuses": statuses}
}

var pullRequestNumber = regexp.MustCompile(`pullRequest\(number:\s*(\d+)\)`)

// graphql answers the query the adapter makes over several pull requests, each under an alias p<number>, with the
// fields of `gh pr view` and the required mark on each check. A number the repository holds no pull request for is
// null, as GitHub answers it.
func (r *response) graphql(hostname string, fields multi) {
	values := map[string]string{}
	for _, field := range fields {
		name, value, _ := strings.Cut(field, "=")
		values[name] = value
	}
	repo := hostname + "/" + values["owner"] + "/" + values["name"]
	repository := map[string]any{}
	for _, match := range pullRequestNumber.FindAllStringSubmatch(values["query"], -1) {
		number, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			fail(2, "fakegh: %v", err)
		}
		alias := "p" + match[1]
		if _, done := repository[alias]; done {
			continue
		}
		pr := r.state.Find(number)
		if pr == nil || pr.Repo != repo {
			repository[alias] = nil
			continue
		}
		repository[alias] = graphPull(*pr, r.state)
	}
	headers := map[string]string{}
	r.rateHeaders(headers)
	r.send(200, headers, map[string]any{"data": map[string]any{"repository": repository}})
}

func graphPull(pr ghstate.PullRequest, state ghstate.State) map[string]any {
	out := render(pr, state, "number,url,state,baseRefName,headRefName,headRefOid,mergeable,mergedAt,mergeCommit")
	nodes := []map[string]any{}
	for _, check := range pr.Checks {
		node := map[string]any{"__typename": check.Typename}
		if check.Typename == "StatusContext" || (check.Name == "" && check.Context != "") {
			node["__typename"] = "StatusContext"
			node["context"], node["state"], node["targetUrl"] = check.Context, check.State, check.TargetURL
		} else {
			node["__typename"] = "CheckRun"
			node["name"], node["status"], node["conclusion"] = check.Name, check.Status, check.Conclusion
			node["detailsUrl"], node["startedAt"], node["completedAt"] = check.DetailsURL, check.StartedAt, check.CompletedAt
		}
		node["isRequired"] = contains(pr.Required, cmpOr(check.Name, check.Context))
		nodes = append(nodes, node)
	}
	out["commits"] = map[string]any{"nodes": []map[string]any{{"commit": map[string]any{
		"oid": out["headRefOid"],
		"statusCheckRollup": map[string]any{"contexts": map[string]any{
			"pageInfo": map[string]any{"hasNextPage": false}, "nodes": nodes,
		}},
	}}}}
	return out
}

func contains(list []string, name string) bool {
	for _, item := range list {
		if item == name {
			return true
		}
	}
	return false
}
