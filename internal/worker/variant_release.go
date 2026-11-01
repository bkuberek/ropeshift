//go:build !chaos

package worker

// chargeKey is the key: a normal build charges one act once.
func chargeKey(key string) string { return key }
