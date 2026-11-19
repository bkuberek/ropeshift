package server

import (
	"regexp"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
)

// FR-UI-7 holds the web UI to the command line, and this holds it the other way: every operation in the
// class table is one a member can do in the browser, through a route the web listener serves, or it is
// named here. A test of internal/entry does the same for every command, and web/src/api/parity.test.ts
// for every route and its caller in the UI.

// opExempt are the operations the web UI does not offer, each with its reason. The delegate-side variants
// share a member's route and are never a member's operation, and the others sit behind a command FR-UI-7
// leaves to the command line. An operation that has a route of its own the web listener serves is not
// exempt, so an entry that closes by that route fails and leaves the list.
var opExempt = map[string]string{
	authority.OpWorkOrderCreateByAssistant: "The assistant's variant of creating work, which shares a member's route and is never a member's operation.",
	authority.OpPlanStartReviewByAssistant: "The assistant's variant of starting a contract review, which shares a member's route and is never a member's operation.",
	authority.OpMessageSendByAssistant:     "The assistant's variant of sending a message, which shares a member's route and is never a member's operation.",
	authority.OpApprovalDecideDelegated:    "A delegate's decision of a request within the delegation policy, which shares a member's route and is never a member's operation.",
	authority.OpWorkOrderWrite:             "FR-UI-7 leaves `work write` to the command line: it stores one event under a claim's fence, which the worker protocol relies on.",
	authority.OpRepositoryRegisterManifest: "FR-UI-7 leaves `repository add --manifest` to the command line: it reads paths on the machine from a file an agent can write.",
}

// opGaps are the operations the web UI should offer and does not yet, each with the slice of U2 that adds
// it. An entry stays only while the operation has no route the web listener serves, so it leaves the list
// when its gap closes, and the first run of this test wrote it.
var opGaps = map[string]string{
	// Raising the project's daily or weekly limit is not on a page yet; SP4 builds it (ADR-0037).
	authority.OpSpendRaiseProject: "SP4",
	// Running the practices check is not on a page yet; P5i builds it.
	authority.OpPracticesCheck: "P5i",
}

var (
	placeholder = regexp.MustCompile(`\{[^}]*\}`)
	gapSlice    = regexp.MustCompile(`^(U2[a-k]|P[345][a-j]|SP[1-9])[12]?$`)
)

// shape is a route's pattern with every placeholder unnamed, so two patterns compare whatever each calls
// an ID.
func shape(pattern string) string { return placeholder.ReplaceAllString(pattern, "{}") }

// webServed is every route the web listener serves, as routeOperations keys it: the method and the path.
// A project's route reads under /v1/projects/{project}/ on the listener, and the server takes that prefix
// off before it looks the route up (withinProject), so each such row is listed under both spellings.
func webServed() map[string]bool {
	served := map[string]bool{}
	for _, route := range api.WebRoutes() {
		served[route.Method+" "+shape(route.Pattern)] = true
		if rest, ok := strings.CutPrefix(route.Pattern, projectPrefix+"{project}/"); ok {
			served[route.Method+" "+shape("/v1/"+rest)] = true
		}
	}
	return served
}

// TestEveryOperationHasAWebRoute holds the class table to the web listener: each operation passes when a
// route that runs it is one the listener serves, or when opExempt gives its reason, or opGaps the slice
// that closes it. A new operation fails here until it has one of the three.
func TestEveryOperationHasAWebRoute(t *testing.T) {
	served := webServed()
	reached := map[string]bool{}
	own := map[string]bool{}
	for pattern, route := range routeOperations {
		method, path, _ := strings.Cut(pattern, " ")
		if !served[method+" "+shape(path)] {
			continue
		}
		for _, id := range route.ids {
			reached[id] = true
			if len(route.ids) == 1 {
				own[id] = true
			}
		}
	}
	if len(reached) < 50 {
		t.Fatalf("%d operations reached, so the route table read too few", len(reached))
	}
	for _, op := range authority.Operations() {
		reason, isExempt := opExempt[op.ID]
		_, isGap := opGaps[op.ID]
		switch {
		case isExempt && isGap:
			t.Errorf("%s is in opExempt and in opGaps: it is one or the other", op.ID)
		case !reached[op.ID] && !isExempt && !isGap:
			t.Errorf("%s (%s) has no route the web listener serves, and is in neither opExempt nor opGaps: serve its route and build it in the UI, or list it with the reason or the slice", op.ID, op.Label)
		case reached[op.ID] && isGap:
			t.Errorf("%s is served to the browser now, so remove it from opGaps", op.ID)
		case own[op.ID] && isExempt:
			t.Errorf("%s has a route of its own that the web listener serves, so remove it from opExempt", op.ID)
		}
		if isExempt && strings.TrimSpace(reason) == "" {
			t.Errorf("opExempt gives %s no reason", op.ID)
		}
	}
	for id := range opExempt {
		if _, ok := authority.OperationOf(id); !ok {
			t.Errorf("opExempt names %s, which the class table lacks", id)
		}
	}
	for id, slice := range opGaps {
		if _, ok := authority.OperationOf(id); !ok {
			t.Errorf("opGaps names %s, which the class table lacks", id)
		}
		if !gapSlice.MatchString(slice) {
			t.Errorf("opGaps names %s as %q, which is no slice of U2", id, slice)
		}
	}
}
