package questions

import (
	"errors"
	"strings"
	"testing"
)

// TestNewRefusesAFieldOverItsCap holds that a question with a field over its byte cap, or more options than the
// cap allows, is invalid, and that the error names the field and the cap.
func TestNewRefusesAFieldOverItsCap(t *testing.T) {
	long := strings.Repeat("a", 2001)
	atCap := strings.Repeat("a", 2000)
	many := make([]string, 21)
	for i := range many {
		many[i] = strings.Repeat("o", i+1)
	}
	tests := []struct {
		name  string
		draft Question
		want  string
	}{
		{"text", Question{Text: long}, "text is over 2000 bytes"},
		{"an option", Question{Text: "Which?", Options: []string{"One", long}}, "option 2 is over 2000 bytes"},
		{"the recommendation", Question{Text: "Which?", Recommendation: long}, "recommendation is over 2000 bytes"},
		{"the rationale", Question{Text: "Which?", Recommendation: "One", RecommendationRationale: long}, "recommendation_rationale is over 2000 bytes"},
		{"the option count", Question{Text: "Which?", Options: many}, "at most 20 options"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.draft.ID = "q-1"
			_, err := New(tt.draft)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not say %q", err, tt.want)
			}
		})
	}
	if _, err := New(Question{ID: "q-1", Text: atCap, Options: many[:20], Recommendation: many[0], RecommendationRationale: atCap}); err != nil {
		t.Errorf("fields at their caps: %v", err)
	}
}
