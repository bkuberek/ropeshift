package delivery

import (
	"math/rand/v2"
	"testing"
)

// TestEveryTrialKeepsOrderAndTheCap runs generated plans through one trial each and checks the schedule it
// gives: no work order starts before every work order it depends on ended, each then takes at least its own
// time, work left out ends just as its dependencies do, and no more lanes are held at once than the cap.
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
