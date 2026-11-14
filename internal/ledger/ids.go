package ledger

import "strings"

// MaxRecordID bounds an ID a caller names for a record it makes.
const MaxRecordID = 64

// PlainID says an ID is one a caller may name for a record it makes: 1 to 64 ASCII letters, digits, '-', and
// '_', as every ID ropeshift generates is. An agent reads the ID of work, a question, a decision, or a lane
// wherever it reads the record, in an orchestrator's wake and in its tools' replies, and passes it back to a
// tool as it is. A plain ID holds no space, quote, line break, angle bracket, '@', or path separator, so it
// cannot read as a sentence, start a line of its own, close a tag, or name a file.
func PlainID(id string) bool {
	return id != "" && len(id) <= MaxRecordID && strings.IndexFunc(id, func(r rune) bool { return !idMark(r) }) < 0
}

func idMark(r rune) bool {
	return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || r == '-' || r == '_'
}
