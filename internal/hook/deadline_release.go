//go:build !hookdeadline

package hook

import "time"

// raisedDeadline is none in a normal build, so a hook keeps its real deadline whatever its environment
// holds. Only the hookdeadline build, which only tests make, reads one (deadline_raised.go).
func raisedDeadline(func(string) string) time.Duration { return 0 }
