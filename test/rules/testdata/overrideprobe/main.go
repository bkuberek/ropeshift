// Command overrideprobe links the session override, so a rule can read what a build
// holds.
package main

import (
	"fmt"

	"ropeshift.dev/ropeshift/internal/lanes"
)

func main() {
	override, err := lanes.ParseOverride(`{"args":["--bare"]}`)
	fmt.Println(override.Args(), override.StreamLog(), err)
}
