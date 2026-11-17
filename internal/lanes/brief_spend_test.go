package lanes_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

// forbiddenInALanesWords are what a lane never reads about its spend (FR-SPEND-9, CS7): a limit, a band, what it has
// left. A lane that knew its limit would make it a target, and learns of one only from the denial at the hard stop.
var forbiddenInALanesWords = []string{
	"soft limit", "hard limit", "spend limit", "over_estimate", "spend_limit", "remaining spend", "charged spend", "cost band",
	"estimated cost", "your budget", "p85", "$",
}

// No brief a lane is given states a limit, a band, or what remains to spend. The briefs are the goldens that hold each
// kind's words, so a sentence added to one of them is read here.
func TestNoBriefStatesALimitOrARemainingSpend(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "brief-*.golden"))
	if err != nil || len(files) < 5 {
		t.Fatalf("the brief goldens are %v, %v", files, err)
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(string(raw))
		for _, word := range forbiddenInALanesWords {
			if strings.Contains(text, word) {
				t.Errorf("%s says %q", file, word)
			}
		}
	}
}

// A brief is made of a BriefSpec alone, so what the spec can hold is all a lane's brief can say. It holds no field about
// spend, a limit, or a band, and a field added for one fails here before it reaches a brief.
func TestABriefSpecHoldsNothingAboutSpend(t *testing.T) {
	kind := reflect.TypeOf(lanes.BriefSpec{})
	for i := range kind.NumField() {
		name := strings.ToLower(kind.Field(i).Name)
		for _, word := range []string{"spend", "limit", "band", "cost", "budget", "estimate", "remaining"} {
			if strings.Contains(name, word) {
				t.Errorf("BriefSpec has a field %s, and a lane never reads %s", kind.Field(i).Name, word)
			}
		}
	}
}
