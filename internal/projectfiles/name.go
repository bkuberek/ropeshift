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

// SafeSegment says a word is not empty and holds only letters, digits, and . _ -: a word a Makefile and its shell
// recipes read as the word it is, with no variable, comment, quote, or separator in it.
func SafeSegment(word string) bool {
	return word != "" && strings.Trim(word, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-") == ""
}
