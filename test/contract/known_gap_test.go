package contract

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// gapRecorder stands in for a check's test, and records how a marker ended it. Skipf and Fatalf stop the
// goroutine, as they stop a test.
type gapRecorder struct {
	testing.TB
	skipped, failed string
}

func (*gapRecorder) Helper() {}

func (g *gapRecorder) Skipf(format string, args ...any) {
	g.skipped = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func (g *gapRecorder) Fatalf(format string, args ...any) {
	g.failed = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// markedWith runs knownGap on what a check found.
func markedWith(found error) (*gapRecorder, error) {
	recorder := &gapRecorder{}
	var returned error
	done := make(chan struct{})
	go func() {
		defer close(done)
		returned = knownGap(&Rig{T: recorder}, "GAP-X", found)
	}()
	<-done
	return recorder, returned
}

func TestKnownGapSkipsOnAViolation(t *testing.T) {
	t.Parallel()
	recorder, returned := markedWith(Violatedf("nothing was told"))
	if want := "known gap GAP-X: nothing was told"; recorder.skipped != want || recorder.failed != "" || returned != nil {
		t.Fatalf("skipped %q, failed %q, returned %v, want a skip reading %q", recorder.skipped, recorder.failed, returned, want)
	}
}

func TestKnownGapFailsWhenTheGapIsFixed(t *testing.T) {
	t.Parallel()
	recorder, returned := markedWith(nil)
	if !strings.Contains(recorder.failed, "GAP-X is fixed: remove the marker") || recorder.skipped != "" || returned != nil {
		t.Fatalf("skipped %q, failed %q, returned %v, want a failure naming the marker", recorder.skipped, recorder.failed, returned)
	}
}

func TestKnownGapReturnsAHarnessError(t *testing.T) {
	t.Parallel()
	fault := errors.New("the store is gone")
	recorder, returned := markedWith(fault)
	if recorder.skipped != "" || recorder.failed != "" || !errors.Is(returned, fault) {
		t.Fatalf("skipped %q, failed %q, returned %v, want the harness error unchanged", recorder.skipped, recorder.failed, returned)
	}
}
