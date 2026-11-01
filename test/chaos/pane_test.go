//go:build chaos

package chaos

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/claudecode"
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/apitest"
	"ropeshift.dev/ropeshift/test/contract"
	"ropeshift.dev/ropeshift/test/doubles"
	"ropeshift.dev/ropeshift/test/gittest"
)

// paneOp is the prefix of a checked start's steps.
const paneOp = "pane.start"

// paneStep is one step of a checked start, and what a kill there must still reach.
type paneStep struct {
	name string
	// noPane says the kill comes before a pane exists.
	noPane bool
	// noBrief says the kill comes before the brief was written, so nothing can tell a
	// new worker whether it arrived. It is never sent blind: only a grace with no evidence
	// retries it, as a new message.
	noBrief bool
}

var paneSteps = []paneStep{
	{name: "intent", noPane: true},
	{name: "opened"},
	{name: "checked"},
	{name: "briefing", noBrief: true},
	{name: "briefed"},
}

// paneProfile runs a lane in a pane, with a short check, so a broken start ends quickly.
const paneProfile = "run_mode = \"tmux\"\n[lifecycle]\nready_timeout = \"4s\"\nbrief_grace = \"2s\"\n"

const paneFlagWait = 30 * time.Second

var chaosDaemon = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("/tmp", "rspane")
	if err != nil {
		return "", err
	}
	binary := filepath.Join(dir, "ropeshift")
	build := exec.Command("go", "build", "-tags", "chaos", "-o", binary, "ropeshift.dev/ropeshift/cmd/ropeshift")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build: %v\n%s", err, out)
	}
	if err := os.Symlink(binary, filepath.Join(dir, "ropeshiftd")); err != nil {
		return "", err
	}
	previous := afterTests
	afterTests = func() error {
		_ = os.RemoveAll(dir)
		return previous()
	}
	return filepath.Join(dir, "ropeshiftd"), nil
})

// paneRun is a whole chaos daemon on a real private tmux server, with fakeclaude in a pane.
type paneRun struct {
	t       *testing.T
	w       *world
	binary  string
	path    string
	paths   api.Paths
	repo    string
	extra   []string
	client  *api.Client
	exited  chan error
	process *exec.Cmd
	stderr  bytes.Buffer
}

func newPaneRun(t *testing.T, fixture string, extra ...string) (*paneRun, error) {
	t.Helper()
	binary, err := chaosDaemon()
	if err != nil {
		return nil, err
	}
	w := newWorld(t, "")
	repo := gittest.NewIn(t, w.target.Dir)
	repo.Commit(".ropeshift/profile.toml", paneProfile)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	r := &paneRun{
		t: t, w: w, binary: binary, path: doubles.InstallFakeClaude(t, fixture), repo: repo.Dir, extra: extra,
		// A session's socket has a 104-byte limit, and a run directory leaves it room.
		paths: api.Paths{StateDir: filepath.Join(w.target.Dir, "state"), RunDir: filepath.Join(apitest.ShortDir(t), "run")},
	}
	r.client = api.NewUnixClient(r.paths.ControlSocket())
	t.Cleanup(r.client.Close)
	doubles.TmuxOn(t, r.paths.TerminalSocket())
	return r, nil
}

// daemon starts one process. A name in kill ends it at that step.
func (r *paneRun) daemon(kill string) error {
	cmd := exec.Command(r.binary, "solo", "--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", r.repo)
	cmd.Env = append([]string{
		"HOME=" + r.w.target.Dir, "PATH=" + r.path,
		api.EnvStateDir + "=" + r.paths.StateDir, api.EnvRunDir + "=" + r.paths.RunDir,
	}, r.extra...)
	if kill != "" {
		cmd.Env = append(cmd.Env, crashpoint.EnvKill+"="+kill)
	}
	cmd.Stderr = &r.stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	r.process, r.exited = cmd, exited
	r.t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
		}
	})
	return nil
}

// adoptionSettle is longer than a worker's first pass over what it finds.
const adoptionSettle = time.Second

// restart starts a new daemon and holds the session's turn until that daemon has adopted
// it, so whatever a broken adoption writes lands while the session can still take it.
func (r *paneRun) restart() error {
	ctx := r.w.rig.Ctx
	var before int64
	if workers, err := r.client.Workers(ctx); err == nil && len(workers) == 1 {
		before = workers[0].Epoch
	}
	if err := r.daemon(""); err != nil {
		return err
	}
	err := until("the new daemon's epoch", func() (bool, error) {
		workers, err := r.client.Workers(ctx)
		return err == nil && len(workers) == 1 && workers[0].Epoch > before, nil
	})
	if err != nil {
		return err
	}
	time.Sleep(adoptionSettle)
	return nil
}

