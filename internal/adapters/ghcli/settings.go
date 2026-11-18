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
//     (https://docs.github.com/en/rest/repos/rules#get-rules-for-a-branch);
//   - when the branch has branch protection and no rule requires a review,
//     repos/{owner}/{repo}/branches/{branch}/protection/required_pull_request_reviews for
//     `required_approving_review_count`, which a 404 whose message holds "not enabled" gives as 0
//     (https://docs.github.com/en/rest/branches/branch-protection).
//
// A read the sign-in is refused, that is rate limited, that the forge answers with a status that settles nothing, or
// that `gh` cannot make leaves the fact it decides unknown with the forge's message, clipped as every message of the
// adapter is, and is no error. A count of 0 with no unknown reason means the forge settled that no review is
// required. A rate limit also stops the reads that remain, leaves their facts unknown, and returns the facts with an
// error that wraps the *ports.RateLimitedError. A canceled context returns its error and no facts.
func (f Forge) Settings(ctx context.Context, repo ports.ForgeRepo, branch string) (practices.ForgeFacts, error) {
	b := repo.Binding
	root := fmt.Sprintf("repos/%s/%s", url.PathEscape(b.Owner), url.PathEscape(b.Repo))
	var limited *ports.RateLimitedError
	var limitedReason string
	// get returns the body of a read, or the reason it settles nothing.
	get := func(path string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if limited != nil {
			return nil, &settingsError{reason: limitedReason}
		}
		response, err := f.api(ctx, repo.Dir, b.Host, path)
		var rate *ports.RateLimitedError
		if errors.As(err, &rate) {
			limited, limitedReason = rate, rateReason(rate.Rate, time.Now())
			return nil, &settingsError{reason: limitedReason}
		}
		if err != nil {
			return nil, err
		}
		return response.Body, nil
	}

	var facts practices.ForgeFacts
	if body, err := get(root); err != nil {
		facts.VisibilityUnknown = reasonOf(err)
	} else if visibility, err := visibilityOf(body); err != nil {
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
func branchFacts(get func(path string) ([]byte, error), root, ref string, facts *practices.ForgeFacts) {
	branch := readBranch(get(root + "/branches/" + ref))
	rules := readRules(get(root + "/rules/branches/" + ref))
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
	case !protected, !branch.protected && branch.reason == "":
		// No protection, or a ruleset with no review rule and no branch protection to read: the forge settled it.
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

// rateReason says when a rate limit ends: the later of the end of the window and the wait a refusal asked for.
func rateReason(rate ports.ForgeRate, now time.Time) string {
	until := time.Time{}
	if rate.Reset.After(now) {
		until = rate.Reset
	}
	if rate.RetryAfter > 0 && now.Add(rate.RetryAfter).After(until) {
		until = now.Add(rate.RetryAfter)
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

func readBranch(body []byte, err error) branchRead {
	if err != nil {
		return branchRead{reason: reasonOf(err)}
	}
	var branch struct {
		Protected bool `json:"protected"`
	}
	if err := json.Unmarshal(body, &branch); err != nil {
		return branchRead{reason: reasonOf(fmt.Errorf("reading gh api: %w", err))}
	}
	return branchRead{protected: branch.Protected}
}

// rulesRead is the answer of the rules read: the rules that apply to the branch, the most approving reviews a
// `pull_request` rule requires, or why it settled nothing.
type rulesRead struct {
	rules   []string
	reviews int
	reason  string
}

func readRules(body []byte, err error) rulesRead {
	if err != nil {
		return rulesRead{reason: reasonOf(err)}
	}
	var listed []struct {
		Type       string          `json:"type"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		return rulesRead{reason: reasonOf(fmt.Errorf("reading gh api: %w", err))}
	}
	var read rulesRead
	for _, rule := range listed {
		read.rules = append(read.rules, rule.Type)
		if rule.Type != "pull_request" {
			continue
		}
		var parameters struct {
			Count int `json:"required_approving_review_count"`
		}
		if json.Unmarshal(rule.Parameters, &parameters) == nil {
			read.reviews = max(read.reviews, parameters.Count)
		}
	}
	return read
}

// readReviews reads the protection's count of approving reviews, which is 0 for a protection with no review rule. A
// 404 settles that only when its message says reviews are not enabled.
func readReviews(body []byte, err error) (int, string) {
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
	if err := json.Unmarshal(body, &protection); err != nil {
		return 0, reasonOf(fmt.Errorf("reading gh api: %w", err))
	}
	return protection.Count, ""
}
