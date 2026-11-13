package contract

import (
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

// TestJournalKeepsTheLanePeaks holds the peaks a recommendation reads past a restart, and a later save
// replaces them.
func TestJournalKeepsTheLanePeaks(t *testing.T) {
	t.Parallel()
	j := openTestJournal(t)
	ctx := t.Context()
	got, err := j.LanePeaks(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("a new journal holds %+v, %v", got, err)
	}
	want := []lanes.LanePeak{{CPU: 0.5, MemoryBytes: 1 << 30}, {CPU: 1.25, MemoryBytes: 3 << 29}}
	if err := j.SaveLanePeaks(ctx, want); err != nil {
		t.Fatal(err)
	}
	if got, err = j.LanePeaks(ctx); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, %v", got, err)
	}
	if err := j.SaveLanePeaks(ctx, want[1:]); err != nil {
		t.Fatal(err)
	}
	if got, err = j.LanePeaks(ctx); err != nil || !reflect.DeepEqual(got, want[1:]) {
		t.Errorf("after a second save, got %+v, %v", got, err)
	}
}
