package server

import (
	"context"

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
