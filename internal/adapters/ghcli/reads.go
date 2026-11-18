package ghcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// The cheap reads of CI (FR-SPEND-20): several pull requests in one GraphQL query, and a probe of one pull request
// with conditional requests. Both go through `gh api --include`, which prints the response's status and headers
// before its body, so the rate limit's headers reach the worker (https://cli.github.com/manual/gh_api).

// apiResponse is one response of `gh api --include`.
type apiResponse struct {
	Status int
	// Header holds each header under its lower-cased name.
	Header map[string]string
	Body   []byte
}

// parseResponse reads the status line, the headers, and the body of what `gh api --include` printed.
func parseResponse(out []byte) (apiResponse, error) {
	text := strings.ReplaceAll(string(out), "\r\n", "\n")
	head, body, _ := strings.Cut(text, "\n\n")
	lines := strings.Split(head, "\n")
	fields := strings.Fields(lines[0])
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "HTTP/") {
		return apiResponse{}, fmt.Errorf("gh api printed no status line, but %q", clip(lines[0]))
	}
	status, err := strconv.Atoi(fields[1])
	if err != nil {
		return apiResponse{}, fmt.Errorf("gh api printed the status %q", fields[1])
	}
	response := apiResponse{Status: status, Header: map[string]string{}, Body: []byte(body)}
	for _, line := range lines[1:] {
		if name, value, ok := strings.Cut(line, ":"); ok {
			response.Header[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(value)
		}
	}
	return response, nil
}

// rateOf reads what a response's headers say of the rate limits: `retry-after` of a secondary limit,
// `x-ratelimit-remaining` and `x-ratelimit-reset` of the primary one, and the least time `x-poll-interval` asks
// between two reads (https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api,
// https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api).
func rateOf(header map[string]string, secondary bool) ports.ForgeRate {
	rate := ports.ForgeRate{Secondary: secondary}
	if seconds, err := strconv.ParseInt(header["retry-after"], 10, 64); err == nil && seconds > 0 {
		rate.RetryAfter, rate.Secondary = time.Duration(seconds)*time.Second, true
	}
	if remaining, err := strconv.ParseInt(header["x-ratelimit-remaining"], 10, 64); err == nil && remaining <= 0 {
		rate.Exhausted = true
	}
	if reset, err := strconv.ParseInt(header["x-ratelimit-reset"], 10, 64); err == nil && reset > 0 {
		rate.Reset = time.Unix(reset, 0)
	}
	if seconds, err := strconv.ParseInt(header["x-poll-interval"], 10, 64); err == nil && seconds > 0 {
		rate.PollInterval = time.Duration(seconds) * time.Second
	}
	return rate
}

// api runs `gh api --include` for one endpoint and sorts what came back. A 304 is an answer. A 429, or a 403 that
// carries `retry-after`, an exhausted window, or a message naming a rate limit, is a RateLimitedError with the wait
// the headers give. Any other status past 299 is an error.
func (f Forge) api(ctx context.Context, dir, host string, args ...string) (apiResponse, error) {
	out, err := f.runKeeping(ctx, dir, append([]string{"api", "--include", "--hostname", host}, args...)...)
	response, parseErr := parseResponse(out)
	if parseErr != nil {
		if err != nil {
			return apiResponse{}, err
		}
		return apiResponse{}, parseErr
	}
	switch {
	case response.Status == 304 || (response.Status >= 200 && response.Status <= 299 && err == nil):
		return response, nil
	case response.Status == 429 || response.Status == 403:
		message := apiMessage(response.Body)
		rate := rateOf(response.Header, strings.Contains(strings.ToLower(message), "secondary"))
		if response.Status == 429 || rate.RetryAfter > 0 || rate.Exhausted || strings.Contains(strings.ToLower(message), "rate limit") {
			return apiResponse{}, &ports.RateLimitedError{Rate: rate, Detail: clip(message)}
		}
	}
	return apiResponse{}, &apiError{args: args, Status: response.Status, Message: clip(apiMessage(response.Body))}
}

// apiMessage reads the message of an error body.
func apiMessage(body []byte) string {
	var failure struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &failure) == nil && failure.Message != "" {
		return failure.Message
	}
	return strings.TrimSpace(string(body))
}

