package cli

import (
	"strings"
	"testing"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
)

// hostileID is a repository ID as a ledger could hold one that another machine wrote: an escape sequence, a bell,
// and a line break among the first twelve characters, which is what a short ID keeps.
const hostileID = "a\x1b[31mb\x07\nc\x1b]0;title\x07defghijklmnop"

// A short ID is the first twelve characters, which a multibyte character must not be cut in the middle of.
func TestShortRepositoryCutsByCharacterNotByByte(t *testing.T) {
	id := "a" + strings.Repeat("é", 13)
	short := shortRepository(id)
	if !utf8.ValidString(short) || short != "a"+strings.Repeat("é", 11) {
		t.Errorf("the short ID of %q is %q, want the first twelve characters", id, short)
	}
	if got := shortRepository("0123456789ab"); got != "0123456789ab" {
		t.Errorf("an ID of twelve characters is cut to %q", got)
	}
}

// Every page that prints a repository's short ID prints it as plain text on its own line, whatever the ID holds.
func TestShortRepositoryIDsPrintAsPlainTextOnOneLine(t *testing.T) {
	prints := map[string]struct {
		text  string
		lines int
	}{
		"the repository list": {repositoryLine(api.RepositoryView{Repository: ledger.Repository{ID: hostileID}}), 1},
		"a registration": {addedText(api.RepositoryAddReport{Repositories: []api.RepositoryAdded{{
			Path: "/srv/a", Outcome: ledger.AlreadyRegistered, Repository: &ledger.Repository{ID: hostileID},
		}}}), 1},
		"a project": {projectText(api.ProjectDetail{Records: []ledger.Repository{{ID: hostileID}}}), -1},
		"the measures": {measuresText(measures.Report{ReviewAgreement: measures.ReviewAgreement{
			Repositories: []measures.RepositoryAgreement{{Repository: hostileID, MergeAuthority: "person_merges"}},
		}}), -1},
	}
	for name, print := range prints {
		if strings.ContainsAny(print.text, "\x1b\x07") || strings.Contains(print.text, "title") {
			t.Errorf("%s prints what a terminal would act on: %q", name, print.text)
		}
		for _, line := range strings.Split(strings.TrimSuffix(print.text, "\n"), "\n") {
			if strings.HasPrefix(line, "c") {
				t.Errorf("%s breaks the ID across lines: %q", name, print.text)
			}
		}
		if print.lines > 0 && strings.Count(print.text, "\n") != print.lines {
			t.Errorf("%s prints %d lines, want %d: %q", name, strings.Count(print.text, "\n"), print.lines, print.text)
		}
	}
}
