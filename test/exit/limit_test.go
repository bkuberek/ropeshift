package exit

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/profiles"
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/api/shimlink"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/parking"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
	"ropeshift.dev/ropeshift/test/doubles"
	"ropeshift.dev/ropeshift/test/gittest"
)

// limitProfile gives a lane no room to wait on anything, and a limit with no known reset a short
// first wait.
const limitProfile = "[lifecycle]\nhandoff_timeout = \"10s\"\nmax_restarts = 0\nusage_backoff = \"400ms\"\n"

// setUserProfile writes the layer only a machine's owner sets.
func (in *install) setUserProfile(t *testing.T, text string) {
	t.Helper()
	dir := in.paths().StateDir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, profiles.UserFile), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fleet starts count lanes, one work order each, in order, so the last to start is the one a
// fixture has reach the limit, with every other lane already at work.
func (in *install) fleet(t *testing.T, fixture string, count int) (*daemon, []string) {
	t.Helper()
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", limitProfile)
	for i := 1; i <= count; i++ {
		name := fmt.Sprintf("work%d", i)
		repo.Commit(name+".md", contract.ContractText(name, name, "Add step "+name+"."))
	}
	in.path = doubles.InstallFakeClaude(t, fixture)
	in.setUserProfile(t, "[admission]\nmax_lanes = 0\nmax_lanes_per_sign_in = 0\n")
	running := in.startWorker(repo.Dir)
	orders := in.workOrders(count)
	in.must("fleet", "caps", "--wip", "0", "--per-repository", "0", "--per-worker", "0")
	var ids []string
	for i := 1; i <= count; i++ {
		id := orders[fmt.Sprintf("The work order of work%d", i)]
		lane := fmt.Sprintf("lane-%d", i)
		in.laneOn(t, id, fmt.Sprintf("c%d", i), lane)
		if i != count {
			in.waitFor("lane "+lane+" at work", in.laneReads(lane, "working"))
		}
		ids = append(ids, id)
	}
	return running, ids
}

func (in *install) allRead(ids []string, state workorders.State) func() bool {
	return func() bool {
		return !slices.ContainsFunc(ids, func(id string) bool { return in.show(id).State != state })
	}
}

// spans reads the scripted sessions' own record of when each was at work, and says how many ran
// at once at the worst moment.
func spans(t *testing.T, dir string) (count, most int) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "spans.log"))
	if err != nil {
		t.Fatal(err)
	}
	type mark struct {
		at    int64
		delta int
	}
	var marks []mark
	for line := range strings.Lines(string(raw)) {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			t.Fatalf("a line of %q", line)
		}
		at, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		delta := 1
		if fields[1] == "end" {
			delta = -1
		} else {
			count++
		}
		marks = append(marks, mark{at, delta})
	}
	slices.SortStableFunc(marks, func(a, b mark) int {
		if a.at != b.at {
			return int(a.at - b.at)
		}
		return a.delta - b.delta
	})
	running := 0
	for _, m := range marks {
		running += m.delta
		most = max(most, running)
	}
	return count, most
}

// TestUsageLimitParksAndResumes is the usage half of E4 on the real binary, driver, and shim. One
// lane reads a limit that names its reset, all six park with no process, and the reset wakes them,
// no more than a sign-in's cap at a time.
func TestUsageLimitParksAndResumes(t *testing.T) {
	in := newInstall(t)
	running, ids := in.fleet(t, "limit-fleet", 6)
	in.waitFor("every lane to park", in.allRead(ids, workorders.Parked))
	in.noneRunning(t, 6)
	in.setUserProfile(t, "[admission]\nmax_lanes = 0\nmax_lanes_per_sign_in = 2\n")
	in.waitFor("every lane's outcome", in.allRead(ids, workorders.Acceptance))
	running.kill()
	count, most := spans(t, doubles.BinDir(in.path))
	if count != 6 {
		t.Errorf("%d lanes came back", count)
	}
	if most > 2 {
		t.Errorf("%d lanes ran at once under a cap of 2", most)
	}
	resumed := 0
	for _, session := range in.sessionsOf(t) {
		if slices.Contains(session, "--resume") {
			resumed++
		}
	}
	if resumed != 6 {
		t.Errorf("%d lanes went on in their own sessions", resumed)
	}
}

// noneRunning holds parked lanes to what parking promises: no process, and no quota in use.
func (in *install) noneRunning(t *testing.T, count int) {
	t.Helper()
	for i := 1; i <= count; i++ {
		state, err := shimlink.ReadState(filepath.Join(in.paths().StateDir, "lanes", fmt.Sprintf("lane-%d", i), "shim-1"))
		if err != nil {
			t.Fatal(err)
		}
		in.waitFor("a parked session's end", func() bool { return !exists(state.ChildPID) && !exists(state.ShimPID) })
	}
}

// TestALimitWithNoResetProbesAndBacksOff is the path with no time: `/usage` is read once, one lane
// goes back to find out and finds the limit still there, and when it gets through, one signal wakes
// the five lanes that had nothing else to wake them.
func TestALimitWithNoResetProbesAndBacksOff(t *testing.T) {
	in := newInstall(t)
	running, ids := in.fleet(t, "limit-fleet-unknown", 6)
	in.waitFor("every lane's outcome", in.allRead(ids, workorders.Acceptance))
	view, ok := in.laneView("lane-6")
	running.kill()
	if !ok || len(view.Generations) != 3 {
		t.Errorf("the probe took generations %+v", view.Generations)
	}
	for _, order := range ids[:5] {
		if parked := in.parkings(t, order); parked != 1 {
			t.Errorf("work order %s parked %d times", order, parked)
		}
	}
	raw, err := os.ReadFile(filepath.Join(doubles.BinDir(in.path), "usage.log"))
	if err != nil {
		t.Fatal(err)
	}
	if reads := strings.Count(string(raw), "/usage"); reads != 1 {
		t.Errorf("/usage read %d times", reads)
	}
	if signals := in.signals(t); signals != 1 {
		t.Errorf("%d signals woke the fleet", signals)
	}
}

// parkings counts what parked one work order, in a stopped daemon's ledger.
func (in *install) parkings(t *testing.T, id string) int {
	t.Helper()
	count := 0
	for _, event := range in.events(t, id) {
		if event.Type == workorders.EventType(workorders.TriggerLaneParked) {
			count++
		}
	}
	return count
}

// signals counts what woke lanes on a sign-in, in a stopped daemon's ledger.
func (in *install) signals(t *testing.T) int {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(in.paths().StateDir, api.LedgerFileName), sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	count := 0
	err = store.Read(t.Context(), "default", func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(tx.Events)
		for _, event := range events {
			if event.Type == parking.EventSignal {
				count++
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

// TestAnExpiredSignInParksUntilItIsBack needs no narrative, because a lane with no sign-in cannot
// write one, and only the doctor's word ends it.
func TestAnExpiredSignInParksUntilItIsBack(t *testing.T) {
	in := newInstall(t)
	running, ids := in.fleet(t, "signin-fleet", 2)
	mark := filepath.Join(doubles.BinDir(in.path), "signed-out")
	if err := os.WriteFile(mark, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	in.waitFor("every lane to park", in.allRead(ids, workorders.Parked))
	if err := os.Remove(mark); err != nil {
		t.Fatal(err)
	}
	in.waitFor("every lane's outcome", in.allRead(ids, workorders.Acceptance))
	running.kill()
}