// probeTag is what a probe remembers of the three responses it compared: the ETag of the pull request, of the
// check runs of its head, and of the commit statuses of its head.
type probeTag struct {
	Pull   string `json:"p,omitempty"`
	Runs   string `json:"r,omitempty"`
	Status string `json:"s,omitempty"`
}

var commitID = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

// ProbeChecks asks GET /repos/{owner}/{repo}/pulls/{number}, and GET /repos/{owner}/{repo}/commits/{head}/check-runs
// and /status, each with the ETag its last response gave in If-None-Match. A 304 does not count against the
// primary rate limit for an authorized request
// (https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api#use-conditional-requests).
// The pull request's response changes with a push, a merge, and a close, the check runs' with every run, and the
// statuses' with every commit status, so all three 304 means a read would find what the last one did.
func (f Forge) ProbeChecks(ctx context.Context, ref ports.PullRequestRef, head, tag string) (ports.ChecksProbe, error) {
	if !commitID.MatchString(head) {
		return ports.ChecksProbe{}, nil
	}
	var old probeTag
	if tag != "" && json.Unmarshal([]byte(tag), &old) != nil {
		old = probeTag{}
	}
	b := ref.Repo.Binding
	root := fmt.Sprintf("repos/%s/%s", b.Owner, b.Repo)
	endpoints := []struct {
		path string
		old  string
	}{
		{fmt.Sprintf("%s/pulls/%d", root, ref.Number), old.Pull},
		{fmt.Sprintf("%s/commits/%s/check-runs?per_page=100", root, head), old.Runs},
		{fmt.Sprintf("%s/commits/%s/status?per_page=100", root, head), old.Status},
	}
	var next probeTag
	tags := []*string{&next.Pull, &next.Runs, &next.Status}
	probe := ports.ChecksProbe{Unchanged: true}
	for i, endpoint := range endpoints {
		args := []string{endpoint.path}
		if endpoint.old != "" {
			args = []string{"-H", "If-None-Match: " + endpoint.old, endpoint.path}
		}
		response, err := f.api(ctx, ref.Repo.Dir, b.Host, args...)
		if err != nil {
			return ports.ChecksProbe{}, err
		}
		probe.Rate = laterRate(probe.Rate, rateOf(response.Header, false))
		if response.Status == 304 && endpoint.old != "" {
			*tags[i] = endpoint.old
			continue
		}
		probe.Unchanged, *tags[i] = false, response.Header["etag"]
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return ports.ChecksProbe{}, err
	}
	probe.Tag = string(raw)
	return probe, nil
}

// laterRate keeps the stricter of two responses' rates: the wait that is longer, and an exhausted window.
func laterRate(a, b ports.ForgeRate) ports.ForgeRate {
	out := a
	out.Exhausted = a.Exhausted || b.Exhausted
	out.Secondary = a.Secondary || b.Secondary
	out.RetryAfter = max(a.RetryAfter, b.RetryAfter)
	out.PollInterval = max(a.PollInterval, b.PollInterval)
	if b.Reset.After(a.Reset) {
		out.Reset = b.Reset
	}
	return out
}

// batchSelection is what the query reads of each pull request: the fields `gh pr view` gives, the head commit's
// check runs and commit statuses, and whether the base branch's protection requires each of them, which is what
// `gh pr checks --required` is asked for on its own. %d is the pull request's number.
const batchSelection = `number url state baseRefName headRefName headRefOid mergeable mergedAt mergeCommit { oid }
commits(last: 1) { nodes { commit { oid statusCheckRollup { contexts(first: 100) { pageInfo { hasNextPage } nodes {
__typename
... on CheckRun { name status conclusion detailsUrl startedAt completedAt isRequired(pullRequestNumber: %[1]d) }
... on StatusContext { context state targetUrl isRequired(pullRequestNumber: %[1]d) }
} } } } } }`

// batchQuery is one query over several pull requests of a repository, each under the alias p<number>
// (https://docs.github.com/en/graphql/reference/objects#pullrequest).
func batchQuery(numbers []int64) string {
	var query strings.Builder
	query.WriteString("query($owner: String!, $name: String!) { repository(owner: $owner, name: $name) {\n")
	for _, number := range numbers {
		fmt.Fprintf(&query, "p%[1]d: pullRequest(number: %[1]d) { "+batchSelection+" }\n", number)
	}
	query.WriteString("} }")
	return query.String()
}

