//go:build !chaos

package crashpoint

// Hit marks a named step of an operation. It does nothing in a normal build.
func Hit(op, step string) {}
