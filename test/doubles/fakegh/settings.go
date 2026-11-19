package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/test/doubles/ghstate"
)

// settingsReply is what the forge gives for a settings read: its body, whether it has one to give, and the headers it
// adds.
type settingsReply struct {
	body    any
	exists  bool
	headers map[string]string
}

// settings answers the reads of a repository's settings: `repos/{owner}/{repo}` for its visibility,
// `repos/{owner}/{repo}/branches/{branch}` for whether the branch is protected,
// `repos/{owner}/{repo}/rules/branches/{branch}` for the rules that apply to it, a page at a time, and
// `repos/{owner}/{repo}/branches/{branch}/protection/required_pull_request_reviews` for the reviews a merge
// requires. It says whether the address was one of them. A status in the state's SettingsStatus answers instead of
// the body, by the endpoint with its query, or by the endpoint.
func (r *response) settings(endpoint, query string, parts []string) bool {
	values, err := url.ParseQuery(query)
	if err != nil {
		fail(2, "fakegh: api query %q", query)
	}
	reply, matched := r.settingsReply(parts, values)
	if !matched {
		return false
	}
	headers := map[string]string{}
	r.rateHeaders(headers)
	for name, value := range reply.headers {
		headers[name] = value
	}
	answer, set := r.state.SettingsStatus[endpoint+"?"+query]
	if !set {
		answer, set = r.state.SettingsStatus[endpoint]
	}
	if set {
		for name, value := range answer.Headers {
			headers[name] = value
		}
		if answer.Status > 299 {
			r.send(answer.Status, headers, map[string]string{"message": answer.Message})
		}
		if answer.Body != "" {
			r.sendRaw(answer.Status, headers, answer.Body)
		}
		r.send(answer.Status, headers, reply.body)
	}
	if !reply.exists {
		r.send(404, headers, map[string]string{"message": "Branch not protected"})
	}
	r.send(200, headers, reply.body)
	return true
}

// sendRaw writes a reply whose body is not marshaled, and ends the process.
func (r *response) sendRaw(status int, headers map[string]string, body string) {
	if r.include {
		fmt.Printf("HTTP/2.0 %d %s\r\n", status, statusText(status))
		for name, value := range headers {
			fmt.Printf("%s: %s\r\n", name, value)
		}
		fmt.Print("\r\n")
	}
	fmt.Println(body)
	os.Exit(0)
}

// settingsReply is the reply of the read an address names, and false for an address that is no settings read.
func (r *response) settingsReply(parts []string, query url.Values) (settingsReply, bool) {
	switch {
	case len(parts) == 3:
		visibility := r.state.Visibility
		for _, repository := range r.state.Repositories {
			if strings.EqualFold(repository.Owner, parts[1]) && strings.EqualFold(repository.Name, parts[2]) && repository.Visibility != "" {
				visibility = repository.Visibility
			}
		}
		if visibility == "" {
			visibility = "private"
		}
		return settingsReply{exists: true, body: map[string]any{"full_name": parts[1] + "/" + parts[2], "visibility": visibility, "private": visibility != "public"}}, true
	case len(parts) >= 5 && parts[3] == "rules" && parts[4] == "branches":
		return r.rulesPage(query), true
	case len(parts) >= 7 && parts[3] == "branches" && parts[len(parts)-2] == "protection" && parts[len(parts)-1] == "required_pull_request_reviews":
		return settingsReply{exists: r.state.Protected, body: map[string]any{"required_approving_review_count": r.state.ReviewCount}}, true
	case len(parts) >= 5 && parts[3] == "branches":
		return settingsReply{exists: true, body: map[string]any{"name": unescaped(parts[4:]), "protected": r.state.Protected}}, true
	}
	return settingsReply{}, false
}

// rulesPage is the page of the rules the query names, with a Link header for the next page when rules remain after it.
func (r *response) rulesPage(query url.Values) settingsReply {
	rules := r.state.Rules
	if rules == nil {
		rules = []ghstate.Rule{}
	}
	perPage := r.state.RulesPerPage
	if perPage <= 0 {
		return settingsReply{exists: true, body: rules}
	}
	page, err := strconv.Atoi(query.Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	start, end := min((page-1)*perPage, len(rules)), min(page*perPage, len(rules))
	reply := settingsReply{exists: true, body: rules[start:end]}
	if end < len(rules) {
		reply.headers = map[string]string{"Link": fmt.Sprintf(`<https://api.github.com/repositories/1/rules/branches/main?per_page=%d&page=%d>; rel="next"`, perPage, page+1)}
	}
	return reply
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
