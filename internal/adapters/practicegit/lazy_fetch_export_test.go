package practicegit

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
)

// WithoutNoLazyFetch makes the scan and the snapshot run git without GIT_NO_LAZY_FETCH until the test ends, as they
// run it on a git older than 2.44, which ignores the variable. The commands the guard of a partial clone keeps off then
// reach the promisor remote on any git. It changes the package's settings, so a test that calls it is never parallel.
func WithoutNoLazyFetch(t *testing.T) {
	t.Helper()
	scan, snapshot := secretScanEnvironment, snapshotNoReplace
	without := func(env []string) []string {
		return slices.DeleteFunc(slices.Clone(env), func(entry string) bool { return entry == gitcli.NoLazyFetch })
	}
	secretScanEnvironment, snapshotNoReplace = without(scan), without(snapshot)
	t.Cleanup(func() { secretScanEnvironment, snapshotNoReplace = scan, snapshot })
}
