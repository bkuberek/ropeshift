package rules

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Group mode is a preview behind an environment variable until its gate comes off (ADR-0031). These checks
// hold the gate to one reader and its callers to the files that dispatch a command, so solo mode cannot
// come to depend on it.

const (
	entryDir          = "internal/entry"
	gateFile          = entryDir + "/gate.go"
	groupGateVariable = "ROPESHIFT_GROUP_MODE"
	// gateIdentifier is the constant that holds the variable's name inside the gate file.
	gateIdentifier = "envGroupMode"
	// gateCall is how the gate is asked.
	gateCall = "groupPreview("
)

// gateCallers are the files that may ask the gate: the gate itself, the main that dispatches a command, and
// the file that lists the commands, because a group command is dispatched and listed only with the gate on.
var gateCallers = []string{gateFile, entryDir + "/entry.go", entryDir + "/routes.go"}

// CheckGate refuses a non-test Go file that names the group mode variable, or the constant that holds its
// name, anywhere but the gate file, and a call to the gate from any file but the gate's callers.
func CheckGate(root string) ([]Violation, error) {
	var found []Violation
	err := eachSource(root, func(rel, text string) {
		if rel != gateFile {
			if strings.Contains(text, groupGateVariable) {
				found = append(found, Violation{"gate-one-reader", rel, "names " + groupGateVariable + ", which only " + gateFile + " may"})
			}
			if strings.Contains(text, gateIdentifier) {
				found = append(found, Violation{"gate-one-reader", rel, "uses " + gateIdentifier + ", which only " + gateFile + " may"})
			}
		}
		if strings.Contains(text, gateCall) && !slices.Contains(gateCallers, rel) {
			found = append(found, Violation{"gate-callers", rel, "asks the gate, which only " + strings.Join(gateCallers, ", ") + " may"})
		}
	})
	return found, err
}

// eachSource reads every non-test Go file under cmd and internal, with its path from the root.
func eachSource(root string, visit func(rel, text string)) error {
	for _, top := range []string{"cmd", "internal"} {
		dir := filepath.Join(root, top)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "testdata" {
				return filepath.SkipDir
			}
			if d.IsDir() || !isSource(d.Name()) {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			visit(filepath.ToSlash(rel), string(raw))
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
