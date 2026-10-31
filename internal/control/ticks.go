package control

import (
	"context"
	"sync"
	"time"
)

// Tick is one deterministic loop of the control plane. A tick holds no state of its
// own, so a restart loses nothing and a missed run is made up by the next one.
type Tick struct {
	Name  string
	Every time.Duration
	Run   func(context.Context) error
}

// RunTicks runs each tick on its own schedule until the context ends. A failed run
// is reported and the tick goes on, because a database that went away can return.
func RunTicks(ctx context.Context, ticks []Tick, report func(name string, err error)) {
	var wg sync.WaitGroup
	for _, tick := range ticks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			timer := time.NewTicker(tick.Every)
			defer timer.Stop()
			for {
				if err := tick.Run(ctx); err != nil && ctx.Err() == nil {
					report(tick.Name, err)
				}
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
			}
		}()
	}
	wg.Wait()
}
