//go:build chaos

package chaos

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/claudecode"
	"ropeshift.com/ropeshift/internal/adapters/tmux"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

// detachKeys is tmux's binding, which a person types to leave a pane they attached to.
const detachKeys = "\x02d"

// plannerRun is a whole chaos daemon with a repository to plan in, on a real private tmux server, and a
// person's terminal to attach with.
type plannerRun struct {
	*paneRun
	terminal *tmux.Terminal
	cli      string
	member   string
}

func newPlannerRun(t *testing.T) *plannerRun {
	t.Helper()
	binary, err := chaosDaemon()
	if err != nil {
		t.Fatal(err)
	}
	w := newWorld(t, "")
	repo := gittest.NewIn(t, w.target.Dir)
	repo.Commit(".ropeshift/profile.toml", paneProfile)
	run := &paneRun{
		t: t, w: w, binary: binary, path: doubles.InstallFakeClaude(t, "pane-survives"), repo: repo.Dir,
		paths: api.Paths{StateDir: filepath.Join(w.target.Dir, "state"), RunDir: filepath.Join(apitest.ShortDir(t), "run")},
	}
	run.client = api.NewUnixClient(run.paths.ControlSocket()).Project(ledger.DefaultProjectName)
	t.Cleanup(run.client.Close)
	terminal := doubles.TmuxOn(t, run.paths.TerminalSocket())
	// The daemon is the same binary under another name, and the command line is what a renamed binary acts as.
	real, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	return &plannerRun{paneRun: run, terminal: terminal, cli: real}
}

// signIn gives the person a session, which is what a shell holds after `ropeshift login`.
func (p *plannerRun) signIn(t *testing.T) {
	t.Helper()
	token, err := apitest.SignIn(p.w.rig.Ctx, p.client)
	if err != nil {
		t.Fatal(err)
	}
	p.member = token
}

// person is a command run on a terminal of its own, as a person runs it in a shell.
type person struct {
	t       *testing.T
	screen  *doubles.PTY
	command *exec.Cmd
	exited  chan error
}

// run starts `ropeshift args...` with the person's terminal as its stdin, stdout, and stderr.
func (p *plannerRun) run(t *testing.T, args ...string) *person {
	t.Helper()
	screen := doubles.NewPTY(t)
	cmd := exec.Command(p.cli, args...)
	cmd.Env = []string{
		"HOME=" + p.w.target.Dir, "PATH=" + p.path, "TERM=xterm-256color",
		api.EnvStateDir + "=" + p.paths.StateDir, api.EnvRunDir + "=" + p.paths.RunDir,
		authority.EnvMemberToken + "=" + p.member,
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = screen.Terminal, screen.Terminal, screen.Terminal
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
		}
	})
	return &person{t: t, screen: screen, command: cmd, exited: exited}
}

// detaches presses the detach keys and says the command ended cleanly, which a person who detaches
// from a session that goes on expects.
// It presses them again every two seconds until the command ends, because a tmux client that has
// just drawn its first frame can miss keys typed at once; a press after the command ended goes
// nowhere.
func (p *person) detaches() error {
	deadline := time.After(20 * time.Second)
	again := time.NewTicker(2 * time.Second)
	defer again.Stop()
	p.screen.Type(p.t, detachKeys)
	for {
		select {
		case err := <-p.exited:
			if err != nil {
				return fmt.Errorf("the command ended badly after the detach: %v: %q", err, p.screen.Drawn())
			}
			return nil
		case <-again.C:
			p.screen.Type(p.t, detachKeys)
		case <-deadline:
			return fmt.Errorf("the command did not end after the detach keys: %q", p.screen.Drawn())
		}
	}
}

// sees waits until the pane's own screen is on the person's terminal.
func (p *person) sees(text string) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-p.exited:
			return fmt.Errorf("the command ended before %q was drawn: %v: %q", text, err, p.screen.Drawn())
		default:
		}
		if strings.Contains(p.screen.Drawn(), text) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("%q was never drawn: %q", text, p.screen.Drawn())
}

// theLaneRuns says the planning lane is where a person left it: not ended, in its first generation, and in
// the pane the worker opened.
func (p *plannerRun) theLaneRuns(ctx context.Context, lane string) error {
	view, err := p.client.Lane(ctx, lane)
	if err != nil {
		return err
	}
	if view.ActualState == ledger.StateEnded || view.Generation != 1 {
		return fmt.Errorf("the lane reads %s in generation %d", view.ActualState, view.Generation)
	}
	pane, err := p.terminal.Pane(ctx, claudecode.PaneName(lane, 1))
	if err != nil {
		return fmt.Errorf("the lane's pane: %w", err)
	}
	if pane.Dead {
		return fmt.Errorf("the lane's pane died: %+v", pane)
	}
	return nil
}

