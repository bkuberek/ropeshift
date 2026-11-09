// Command ropeshiftd runs the control plane and the worker. A release links this
// name to the ropeshift binary, and go install builds it as a second copy.
package main

import (
	"os"

	"ropeshift.com/ropeshift/internal/entry"
)

func main() {
	os.Exit(entry.Main(os.Args, os.Stdout, os.Stderr))
}
