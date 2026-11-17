package projectfiles

import "strings"

// maxNameLength is the longest project name the ledger takes.
const maxNameLength = 63

// NameOf makes a project name of a directory's name: lowercase letters and digits, with a hyphen for each run
// of anything else. It is empty when nothing is left. `ropeshift new`, `ropeshift adopt`, and a worker writing
// a folder for the web UI name a project the same way.
func NameOf(directory string) string {
	var out strings.Builder
	gap := false
	for _, r := range strings.ToLower(directory) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if gap && out.Len() > 0 {
				out.WriteByte('-')
			}
			out.WriteRune(r)
			gap = false
			continue
		}
		gap = true
	}
	name := out.String()
	if len(name) > maxNameLength {
		name = strings.TrimRight(name[:maxNameLength], "-")
	}
	return name
}
