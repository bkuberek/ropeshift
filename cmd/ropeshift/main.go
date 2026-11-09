// Command ropeshift is the CLI, and it serves the MCP, TUI, hook, and shim entry
// points. All of its behavior lives behind internal/entry.
package main

import (
	"os"

	"ropeshift.com/ropeshift/internal/entry"
)

func main() {
	os.Exit(entry.Main(os.Args, os.Stdout, os.Stderr))
}
