// Package lanes is a fixture that reads a transcript.
package lanes

import "os"

// Read reads a vendor file the design forbids.
func Read(home string) ([]byte, error) {
	return os.ReadFile(home + "/.claude/projects/session.jsonl")
}
