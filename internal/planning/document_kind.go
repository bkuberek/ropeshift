package planning

import (
	"path"
	"strings"
	"unicode"
)

// DocumentKind says what a document is to a person looking for one. A source that knows says so, and
// a source that does not reads it from the document's path.
type DocumentKind string

// The kinds a person looks for, and the rest.
const (
	DocumentRequirements DocumentKind = "requirements"
	DocumentSpec         DocumentKind = "spec"
	DocumentDesign       DocumentKind = "design"
	DocumentOther        DocumentKind = "other"
)

// kindWords are the words in a path that name a kind.
var kindWords = map[string]DocumentKind{
	"requirement": DocumentRequirements, "requirements": DocumentRequirements,
	"spec": DocumentSpec, "specs": DocumentSpec, "specification": DocumentSpec, "specifications": DocumentSpec,
	"design": DocumentDesign, "designs": DocumentDesign, "architecture": DocumentDesign,
}

// KindOfPath reads a kind from a document's path. The nearest name decides, so a file names itself
// before its folder does, and a word counts only whole.
func KindOfPath(name string) DocumentKind {
	parts := strings.Split(strings.TrimSuffix(strings.ToLower(name), path.Ext(name)), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		words := strings.FieldsFunc(parts[i], func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		for j := len(words) - 1; j >= 0; j-- {
			if kind, ok := kindWords[words[j]]; ok {
				return kind
			}
		}
	}
	return DocumentOther
}
