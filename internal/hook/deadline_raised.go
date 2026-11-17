//go:build hookdeadline

package hook

import "time"

// raisedDeadline reads the deadline a test gives a hook in place of hardDeadline, so a test that checks
// a hook's verdict does not race the real deadline on a loaded machine. Only tests build with the
// hookdeadline tag, and a value that does not parse raises nothing.
func raisedDeadline(getenv func(string) string) time.Duration {
	raised, err := time.ParseDuration(getenv(EnvTestDeadline))
	if err != nil {
		return 0
	}
	return raised
}
