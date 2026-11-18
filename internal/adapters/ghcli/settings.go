package ghcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
)

var _ ports.ForgeSettings = Forge{}

// Settings reads what the forge says of a repository and its base branch for the practices check, with `gh api`
// under the person's sign-in:
//
//   - repos/{owner}/{repo} for the visibility;
//   - repos/{owner}/{repo}/branches/{branch} for `protected`;
//   - repos/{owner}/{repo}/rules/branches/{branch} for the rules that apply to the branch, where any rule is
//     protection, and a `pull_request` rule with `required_approving_review_count` of at least 1 requires a review
//     (https://docs.github.com/en/rest/repos/rules#get-rules-for-a-branch). It asks for 100 rules a page and follows
//     the pages the `Link` header names, up to maxRulePages, until a page holds a review rule;
//   - when the branch has branch protection and no rule requires a review,
//     repos/{owner}/{repo}/branches/{branch}/protection/required_pull_request_reviews for
//     `required_approving_review_count`, which a 404 whose message holds "not enabled" gives as 0
//     (https://docs.github.com/en/rest/branches/branch-protection).
//
// A read the sign-in is refused, that is rate limited, that the forge answers with a status that settles nothing, or
// that `gh` cannot make leaves the fact it decides unknown with the forge's message, clipped as every message of the
// adapter is, and is no error. A count of 0 with no unknown reason means the forge settled that no review is
// required, so a branch with more rules than the pages read, and no review rule among them, leaves the review
// unknown. A rate limit also stops the reads that remain, leaves their facts unknown, and returns the facts with an
// error that wraps the *ports.RateLimitedError. A canceled context returns its error and no facts.
func (f Forge) Settings(ctx context.Context, repo ports.ForgeRepo, branch string) (practices.ForgeFacts, error) {
	b := repo.Binding
	if !safeName(b.Owner) || !safeName(b.Repo) {
		reason := fmt.Sprintf("the repository %q is no repository name", clip(b.Owner+"/"+b.Repo))
		return practices.ForgeFacts{VisibilityUnknown: reason, ProtectedUnknown: reason, ReviewUnknown: reason}, nil
	}
	root := fmt.Sprintf("repos/%s/%s", url.PathEscape(b.Owner), url.PathEscape(b.Repo))
	var limited *ports.RateLimitedError
	var limitedReason string
	// get returns the response of a read, or the reason it settles nothing.
	get := func(path string) (apiResponse, error) {
		if err := ctx.Err(); err != nil {
			return apiResponse{}, err
		}
		if limited != nil {
			return apiResponse{}, &settingsError{reason: limitedReason}
		}
		response, err := f.api(ctx, repo.Dir, b.Host, path)
		var rate *ports.RateLimitedError
		if errors.As(err, &rate) {
			limited, limitedReason = rate, rateReason(rate.Rate, time.Now())
			return apiResponse{}, &settingsError{reason: limitedReason}
		}
		return response, err
	}

	var facts practices.ForgeFacts
	if response, err := get(root); err != nil {
		facts.VisibilityUnknown = reasonOf(err)
	} else if visibility, err := visibilityOf(response.Body); err != nil {
		facts.VisibilityUnknown = reasonOf(err)
	} else {
		facts.Visibility = visibility
	}

	if refs, ok := refPath(branch); ok {
		branchFacts(get, root, refs, &facts)
	} else {
		reason := fmt.Sprintf("the base branch %q is no branch name", clip(branch))
		facts.ProtectedUnknown, facts.ReviewUnknown = reason, reason
	}
	if err := ctx.Err(); err != nil {
		return practices.ForgeFacts{}, err
	}
	if limited != nil {
		return facts, fmt.Errorf("reading the forge settings: %w", limited)
	}
	return facts, nil
}

// branchFacts decides Protected and ReviewCount from the branch, rules, and protection reads of a branch.
func branchFacts(get func(path string) (apiResponse, error), root, ref string, facts *practices.ForgeFacts) {
	branch := readBranch(get(root + "/branches/" + ref))
	rules := readRules(get, root+"/rules/branches/"+ref)
	protected := branch.protected || len(rules.rules) > 0
	unread := firstReason(branch.reason, rules.reason)
	switch {
	case protected:
		facts.Protected = true
	case unread != "":
		facts.ProtectedUnknown = unread
	}
	switch {
	case rules.reviews >= 1:
		facts.ReviewCount = rules.reviews
	case !protected && unread != "":
		facts.ReviewUnknown = unread
	case !protected:
		// Neither read says protected, and both were read: the forge settled that no review is required.
	case !branch.protected && branch.reason == "":
		// Ruleset protection only, so no branch protection to read: the rules decide, and may be unread.
		facts.ReviewUnknown = rules.reason
	default:
		count, reason := readReviews(get(root + "/branches/" + ref + "/protection/required_pull_request_reviews"))
		switch {
		case reason != "":
			facts.ReviewUnknown = reason
		case count >= 1:
			facts.ReviewCount = count
		default:
			// With the rules unread, a ruleset may still require a review.
			facts.ReviewUnknown = rules.reason
		}
	}
}

// settingsError is a read that settled nothing, with the reason.
type settingsError struct{ reason string }

func (e *settingsError) Error() string { return e.reason }

