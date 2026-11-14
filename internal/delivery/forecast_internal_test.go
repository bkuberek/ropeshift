package delivery

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// TestTheDrawsArePinned holds the forecast's source of chance to SplitMix64 as published, and to the state the
// seed gives it, values computed apart from this package, so no change to it can pass unseen.
func TestTheDrawsArePinned(t *testing.T) {
	zero := &draws{}
	if got := []uint64{zero.next(), zero.next(), zero.next()}; !slices.Equal(got, []uint64{0xe220a8397b1dcdaf, 0x6e789e6aa1b965f4, 0x06c45d188009454f}) {
		t.Errorf("SplitMix64 from zero gives %#x", got)
	}
	seed := ForecastSeed("sha256:plan", 40)
	if seed != "sha256:da7843d58468bffc6aacaac77c6745e9f3a7ef642981051d9e20eb300b321f45" {
		t.Errorf("the seed is %s", seed)
	}
	d := drawsFrom(seed)
	if d.state != 0x7e5d938b0b741a34 {
		t.Errorf("the seed starts the draws at %#x", d.state)
	}
	if got := []uint64{d.next(), d.next(), d.next()}; !slices.Equal(got, []uint64{3146701580490199299, 1268103367480735842, 11121369043093970801}) {
		t.Errorf("the seed draws %v", got)
	}
	again := drawsFrom(seed)
	if got := []int{again.index(7), again.index(7), again.index(7)}; !slices.Equal(got, []int{1, 0, 0}) {
		t.Errorf("one of seven draws %v", got)
	}
}

// TestEveryTrialKeepsOrderAndTheCap runs generated plans through one trial each and checks the schedule it
// gives: no work order starts before every work order it depends on ended, each then takes at least its own
// time, work left out ends just as its dependencies do, and no more lanes are held at once than the cap, where a
// cap of zero caps nothing.
func TestEveryTrialKeepsOrderAndTheCap(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261114, 9))
	for run := range 3000 {
		n := 1 + rng.IntN(12)
		jobs, deps := make([]job, n), make([][]int, n)
		for i := range jobs {
			if rng.IntN(6) == 0 {
				jobs[i] = job{leftOut: true}
			} else {
				total := int64(rng.IntN(10))
				jobs[i] = job{total: total, lane: int64(rng.IntN(int(total) + 1))}
			}
			// A work order depends only on earlier ones, so the graph has no cycle.
			for j := range i {
				if rng.IntN(4) == 0 {
					deps[i] = append(deps[i], j)
				}
			}
		}
		// Zero lanes caps nothing.
		lanes := int64(rng.IntN(4))
		end := simulate(jobs, deps, rng.Perm(n), lanes)
		for i, j := range jobs {
			var ready int64
			for _, d := range deps[i] {
				ready = max(ready, end[d])
			}
			if end[i] < ready+j.total || (j.leftOut && end[i] != ready) {
				t.Fatalf("run %d: work %d ends at %d, though it is %+v and its dependencies end by %d (%+v, %v)", run, i, end[i], j, ready, jobs, end)
			}
		}
		if lanes == 0 {
			continue
		}
		for i, j := range jobs {
			if j.leftOut || j.lane == 0 {
				continue
			}
			at := end[i] - j.total
			held := int64(0)
			for k, other := range jobs {
				if other.leftOut || other.lane == 0 {
					continue
				}
				if from := end[k] - other.total; from <= at && at < from+other.lane {
					held++
				}
			}
			if held > lanes {
				t.Fatalf("run %d: %d lanes held at %d under a cap of %d (%+v, %v)", run, held, at, lanes, jobs, end)
			}
		}
	}
}
