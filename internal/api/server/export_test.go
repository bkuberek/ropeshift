package server

import (
	"context"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ports"
)

// RouteOperation is what a test reads of a changing route's operations: every one it can run, and whether
// it picks among them per call.
type RouteOperation struct {
	IDs      []string
	Resolved bool
}

// RouteOperations reads the operation table of the changing routes, by pattern.
func RouteOperations() map[string]RouteOperation {
	out := make(map[string]RouteOperation, len(routeOperations))
	for pattern, route := range routeOperations {
		out[pattern] = RouteOperation{IDs: route.ids, Resolved: route.resolve != nil}
	}
	return out
}

// SetBodyWindows changes how long a request body may take to arrive on the routes that read one before any
// credential is checked, and on the worker plane, until the returned function puts them back.
func SetBodyWindows(unauthenticated, plane time.Duration) func() {
	wasUnauthenticated, wasPlane := unauthenticatedWindow, planeWindow
	unauthenticatedWindow, planeWindow = unauthenticated, plane
	return func() { unauthenticatedWindow, planeWindow = wasUnauthenticated, wasPlane }
}

// WhenConfirmed runs f with a confirming request's context once its card is approved and before its call
// runs, until the test ends.
func WhenConfirmed(t interface{ Cleanup(func()) }, f func(context.Context)) {
	onConfirmed = f
	t.Cleanup(func() { onConfirmed = func(context.Context) {} })
}

// CardConversation names the conversation a card came from, as Needs you and an approval's detail read it.
func CardConversation(members ports.MemberReader, threads ports.ConversationReader, card authority.Approval) (*api.CardConversation, error) {
	return cardConversation(members, threads, card)
}

// OneWayReviewNeeds reads the one-way decisions of an orchestrator that wait for a member's review, in the read a
// test gives it.
func OneWayReviewNeeds(tx ports.ReadTx) ([]api.NeedItem, error) { return oneWayReviewNeeds(tx) }