// batched is a pull request as the batched query answers it.
type batched struct {
	viewed
	Commits struct {
		Nodes []struct {
			Commit struct {
				Oid               string `json:"oid"`
				StatusCheckRollup *struct {
					Contexts struct {
						PageInfo struct {
							HasNextPage bool `json:"hasNextPage"`
						} `json:"pageInfo"`
						Nodes []struct {
							rollup
							IsRequired bool `json:"isRequired"`
						} `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

// pullRequest is the pull request with its checks, and false for one the query did not answer completely: a head
// the last commit is not, which a push in the moment between two reads makes, and a list of checks longer than the
// query asked for.
func (b batched) pullRequest() (ledger.PullRequest, bool) {
	if len(b.Commits.Nodes) != 1 {
		return ledger.PullRequest{}, false
	}
	commit := b.Commits.Nodes[0].Commit
	if stateOf(b.State) != ledger.PullRequestOpen {
		return b.viewed.pullRequest(), true
	}
	if commit.Oid != b.HeadRefOid {
		return ledger.PullRequest{}, false
	}
	required := map[string]bool{}
	if commit.StatusCheckRollup != nil {
		contexts := commit.StatusCheckRollup.Contexts
		if contexts.PageInfo.HasNextPage {
			return ledger.PullRequest{}, false
		}
		for _, node := range contexts.Nodes {
			b.StatusCheckRollup = append(b.StatusCheckRollup, node.rollup)
			check := node.check()
			required[check.Name] = node.IsRequired
		}
	}
	pr := b.viewed.pullRequest()
	for i := range pr.Checks {
		pr.Checks[i].Required = required[pr.Checks[i].Name]
	}
	return pr, true
}

// ReadPullRequests reads every pull request named in one `gh api graphql` query of the repository, which counts
// as points of the GraphQL rate limit, the nodes the query could reach divided by 100 and at least 1
// (https://docs.github.com/en/graphql/overview/rate-limits-and-node-limits-for-the-graphql-api). GitHub documents
// no conditional request for GraphQL **(unverified)**, so the single-head probe is the conditional read.
func (f Forge) ReadPullRequests(ctx context.Context, repo ports.ForgeRepo, numbers []int64) (ports.PullRequestsRead, error) {
	read := ports.PullRequestsRead{Pulls: map[int64]ledger.PullRequest{}}
	numbers = slices.DeleteFunc(slices.Clone(numbers), func(n int64) bool { return n <= 0 })
	slices.Sort(numbers)
	numbers = slices.Compact(numbers)
	if len(numbers) == 0 {
		return read, nil
	}
	b := repo.Binding
	response, err := f.api(ctx, repo.Dir, b.Host, "graphql", "-f", "query="+batchQuery(numbers), "-f", "owner="+b.Owner, "-f", "name="+b.Repo)
	if err != nil {
		return ports.PullRequestsRead{}, err
	}
	read.Rate = rateOf(response.Header, false)
	var body struct {
		Data struct {
			Repository map[string]*batched `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(response.Body, &body); err != nil {
		return ports.PullRequestsRead{}, fmt.Errorf("reading gh api graphql: %w", err)
	}
	var failures []string
	for _, problem := range body.Errors {
		switch problem.Type {
		case "RATE_LIMITED":
			return ports.PullRequestsRead{}, &ports.RateLimitedError{Rate: read.Rate, Detail: clip(problem.Message)}
		case "NOT_FOUND":
			// A pull request the repository does not hold is null in the answer, and left out of this one.
		default:
			failures = append(failures, problem.Message)
		}
	}
	if len(body.Data.Repository) == 0 {
		return ports.PullRequestsRead{}, errors.New("gh api graphql answered with no repository: " + clip(strings.Join(failures, "; ")))
	}
	for _, number := range numbers {
		if answered := body.Data.Repository["p"+strconv.FormatInt(number, 10)]; answered != nil {
			if pr, whole := answered.pullRequest(); whole && pr.Number == number {
				read.Pulls[number] = pr
			}
		}
	}
	return read, nil
}
