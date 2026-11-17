// Command deadlineprobe links the hook, so a rule can read what a build holds.
package main

import (
	"os"

	"ropeshift.com/ropeshift/internal/hook"
)

func main() {
	os.Exit(hook.Main(os.Args[1:], os.Stdin, os.Stdout, os.Getenv))
}
