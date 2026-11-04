// Package worker breaks the rule on purpose: it reads a pane to learn a lane's state.
package worker

type terminal interface {
	Capture(name string) (string, error)
}

func state(t terminal) (string, error) { return t.Capture("lane") }
