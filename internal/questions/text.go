package questions

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// closingMarks are what a question may end on without changing what it asks.
const closingMarks = "?!.:;"

// Normalize is the one comparison the core makes itself: case and spacing, and the marks a
// question closes on. It reads no meaning, so a paraphrase stays a different question.
func Normalize(text string) string {
	folded := strings.Join(strings.Fields(strings.ToLower(text)), " ")
	return strings.TrimRight(strings.TrimRight(folded, closingMarks+" "), " ")
}

// Hash identifies a question's normalized text. A stored question keeps this value, so
// changing Normalize means rewriting every record.
func Hash(text string) string {
	sum := sha256.Sum256([]byte(Normalize(text)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Terms splits text into the words search compares, in order, each once. Everything else
// is dropped, so a search string holds no operator of any database's query language.
func Terms(text string) []string {
	var out []string
	seen := map[string]bool{}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, word := range words {
		if !seen[word] {
			seen[word] = true
			out = append(out, word)
		}
	}
	return out
}
