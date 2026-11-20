package ledger_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

func TestACheckAWorkerReportsIsKeptOnlyAsADriverAVersionAndPlainWords(t *testing.T) {
	t.Parallel()
	valid := ledger.CheckingCLI("claude-code", "2.1.400")
	tests := []struct {
		name  string
		check ledger.CLICheck
		ok    bool
	}{
		{"a check the ledger would word itself", *valid, true},
		{"a release candidate with build metadata", *ledger.CheckingCLI("claude-code", "2.1.400-rc.1+build.5"), true},
		{"a driver no table holds", *ledger.CheckingCLI("other-cli", "1"), true},
		{"a driver with a space", ledger.CLICheck{Driver: "claude code", Version: "2.1.400", Words: valid.Words}, false},
		{"no driver", ledger.CLICheck{Version: "2.1.400", Words: valid.Words}, false},
		{"a driver too long", ledger.CLICheck{Driver: strings.Repeat("a", 33), Version: "2.1.400", Words: valid.Words}, false},
		{"no version", ledger.CLICheck{Driver: "claude-code", Words: valid.Words}, false},
		{"a version that starts with a dot", ledger.CLICheck{Driver: "claude-code", Version: ".1", Words: valid.Words}, false},
		{"a version that ends in a line break", ledger.CLICheck{Driver: "claude-code", Version: "2.1.400\n", Words: valid.Words}, false},
		{"a version too long", ledger.CLICheck{Driver: "claude-code", Version: strings.Repeat("9", 65), Words: valid.Words}, false},
		{"words with an escape sequence", ledger.CLICheck{Driver: "claude-code", Version: "2.1.400", Words: "Checking \x1b[2J"}, false},
		{"words with a tab", ledger.CLICheck{Driver: "claude-code", Version: "2.1.400", Words: "Checking\tClaude Code"}, false},
		{"words past the bound", ledger.CLICheck{Driver: "claude-code", Version: "2.1.400", Words: strings.Repeat("x", ledger.MaxCheckWords+1)}, false},
		{"words at the bound", ledger.CLICheck{Driver: "claude-code", Version: "2.1.400", Words: strings.Repeat("x", ledger.MaxCheckWords)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.check.Validate(); (err == nil) != tt.ok {
				t.Errorf("Validate() = %v, want ok %v", err, tt.ok)
			}
		})
	}
}

// The ledger's own words for the longest driver and version a check can name stay within the bound a worker's
// words are held to.
func TestTheWordsOfTheLongestCheckFitTheBound(t *testing.T) {
	t.Parallel()
	check := ledger.CheckingCLI(strings.Repeat("a", 32), strings.Repeat("9", 64))
	if err := check.Validate(); err != nil {
		t.Errorf("the longest check the ledger words is refused: %v, with %d bytes of words", err, len(check.Words))
	}
}

// A check says what it means for the lane and what ends it, in one line: the words every surface shows are the
// ledger's own.
func TestACheckSaysWhatItMeansForTheLaneAndWhatEndsIt(t *testing.T) {
	t.Parallel()
	got := ledger.CheckingCLI("claude-code", "2.1.400").Words
	const want = "Checking Claude Code 2.1.400: this lane runs on documented surfaces until the check passes; a respawn then picks up the qualified surfaces"
	if got != want {
		t.Errorf("the words of a check read %q, want %q", got, want)
	}
}