// reasonOf is the text that says why a read settled nothing: the status and message of the forge's answer, the
// rate limit's end, or what stopped `gh` from running.
func reasonOf(err error) string {
	var settled *settingsError
	var answered *apiError
	switch {
	case errors.As(err, &settled):
		return settled.reason
	case errors.As(err, &answered):
		if answered.Message == "" {
			return fmt.Sprintf("HTTP %d", answered.Status)
		}
		return clip(fmt.Sprintf("HTTP %d: %s", answered.Status, answered.Message))
	}
	return clip(err.Error())
}

// rateReason says when a rate limit ends: the later of the wait a refusal asked for and, for an exhausted window, the
// end of the window, as the worker holds its reads.
func rateReason(rate ports.ForgeRate, now time.Time) string {
	until := time.Time{}
	if rate.RetryAfter > 0 {
		until = now.Add(rate.RetryAfter)
	}
	if rate.Exhausted && rate.Reset.After(now) && rate.Reset.After(until) {
		until = rate.Reset
	}
	if until.IsZero() {
		return "rate limited"
	}
	return "rate limited until " + until.UTC().Format(time.RFC3339)
}

func firstReason(reasons ...string) string {
	for _, reason := range reasons {
		if reason != "" {
			return reason
		}
	}
	return ""
}

func visibilityOf(body []byte) (practices.Visibility, error) {
	var repo struct {
		Visibility string `json:"visibility"`
	}
	if err := json.Unmarshal(body, &repo); err != nil {
		return "", fmt.Errorf("reading gh api: %w", err)
	}
	switch visibility := practices.Visibility(repo.Visibility); visibility {
	case practices.VisibilityPublic, practices.VisibilityPrivate, practices.VisibilityInternal:
		return visibility, nil
	}
	return "", fmt.Errorf("the forge reports the visibility %q", clip(repo.Visibility))
}

// branchRead is the answer of the branch read: whether it says protected, or why it settled nothing.
type branchRead struct {
	protected bool
	reason    string
}

func readBranch(response apiResponse, err error) branchRead {
	if err != nil {
		return branchRead{reason: reasonOf(err)}
	}
	var branch struct {
		Protected bool `json:"protected"`
	}
	if err := json.Unmarshal(response.Body, &branch); err != nil {
		return branchRead{reason: reasonOf(fmt.Errorf("reading gh api: %w", err))}
	}
	return branchRead{protected: branch.Protected}
}

// maxRulePages is how many pages of a branch's rules a read follows, and rulesPerPage is how many rules it asks each
// page for, the most the forge gives (https://docs.github.com/en/rest/using-the-rest-api/using-pagination-in-the-rest-api).
const (
	maxRulePages = 5
	rulesPerPage = 100
)

// rulesRead is the answer of the rules read: the rules that apply to the branch, the most approving reviews a
// `pull_request` rule requires, or why the rules or their review count are not all known.
type rulesRead struct {
	rules   []string
	reviews int
	reason  string
}

// readRules reads the pages of the rules of a branch until one holds a review rule, there is no next page, or
// maxRulePages are read. A page that cannot be read, or a `pull_request` rule whose count cannot be read, leaves the
// review unknown when no rule requires one, and keeps the rules read before it.
func readRules(get func(path string) (apiResponse, error), path string) rulesRead {
	var read rulesRead
	for page := 1; page <= maxRulePages; page++ {
		address := fmt.Sprintf("%s?per_page=%d", path, rulesPerPage)
		if page > 1 {
			address += fmt.Sprintf("&page=%d", page)
		}
		response, err := get(address)
		if err != nil {
			read.reason = firstReason(read.reason, reasonOf(err))
			return read
		}
		if reason := read.add(response.Body); reason != "" {
			read.reason = firstReason(read.reason, reason)
			return read
		}
		switch {
		case read.reviews >= 1 || !strings.Contains(response.Header["link"], `rel="next"`):
			return read
		case page == maxRulePages:
			read.reason = firstReason(read.reason, "the branch has more rules than ropeshift reads")
		}
	}
	return read
}

// add takes the rules of one page, and says why the page could not be read, or nothing. The reason of a rule whose
// count cannot be read is kept, and the page is still read.
func (r *rulesRead) add(body []byte) string {
	var listed []struct {
		Type       string          `json:"type"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		return reasonOf(fmt.Errorf("reading gh api: %w", err))
	}
	for _, rule := range listed {
		r.rules = append(r.rules, rule.Type)
		if rule.Type != "pull_request" {
			continue
		}
		var parameters struct {
			Count *int `json:"required_approving_review_count"`
		}
		if json.Unmarshal(rule.Parameters, &parameters) != nil || parameters.Count == nil {
			r.reason = firstReason(r.reason, "a pull_request rule of the branch has no readable required_approving_review_count")
			continue
		}
		r.reviews = max(r.reviews, *parameters.Count)
	}
	return ""
}

// readReviews reads the protection's count of approving reviews, which is 0 for a protection with no review rule. A
// 404 settles that only when its message says reviews are not enabled.
func readReviews(response apiResponse, err error) (int, string) {
	var answered *apiError
	if errors.As(err, &answered) && answered.Status == 404 && strings.Contains(strings.ToLower(answered.Message), "not enabled") {
		return 0, ""
	}
	if err != nil {
		return 0, reasonOf(err)
	}
	var protection struct {
		Count int `json:"required_approving_review_count"`
	}
	if err := json.Unmarshal(response.Body, &protection); err != nil {
		return 0, reasonOf(fmt.Errorf("reading gh api: %w", err))
	}
	return protection.Count, ""
}

// safeName says an owner or repository name can be put in a path: it is not empty, is not a path segment that climbs, and does
// not start with a dash, which a command would read as a flag.
func safeName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.HasPrefix(name, "-")
}
