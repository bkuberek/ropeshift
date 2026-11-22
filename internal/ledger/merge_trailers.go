package ledger

import (
	"slices"
	"strings"
)

// The trailers of a commit that lands a work order's reviewed commit on its base. A repeat of a merge finds the commit by
// them, and a reading of a base tells ropeshift's commits from a person's by them.
const (
	TrailerWorkOrder = "Ropeshift-Work-Order"
	TrailerReviewed  = "Ropeshift-Reviewed-Commit"
)

// MergeTrailers is the trailer block of a commit that lands the reviewed commit of a work order: one line for each
// trailer, the last ended by a newline.
func MergeTrailers(workOrder, reviewed string) string {
	return TrailerWorkOrder + ": " + workOrder + "\n" + TrailerReviewed + ": " + reviewed + "\n"
}

// WithTrailers appends trailers to a commit message. They follow a blank line, or join the trailer block the message
// already ends in, and a line the block already holds is not added again. The message is kept as it is, spaces and
// blank lines included, except for the blank lines that end it. The result ends in a newline.
func WithTrailers(message, trailers string) string {
	message = trimBlankEnd(message)
	wanted := strings.Split(strings.TrimRight(trailers, "\n"), "\n")
	if message == "" {
		return strings.Join(wanted, "\n") + "\n"
	}
	head, last := "", message
	if at := strings.LastIndex(message, "\n\n"); at >= 0 {
		head, last = message[:at+2], message[at+2:]
	}
	held := strings.Split(last, "\n")
	if head == "" || !trailerBlock(held) {
		return message + "\n\n" + strings.Join(wanted, "\n") + "\n"
	}
	out := message
	for _, line := range wanted {
		if !slices.Contains(held, line) {
			out += "\n" + line
		}
	}
	return out + "\n"
}

// trailerBlock says every line of a paragraph is a trailer, `Token: value`, or a folded line of one, which begins with
// white space.
func trailerBlock(lines []string) bool {
	for i, line := range lines {
		if line == "" {
			return false
		}
		if i > 0 && (line[0] == ' ' || line[0] == '\t') {
			continue
		}
		token, _, found := strings.Cut(line, ":")
		if !found || token == "" {
			return false
		}
		for _, c := range token {
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

// trimBlankEnd takes the lines off the end of a message that hold nothing but white space, and keeps the white space of
// the last line that holds something.
func trimBlankEnd(message string) string {
	lines := strings.Split(message, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