func (r *paneRun) dispatch() (string, error) {
	ctx := r.w.rig.Ctx
	var id string
	err := until("the work order", func() (bool, error) {
		list, err := r.client.WorkOrders(ctx, "")
		if err != nil || len(list) == 0 {
			return false, nil
		}
		id = list[0].ID
		return true, nil
	})
	if err != nil {
		return "", err
	}
	if _, err := r.client.Review(ctx, id); err != nil {
		return "", err
	}
	var worker string
	err = until("the worker", func() (bool, error) {
		workers, err := r.client.Workers(ctx)
		if err != nil || len(workers) != 1 || workers[0].Epoch < 1 {
			return false, nil
		}
		worker = workers[0].ID
		return true, nil
	})
	if err != nil {
		return "", err
	}
	if _, err := r.client.Claim(ctx, id, api.ClaimRequest{ClaimID: "c1", WorkerID: worker}); err != nil {
		return "", err
	}
	_, err = r.client.StartLane(ctx, id, api.StartLane{LaneID: "lane-1"})
	return id, err
}

func (r *paneRun) log(name string) []string {
	raw, err := os.ReadFile(filepath.Join(doubles.BinDir(r.path), name+".log"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func (r *paneRun) release() error {
	return os.WriteFile(filepath.Join(doubles.BinDir(r.path), "release"), nil, 0o600)
}

func (r *paneRun) state(id string) workorders.State {
	row, err := r.client.WorkOrder(r.w.rig.Ctx, id)
	if err != nil {
		return ""
	}
	return row.State
}

func settled(state workorders.State) bool {
	return state == workorders.Acceptance || state == workorders.Blocked || state == workorders.Failed
}

func (r *paneRun) sessions() int {
	count := 0
	seen, err := filepath.Glob(filepath.Join(doubles.BinDir(r.path), doubles.SeenDir, "*.json"))
	if err != nil {
		return 0
	}
	for _, file := range seen {
		raw, err := os.ReadFile(file)
		if err == nil && !strings.Contains(string(raw), `"args":["agents"`) {
			count++
		}
	}
	return count
}

// atMostOnce holds the three promises of a checked start, however a crash falls.
func (r *paneRun) atMostOnce() error {
	if r.log("bare-enter") != nil {
		return contract.Violatedf("a bare Enter reached the trust dialog")
	}
	if messages := r.log("messages"); len(messages) > 1 {
		return contract.Violatedf("the brief arrived %d times", len(messages))
	}
	if sessions := r.sessions(); sessions > 1 {
		return contract.Violatedf("%d sessions started for one generation", sessions)
	}
	return nil
}

// runPaneCrash kills a daemon at one step, starts another, and reads what happened.
func runPaneCrash(r *paneRun, step paneStep) error {
	if err := r.daemon(paneOp + "/" + step.name); err != nil {
		return err
	}
	id, err := r.dispatch()
	if err != nil {
		return err
	}
	killed := false
	err = until("the kill, or the lane's end", func() (bool, error) {
		select {
		case <-r.exited:
			killed = true
			return true, nil
		default:
		}
		return settled(r.state(id)), nil
	})
	if err != nil {
		return err
	}
	if err := r.atMostOnce(); err != nil {
		return err
	}
	if !killed {
		return fmt.Errorf("the daemon did not reach %s: the work order reads %s: %s", step.name, r.state(id), r.stderr.String())
	}
	if !step.noPane {
		// A pane outlives the kill, and its process may still be starting.
		if err := until("the pane's session, with its worker dead", func() (bool, error) { return r.sessions() != 0, nil }); err != nil {
			return contract.Violatedf("no session after a kill at %s", step.name)
		}
	}
	if err := r.restart(); err != nil {
		return err
	}
	if err := r.release(); err != nil {
		return err
	}
	if step.noBrief {
		time.Sleep(time.Second)
		if messages := r.log("messages"); len(messages) != 0 {
			return contract.Violatedf("a brief that may have arrived was sent again before its grace")
		}
	}
	if err := until("the lane's outcome", func() (bool, error) { return settled(r.state(id)), nil }); err != nil {
		return fmt.Errorf("%w: %s", err, r.stderr.String())
	}
	if err := r.atMostOnce(); err != nil {
		return err
	}
	if state := r.state(id); state != workorders.Acceptance {
		return contract.Violatedf("the lane ended in %s after a kill at %s", state, step.name)
	}
	return nil
}

func TestCrashPaneStart(t *testing.T) {
	doubles.RequireTmux(t)
	for _, step := range paneSteps {
		t.Run(step.name, func(t *testing.T) {
			r, err := newPaneRun(t, "pane-reports")
			if err != nil {
				t.Fatal(err)
			}
			if err := runPaneCrash(r, step); err != nil {
				var violation *contract.Violation
				if errors.As(err, &violation) {
					t.Fatalf("violation: %v", err)
				}
				t.Fatal(err)
			}
		})
	}
}

// checkPaneVariant runs one broken driver through the crash that exposes it.
func checkPaneVariant(t *testing.T, sabotage, stepName string) error {
	doubles.RequireTmux(t)
	r, err := newPaneRun(t, "pane-reports", claudecode.EnvSabotage+"="+sabotage)
	if err != nil {
		return err
	}
	for _, step := range paneSteps {
		if step.name == stepName {
			return runPaneCrash(r, step)
		}
	}
	return fmt.Errorf("no step %s", stepName)
}

// TestPaneRandomKills adds what named steps can miss: a daemon that dies by SIGKILL at any
// moment leaves its pane alone, and a new one reaches the same end with one session.
func TestPaneRandomKills(t *testing.T) {
	doubles.RequireTmux(t)
	for attempt := range 6 {
		t.Run(fmt.Sprintf("kill-%d", attempt), func(t *testing.T) {
			r, err := newPaneRun(t, "pane-reports")
			if err != nil {
				t.Fatal(err)
			}
			if err := r.daemon(""); err != nil {
				t.Fatal(err)
			}
			id, err := r.dispatch()
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Duration(rand.Int64N(int64(900 * time.Millisecond))))
			if err := r.process.Process.Signal(syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
			<-r.exited
			if err := r.atMostOnce(); err != nil {
				t.Fatal(err)
			}
			if err := r.daemon(""); err != nil {
				t.Fatal(err)
			}
			if err := until("the new daemon", func() (bool, error) {
				workers, err := r.client.Workers(context.Background())
				return err == nil && len(workers) == 1, nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := r.release(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(paneFlagWait)
			for time.Now().Before(deadline) && !settled(r.state(id)) {
				time.Sleep(20 * time.Millisecond)
			}
			if err := r.atMostOnce(); err != nil {
				t.Fatal(err)
			}
			t.Logf("state %s, sessions %d, messages %d", r.state(id), r.sessions(), len(r.log("messages")))
		})
	}
}

// restartOp is the prefix of a crash restart's steps.
const restartOp = "worker.restart"

var restartSteps = []string{"charged", "next"}

// restartProfile restarts a crashed session at once, so a kill falls inside the restart.
const restartProfile = "[lifecycle]\nrestart_backoff = \"10ms\"\n"

// runRestartCrash kills a daemon inside a crash restart, starts another, and reads the
// breaker's charges from the journal. One crash costs one charge, however a kill falls.
func runRestartCrash(t *testing.T, step string, extra ...string) error {
	t.Helper()
	r, err := newRestartRun(t, extra...)
	if err != nil {
		return err
	}
	if err := r.daemon(restartOp + "/" + step); err != nil {
		return err
	}
	id, err := r.dispatch()
	if err != nil {
		return err
	}
	if err := until("the kill", func() (bool, error) {
		select {
		case <-r.exited:
			return true, nil
		default:
			return settled(r.state(id)), nil
		}
	}); err != nil {
		return err
	}
	if err := r.daemon(""); err != nil {
		return err
	}
	if err := until("the lane's outcome", func() (bool, error) { return settled(r.state(id)), nil }); err != nil {
		return fmt.Errorf("%w: %s", err, r.stderr.String())
	}
	if state := r.state(id); state != workorders.Acceptance {
		return contract.Violatedf("the lane ended in %s after a kill at %s: %s", state, step, r.stderr.String())
	}
	journal, err := sqlite.OpenJournal(filepath.Join(r.paths.StateDir, api.JournalFileName))
	if err != nil {
		return err
	}
	defer journal.Close()
	charges, err := journal.Restarts(r.w.rig.Ctx, "lane-1")
	if err != nil {
		return err
	}
	if len(charges) != 1 {
		return contract.Violatedf("one crash cost %d charges", len(charges))
	}
	if sessions := r.sessions(); sessions != 2 {
		return contract.Violatedf("%d sessions, want the first and its resume", sessions)
	}
	return nil
}

func newRestartRun(t *testing.T, extra ...string) (*paneRun, error) {
	t.Helper()
	binary, err := chaosDaemon()
	if err != nil {
		return nil, err
	}
	w := newWorld(t, "")
	repo := gittest.NewIn(t, w.target.Dir)
	repo.Commit(".ropeshift/profile.toml", restartProfile)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	r := &paneRun{
		t: t, w: w, binary: binary, path: doubles.InstallFakeClaude(t, "crash-then-report"), repo: repo.Dir, extra: extra,
		paths: api.Paths{StateDir: filepath.Join(w.target.Dir, "state"), RunDir: filepath.Join(apitest.ShortDir(t), "run")},
	}
	r.client = api.NewUnixClient(r.paths.ControlSocket())
	t.Cleanup(r.client.Close)
	doubles.ReapShims(t, filepath.Join(r.paths.StateDir, "lanes", "lane-1"))
	return r, nil
}

func TestCrashRestartChargesOnce(t *testing.T) {
	for _, step := range restartSteps {
		t.Run(step, func(t *testing.T) {
			if err := runRestartCrash(t, step); err != nil {
				var violation *contract.Violation
				if errors.As(err, &violation) {
					t.Fatalf("violation: %v", err)
				}
				t.Fatal(err)
			}
		})
	}
}
