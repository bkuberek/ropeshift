package server

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
