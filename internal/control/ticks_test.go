package control

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunTicksRunsAtOnceAndSurvivesAFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var runs, reports atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunTicks(ctx, []Tick{{
			Name:  "flaky",
			Every: time.Millisecond,
			Run: func(context.Context) error {
				if runs.Add(1) == 3 {
					cancel()
				}
				return errors.New("the database went away")
			},
		}}, func(name string, err error) {
			if name != "flaky" || err == nil {
				t.Errorf("reported %s: %v", name, err)
			}
			reports.Add(1)
		})
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the tick did not stop")
	}
	if runs.Load() < 3 || reports.Load() < 2 {
		t.Errorf("%d runs and %d reports", runs.Load(), reports.Load())
	}
}
