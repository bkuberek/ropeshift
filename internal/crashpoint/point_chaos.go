//go:build chaos

package crashpoint

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// EnvPause holds a process at one named step, as "op/step=duration", so a test can
// order a race between two loops of a daemon that runs as its own process.
const EnvPause = "ROPESHIFT_CHAOS_PAUSE"

var handler atomic.Pointer[func(op, step string)]

func init() {
	spec := os.Getenv(EnvPause)
	if spec == "" {
		return
	}
	name, value, _ := strings.Cut(spec, "=")
	pause, err := time.ParseDuration(value)
	if err != nil {
		panic(fmt.Sprintf("crashpoint: %s: %v", EnvPause, err))
	}
	Arm(func(op, step string) {
		if op+"/"+step == name {
			time.Sleep(pause)
		}
	})
}

// Arm installs the function a chaos child runs at every named step.
func Arm(fn func(op, step string)) {
	handler.Store(&fn)
}

// Hit calls the armed function, if there is one.
func Hit(op, step string) {
	if fn := handler.Load(); fn != nil {
		(*fn)(op, step)
	}
}
