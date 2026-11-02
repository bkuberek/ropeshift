// Command evidencehook appends what one hook of a real session saw to the log its argument names. A
// real suite runs it beside the generated hooks. It prints nothing and exits 0 whatever happens, so
// it changes nothing a session does.
package main

import (
	"io"
	"os"
	"time"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/test/doubles/evidence"
)

// maxInput caps what one hook reads, as the production hook does.
const maxInput = 4 << 20

func main() {
	if len(os.Args) != 2 {
		return
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, maxInput))
	if err != nil {
		return
	}
	tags := evidence.Tags{Lane: os.Getenv(lanes.EnvLaneID), Generation: os.Getenv(lanes.EnvGeneration), Home: os.Getenv("HOME")}
	file, err := os.OpenFile(os.Args[1], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	// One write, so hooks that run side by side cannot interleave a line.
	_, _ = file.Write(evidence.HookLine(input, tags, time.Now()))
}
