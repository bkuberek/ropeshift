package lanes_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// TestChangeSizeOfCountsFilesAndLines reads what `git diff --numstat` says a branch changed (FR-OBS-13):
// one line per file, a binary file counting as a changed file with no lines.
func TestChangeSizeOfCountsFilesAndLines(t *testing.T) {
	for name, tt := range map[string]struct {
		numstat string
		want    ledger.ChangeSize
	}{
		"nothing changed":                    {"", ledger.ChangeSize{}},
		"text files":                         {"3\t1\tmain.go\n10\t0\tdocs/new.md\n", ledger.ChangeSize{Files: 2, Added: 13, Removed: 1}},
		"a binary file counts with no lines": {"-\t-\tlogo.png\n2\t2\tREADME.md\n", ledger.ChangeSize{Files: 2, Added: 2, Removed: 2}},
		"renames":                            {"0\t0\told.txt => new.txt\n1\t0\tsrc/{a => b}/c.go\n", ledger.ChangeSize{Files: 2, Added: 1}},
		"a quoted path":                      {"1\t1\t\"a\\tb.txt\"\n", ledger.ChangeSize{Files: 1, Added: 1, Removed: 1}},
		"no final newline":                   {"4\t2\tmain.go", ledger.ChangeSize{Files: 1, Added: 4, Removed: 2}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := lanes.ChangeSizeOf(tt.numstat)
			if err != nil || got != tt.want {
				t.Errorf("change size %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

// TestChangeSizeOfRefusesWhatIsNotNumstat never turns a line it cannot read into a count.
func TestChangeSizeOfRefusesWhatIsNotNumstat(t *testing.T) {
	for name, numstat := range map[string]string{
		"words for counts":  "x\ty\tmain.go\n",
		"one count missing": "3\tmain.go\n",
		"a negative count":  "-3\t1\tmain.go\n",
		"half a binary":     "-\t2\tlogo.png\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := lanes.ChangeSizeOf(numstat); err == nil {
				t.Errorf("read %q as %+v", numstat, got)
			}
		})
	}
}
