package questions

import (
	"slices"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"case", "What Is The Retry Budget", "what is the retry budget"},
		{"spaces and lines", "  what  is\n\tthe   budget  ", "what is the budget"},
		{"a closing mark", "what is the budget?", "what is the budget"},
		{"several closing marks", "what is the budget?!.", "what is the budget"},
		{"a mark inside stays", "is v1.2 still supported?", "is v1.2 still supported"},
		{"non-Latin text", "ÉTAT DU BUDGET ?", "état du budget"},
		{"nothing but marks", " ?! ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.text); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestNormalizedTextGivesAStableHash is the unit's check. The value is fixed here, because a
// change to it would stop every stored question from matching.
func TestNormalizedTextGivesAStableHash(t *testing.T) {
	want := "sha256:5d7ce07ede14016c9e076567fdf1af1e8c95d2b71cf8f1ffad01d94cd242a528"
	if got := Hash("What is the retry budget?"); got != want {
		t.Errorf("hash %s", got)
	}
	if Hash("what  is the retry budget") != Hash("WHAT IS THE RETRY BUDGET ?") {
		t.Error("the same question hashed differently")
	}
	if Hash("what is the retry budget") == Hash("what is the retry limit") {
		t.Error("two questions share a hash")
	}
}

func TestTerms(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{"words", "Retry the Budget", []string{"retry", "the", "budget"}},
		{"marks split", "PAY-12: retry_budget?", []string{"pay", "12", "retry", "budget"}},
		{"a repeat counts once", "budget budget BUDGET", []string{"budget"}},
		{"operators are words", `"a" OR b*`, []string{"a", "or", "b"}},
		{"nothing to search", " -- ?? ", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Terms(tt.text); !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
