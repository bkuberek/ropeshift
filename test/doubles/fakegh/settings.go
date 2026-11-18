package main

import (
	"net/url"
	"strings"

	"ropeshift.com/ropeshift/test/doubles/ghstate"
)

// settings answers the reads of a repository's settings: `repos/{owner}/{repo}` for its visibility,
// `repos/{owner}/{repo}/branches/{branch}` for whether the branch is protected,
// `repos/{owner}/{repo}/rules/branches/{branch}` for the rules that apply to it, and
// `repos/{owner}/{repo}/branches/{branch}/protection/required_pull_request_reviews` for the reviews a merge
// requires. It says whether the address was one of them. A status in the state's SettingsStatus answers instead of
// the body.
func (r *response) settings(endpoint string, parts []string) bool {
	body, exists, matched := r.settingsBody(parts)
	if !matched {
		return false
	}
	headers := map[string]string{}
	r.rateHeaders(headers)
	if answer, set := r.state.SettingsStatus[endpoint]; set {
		for name, value := range answer.Headers {
			headers[name] = value
		}
		if answer.Status > 299 {
			r.send(answer.Status, headers, map[string]string{"message": answer.Message})
		}
		r.send(answer.Status, headers, body)
	}
	if !exists {
		r.send(404, headers, map[string]string{"message": "Branch not protected"})
	}
	r.send(200, headers, body)
	return true
}

// settingsBody is the body of the read an address names, whether the forge has one to give, and whether the address
// names a settings read at all.
func (r *response) settingsBody(parts []string) (body any, exists, matched bool) {
	switch {
	case len(parts) == 3:
		visibility := r.state.Visibility
		if visibility == "" {
			visibility = "private"
		}
		return map[string]any{"full_name": parts[1] + "/" + parts[2], "visibility": visibility, "private": visibility != "public"}, true, true
	case len(parts) >= 5 && parts[3] == "rules" && parts[4] == "branches":
		rules := r.state.Rules
		if rules == nil {
			rules = []ghstate.Rule{}
		}
		return rules, true, true
	case len(parts) >= 7 && parts[3] == "branches" && parts[len(parts)-2] == "protection" && parts[len(parts)-1] == "required_pull_request_reviews":
		return map[string]any{"required_approving_review_count": r.state.ReviewCount}, r.state.Protected, true
	case len(parts) >= 5 && parts[3] == "branches":
		return map[string]any{"name": unescaped(parts[4:]), "protected": r.state.Protected}, true, true
	}
	return nil, false, false
}

// unescaped joins the segments of a branch name in an address, each unescaped.
func unescaped(segments []string) string {
	out := make([]string, 0, len(segments))
	for _, segment := range segments {
		value, err := url.PathUnescape(segment)
		if err != nil {
			fail(2, "fakegh: api path segment %q", segment)
		}
		out = append(out, value)
	}
	return strings.Join(out, "/")
}
