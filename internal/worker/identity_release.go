//go:build !screenshots

package worker

// newWorkerID is a new random ID: a normal build names each worker apart. The screenshots build gives a
// fixed one in its place (identity_screenshots.go).
func newWorkerID() (string, error) { return newToken(16) }
