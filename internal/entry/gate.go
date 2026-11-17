package entry

import (
	"fmt"
	"io"
)

// envGroupMode is the one switch of group mode's preview (ADR-0031). Every group entry point starts only
// with it set to groupPreviewValue, until the gate comes off. This file is its only reader: a test of the
// source holds that no other file names it or asks the gate except the files that dispatch a command, so
// solo mode cannot come to depend on it.
const envGroupMode = "ROPESHIFT_GROUP_MODE"

// groupPreviewValue is the one value that turns the preview on.
const groupPreviewValue = "preview"

// groupRefusal is what a group entry point says while the preview is off.
const groupRefusal = "group mode is a preview in this release; set ROPESHIFT_GROUP_MODE=preview to run it"

// groupGateNote is what help says of a group command, so a person who reads it learns the gate.
const groupGateNote = "A preview: it starts only with " + envGroupMode + "=" + groupPreviewValue + " in its environment, and carries no\n" +
	"compatibility promise."

// groupPreview reports whether the environment turns the preview on: the variable holds exactly the value
// `preview`, and nothing else a person might mean by it.
func groupPreview(getenv func(string) string) bool {
	return getenv(envGroupMode) == groupPreviewValue
}

// refuseGroup says a group command does not start with the preview off, as a usage error: a person asked
// for something this release does not run.
func refuseGroup(command, word string, stderr io.Writer) int {
	fmt.Fprintf(stderr, "%s %s: %s\n", command, word, groupRefusal)
	return exitUsage
}