// markerFor reads the file the worker touches while a person is attached to the lane's pane, which a
// planning lane's hook reads to leave the question tool to the terminal.
func (p *plannerRun) markerFor(lane string) (attached bool) {
	info, err := os.Stat(filepath.Join(p.paths.StateDir, "lanes", lane, "attached-1"))
	return err == nil && lanes.Attached(info.ModTime(), time.Now())
}

// policyOf reads the policy file the lane's hook reads.
func (p *plannerRun) policyOf(lane string) (lanes.DenyPolicy, error) {
	var policy lanes.DenyPolicy
	raw, err := os.ReadFile(filepath.Join(p.paths.StateDir, "lanes", lane, "policy-1.json"))
	if err != nil {
		return policy, err
	}
	return policy, json.Unmarshal(raw, &policy)
}

// marked waits until the marker says whether a person is attached.
func (p *plannerRun) marked(lane string, attached bool) error {
	what := "no marker"
	if attached {
		what = "a fresh marker"
	}
	return until(what, func() (bool, error) { return p.markerFor(lane) == attached, nil })
}

// A planning session is the person's conversation, so it must go on when they leave it and when the
// worker that runs it dies (FR-SESSION-6): `plan start` attaches, a detach leaves the lane running,
// `lane attach` joins the same pane again, and a daemon killed outright and started again re-adopts the
// pane, which a new `lane attach` still reaches.
func TestAPlanningSessionSurvivesADetachAndAWorkerRestart(t *testing.T) {
	doubles.RequireTmux(t)
	p := newPlannerRun(t)
	ctx := p.w.rig.Ctx
	if err := p.daemon(""); err != nil {
		t.Fatal(err)
	}
	if err := until("the worker", func() (bool, error) {
		workers, err := p.client.Workers(ctx)
		return err == nil && len(workers) == 1 && workers[0].Epoch >= 1, nil
	}); err != nil {
		t.Fatal(err)
	}
	p.signIn(t)

	started := p.run(t, "plan", "start", "--goal", "Build a billing portal")
	if err := started.sees("fakeclaude"); err != nil {
		t.Fatalf("%v: %s", err, p.stderr.String())
	}
	views, err := p.client.Lanes(ctx)
	if err != nil || len(views) != 1 {
		t.Fatalf("lanes %v: %v", views, err)
	}
	lane := views[0].ID
	// The worker told the driver this lane is a planner, so its hook leaves the question tool to the
	// terminal while a person is attached.
	if policy, err := p.policyOf(lane); err != nil || policy.QuestionTool != lanes.QuestionToolTerminalIfAttached {
		t.Fatalf("the planning lane's policy reads %q: %v", policy.QuestionTool, err)
	}
	if err := p.marked(lane, true); err != nil {
		t.Fatalf("while a person was attached: %v", err)
	}
	if err := started.detaches(); err != nil {
		t.Fatal(err)
	}
	if err := p.marked(lane, false); err != nil {
		t.Fatalf("after the person detached: %v", err)
	}
	if err := p.theLaneRuns(ctx, lane); err != nil {
		t.Fatalf("after the first detach: %v", err)
	}

	joined := p.run(t, "lane", "attach", lane)
	if err := joined.sees("fakeclaude"); err != nil {
		t.Fatal(err)
	}
	if err := p.marked(lane, true); err != nil {
		t.Fatalf("while a person was attached again: %v", err)
	}
	if err := joined.detaches(); err != nil {
		t.Fatal(err)
	}
	if err := p.theLaneRuns(ctx, lane); err != nil {
		t.Fatalf("after the second detach: %v", err)
	}

	if err := p.process.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	<-p.exited
	if err := p.restart(); err != nil {
		t.Fatal(err)
	}
	if err := p.theLaneRuns(ctx, lane); err != nil {
		t.Fatalf("after the worker restarted: %v", err)
	}

	again := p.run(t, "lane", "attach", lane)
	if err := again.sees("fakeclaude"); err != nil {
		t.Fatalf("after the worker restarted: %v", err)
	}
	// The new worker adopted the pane, so it tells the hook who is attached, as the one it replaced did.
	if err := p.marked(lane, true); err != nil {
		t.Fatalf("after the worker restarted: %v", err)
	}
	if err := again.detaches(); err != nil {
		t.Fatal(err)
	}
	if err := p.theLaneRuns(ctx, lane); err != nil {
		t.Fatalf("after the third detach: %v", err)
	}
	if err := p.release(); err != nil {
		t.Fatal(err)
	}
}
