// Command firstrun is a new user's first work order, which the exit tests run inside a Linux container
// with no network: TestExitE1FirstWorkOrder on a release installed by install.sh, and
// TestNoOutboundConnections on a build of this tree under strace. It does only what a person does,
// with every setting at its default: ropeshift init on a terminal, a repository holding the sample
// contract init wrote, ropeshiftd solo with that repository as its source, and the CLI from review to
// acceptance. Every program comes from PATH. It writes what it saw to result.json in its output
// directory, stops at the first step that fails, and exits 1 when one did.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/shimlink"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/exit/ptypair"
	"ropeshift.com/ropeshift/test/procs"
)

// ResultFile is what the run leaves in its output directory.
const ResultFile = "result.json"

// passphrase is the one a new user types, twice to init and once to log in.
const passphrase = "a first passphrase on a new machine"

// The names the run gives what it makes.
const (
	claimID = "first-claim"
	laneID  = "first-lane"
)

// How long each step may take. A step past its limit is killed with its process group, and fails the run
// with its name and the end of its output, so a step that hangs fails as itself and not as the exit tests'
// 20-minute timeout. The limits of the whole run add up to 14m10s: twelve one-shot commands at
// commandLimit (version, five of git, login, review, worker list, claim, lane start, and accept), init,
// the five waits, the daemon's stop, and the end of its terminal server. The daemon runs at its default
// ticks, so a lane waits for the worker's next beat, and the first lane also waits for the probe of the
// agent CLI's version, which is why the wait for acceptance has the largest share.
const (
	commandLimit = 15 * time.Second
	// initLimit covers ropeshift init's own bounds: a minute for its daemon to answer, a minute for the
	// worker's registration, and 15 seconds for its daemon's stop.
	initLimit       = 3 * time.Minute
	answerLimit     = 30 * time.Second
	intakeLimit     = 2 * time.Minute
	readyLimit      = time.Minute
	acceptanceLimit = 3 * time.Minute
	doneLimit       = time.Minute
	stopLimit       = 30 * time.Second
	terminalLimit   = 10 * time.Second
	pollEvery       = 250 * time.Millisecond
	// outputGrace is how long a step that ended or was killed may hold its output open, through a process
	// it started in another group, before the run stops reading it.
	outputGrace = 5 * time.Second
)

// How much of a step's output, each stream on its own, a failure shows.
const (
	tailLines = 40
	tailBytes = 4096
)

// Step is one command the run started.
type Step struct {
	Name     string   `json:"name"`
	Args     []string `json:"args"`
	Exit     int      `json:"exit"`
	Stdout   string   `json:"stdout"`
	Stderr   string   `json:"stderr"`
	Terminal string   `json:"terminal,omitempty"`
	Millis   int64    `json:"ms"`
	// TimedOut says the step ran past its limit and was killed.
	TimedOut bool `json:"timed_out,omitempty"`
}

// Result is what the run saw.
type Result struct {
	// Interfaces are the network interfaces of this namespace that are up or hold an address, and
	// Devices are all of them.
	Interfaces []string `json:"interfaces"`
	Devices    []string `json:"devices"`
	// Docker reports a docker command or socket inside the container.
	Docker bool `json:"docker"`
	// PriorState reports an installation directory that existed before init.
	PriorState bool   `json:"prior_state"`
	Steps      []Step `json:"steps"`
	WorkOrder  string `json:"work_order"`
	// States are the work order's states, each once, in the order the run saw them.
	States []string `json:"states"`
	Final  string   `json:"final"`
	// WebStatus is the status of the web UI's page on its default loopback address.
	WebStatus int `json:"web_status"`
	// OffLoopback lists every socket of this namespace, while the daemon ran, with an address off
	// loopback.
	OffLoopback []string `json:"off_loopback"`
	// Left lists what the run had to end after the daemon stopped.
	Left      []string `json:"left"`
	CanaryRan bool     `json:"canary_ran"`
	Failure   string   `json:"failure,omitempty"`
}

type run struct {
	out    string
	env    []string
	paths  api.Paths
	result Result
	daemon *exec.Cmd
	exited chan error
	// last is the step a wait ran last, which a wait that runs out names.
	last Step
}

func main() {
	out := flag.String("out", "", "the directory result.json goes in")
	canary := flag.String("canary", "", "the directory that holds the claude canary")
	flag.Parse()
	if *out == "" || *canary == "" {
		fmt.Fprintln(os.Stderr, "usage: firstrun -out DIR -canary DIR")
		os.Exit(2)
	}
	r := &run{out: *out, env: os.Environ(), result: Result{States: []string{}, OffLoopback: []string{}, Left: []string{}}}
	if err := r.firstWorkOrder(); err != nil {
		r.result.Failure = err.Error()
	}
	r.endWhatRemains()
	if _, err := os.Stat(filepath.Join(*canary, "tripped")); err == nil {
		r.result.CanaryRan = true
		r.result.Failure = cmpOr(r.result.Failure, "the claude canary ran")
	}
	raw, err := json.MarshalIndent(r.result, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(*out, ResultFile), raw, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if r.result.Failure != "" {
		fmt.Fprintln(os.Stderr, "firstrun:", r.result.Failure)
		os.Exit(1)
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (r *run) firstWorkOrder() error {
	if err := signalsAtDefault(); err != nil {
		return err
	}
	r.result.Interfaces, r.result.Devices = interfaces()
	if _, err := exec.LookPath("docker"); err == nil {
		r.result.Docker = true
	}
	if _, err := os.Stat("/var/run/docker.sock"); err == nil {
		r.result.Docker = true
	}
	home := os.Getenv("HOME")
	paths, err := api.ResolvePaths(runtime.GOOS, os.Getenv, home)
	if err != nil {
		return err
	}
	r.paths = paths
	if _, err := os.Stat(paths.StateDir); err == nil {
		r.result.PriorState = true
	}

	if _, err := r.must(r.command("version", commandLimit, "ropeshift", "version", "--json")); err != nil {
		return err
	}
	if _, err := r.must(r.onTerminal("init", initLimit, []string{"ropeshift", "init"}, passphrase, passphrase)); err != nil {
		return err
	}
	repo := filepath.Join(home, "project")
	if err := r.newRepository(repo, filepath.Join(paths.StateDir, cli.SampleContractName)); err != nil {
		return err
	}
	if err := r.startDaemon(repo); err != nil {
		return err
	}
	if err := r.waitFor("the daemon's answer", answerLimit, func(ctx context.Context) (bool, error) {
		return r.commandIn(ctx, "health", "ropeshift", "health").Exit == 0, nil
	}); err != nil {
		return err
	}
	login, err := r.must(r.onTerminal("login", commandLimit, []string{"ropeshift", "login"}, passphrase))
	if err != nil {
		return err
	}
	token, ok := strings.CutPrefix(strings.TrimSpace(login.Stdout), "export "+authority.EnvMemberToken+"=")
	if !ok || token == "" {
		return fmt.Errorf("ropeshift login printed %q", login.Stdout)
	}
	r.env = append(r.env, authority.EnvMemberToken+"="+token)

	id, err := r.theWorkOrder()
	if err != nil {
		return err
	}
	if _, err := r.must(r.command("review", commandLimit, "ropeshift", "work", "review", id)); err != nil {
		return err
	}
	if err := r.reach(id, workorders.Ready, readyLimit); err != nil {
		return err
	}
	worker, err := r.theWorker()
	if err != nil {
		return err
	}
	if _, err := r.must(r.command("claim", commandLimit, "ropeshift", "work", "claim", id, "--worker", worker, "--claim-id", claimID)); err != nil {
		return err
	}
	if _, err := r.must(r.command("lane start", commandLimit, "ropeshift", "lane", "start", id, "--lane", laneID)); err != nil {
		return err
	}
	if err := r.reach(id, workorders.Acceptance, acceptanceLimit); err != nil {
		return err
	}
	r.result.WebStatus = webStatus(api.DefaultWebAddress)
	r.result.OffLoopback = offLoopback()
	if _, err := r.must(r.command("accept", commandLimit, "ropeshift", "work", "accept", id)); err != nil {
		return err
	}
	if err := r.reach(id, workorders.Done, doneLimit); err != nil {
		return err
	}
	return r.stopDaemon()
}

// signalsAtDefault fails when this process started with SIGINT or SIGQUIT ignored, as a command that a
// non-interactive shell starts with & does. Every program of the run would inherit that, and a stop
// the product sends by SIGINT would not reach them as it reaches a person's programs.
func signalsAtDefault() error {
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return fmt.Errorf("read this process's signal state: %w", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		mask, ok := strings.CutPrefix(line, "SigIgn:")
		if !ok {
			continue
		}
		ignored, err := strconv.ParseUint(strings.TrimSpace(mask), 16, 64)
		if err != nil {
			return fmt.Errorf("read the ignored signals %q: %w", mask, err)
		}
		for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGQUIT} {
			if ignored&(1<<(uint(sig)-1)) != 0 {
				return fmt.Errorf("the run started with %v ignored, which a person's shell never does", sig)
			}
		}
		return nil
	}
	return errors.New("this process's status names no ignored signals")
}

// newRepository is a person's own repository, with the sample contract committed to its default branch.
func (r *run) newRepository(repo, sample string) error {
	raw, err := os.ReadFile(sample)
	if err != nil {
		return fmt.Errorf("init wrote no sample contract: %w", err)
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(repo, cli.SampleContractName), raw, 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"git", "config", "--global", "user.name", "A New User"},
		{"git", "config", "--global", "user.email", "new-user@example.invalid"},
		{"git", "-C", repo, "init", "--quiet", "--initial-branch", "main"},
		{"git", "-C", repo, "add", cli.SampleContractName},
		{"git", "-C", repo, "commit", "--quiet", "--message", "Add the sample contract"},
	} {
		if _, err := r.must(r.command("git", commandLimit, args...)); err != nil {
			return err
		}
	}
	return nil
}

func (r *run) startDaemon(repo string) error {
	logFile, err := os.Create(filepath.Join(r.out, "daemon.log"))
	if err != nil {
		return err
	}
	daemon := exec.Command("ropeshiftd", "solo", "--source", repo)
	daemon.Env = r.env
	daemon.Stdout, daemon.Stderr = logFile, logFile
	r.result.Steps = append(r.result.Steps, Step{Name: "daemon", Args: daemon.Args})
	if err := daemon.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start ropeshiftd: %w", err)
	}
	r.daemon, r.exited = daemon, make(chan error, 1)
	go func() {
		r.exited <- daemon.Wait()
		_ = logFile.Close()
	}()
	return nil
}

// stopDaemon ends the daemon the way a service manager does, by its own process ID.
func (r *run) stopDaemon() error {
	if r.daemon == nil {
		return nil
	}
	_ = r.daemon.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-r.exited:
		r.daemon = nil
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			return err
		}
		if err != nil {
			return fmt.Errorf("ropeshiftd exited %d on SIGTERM", exit.ExitCode())
		}
		return nil
	case <-time.After(stopLimit):
		return fmt.Errorf("the step \"daemon stop\" did not end within its limit: ropeshiftd did not stop within %v of SIGTERM\n%s", stopLimit, r.daemonLog())
	}
}

// daemonLog is the end of what the daemon wrote, which a step that waits on the daemon shows when it fails.
func (r *run) daemonLog() string {
	raw, err := os.ReadFile(filepath.Join(r.out, "daemon.log"))
	if err != nil {
		return "the daemon's log could not be read: " + err.Error()
	}
	if text := tail(string(raw)); text != "" {
		return fmt.Sprintf("the daemon's log, at most its last %d lines:\n%s", tailLines, text)
	}
	return "the daemon's log is empty"
}

// endWhatRemains ends what the daemon leaves by design, each by the exact identity it recorded: its
// terminal server, through that server's own socket, and each shim and session a lane's state file
// names. Each one it had to end is listed.
func (r *run) endWhatRemains() {
	if r.daemon != nil {
		_ = r.daemon.Process.Signal(syscall.SIGKILL)
		err := <-r.exited
		r.result.Left = append(r.result.Left, fmt.Sprintf("the daemon, after a failed step (%v)", err))
	}
	if r.paths.StateDir == "" {
		return
	}
	if _, err := os.Stat(r.paths.TerminalSocket()); err == nil {
		if tool, err := exec.LookPath("tmux"); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), terminalLimit)
			kill := runStep(ctx, "terminal server", []string{tool, "-S", r.paths.TerminalSocket(), "kill-server"}, func(cmd *exec.Cmd) { cmd.Env = r.env })
			cancel()
			switch {
			case kill.TimedOut:
				r.result.Left = append(r.result.Left, fmt.Sprintf("the daemon's terminal server, which tmux did not end within %v", terminalLimit))
			case kill.Exit == 0:
				r.result.Left = append(r.result.Left, "the daemon's terminal server")
			}
		}
	}
	dirs, _ := filepath.Glob(filepath.Join(r.paths.StateDir, "lanes", "*", "shim-*"))
	for _, dir := range dirs {
		state, err := shimlink.ReadState(dir)
		if err != nil {
			continue
		}
		// An ID outlives its process, so each is ended only while the process holding it still names
		// this lane: the shim by its name and its directory, the session by the lane's config directory.
		for _, recorded := range []struct {
			pid     int
			markers []string
		}{{state.ChildPID, []string{filepath.Dir(dir)}}, {state.ShimPID, []string{"lane-shim", dir}}} {
			if recorded.pid <= 0 {
				continue
			}
			sent, skipped := procs.Signal(recorded.pid, syscall.SIGKILL, recorded.markers...)
			if skipped != "" {
				fmt.Fprintln(os.Stderr, "firstrun:", skipped)
			}
			if sent {
				r.result.Left = append(r.result.Left, fmt.Sprintf("process %d from %s", recorded.pid, filepath.Base(dir)))
			}
		}
	}
}

// onTerminal runs a command whose controlling terminal is a new one, for at most limit, and types each
// answer once its prompt shows, as a person does. Its stdout stays a pipe, as in `$(ropeshift login)`.
func (r *run) onTerminal(name string, limit time.Duration, args []string, answers ...string) Step {
	typed, held, err := ptypair.Open()
	if err != nil {
		return Step{Name: name, Args: args, Exit: -1, Stderr: err.Error()}
	}
	var screen bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		answer(typed, &screen, answers)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	step := runStep(ctx, name, args, func(cmd *exec.Cmd) {
		cmd.Env = r.env
		cmd.Stdin = strings.NewReader("")
		ptypair.Controlling(cmd, held)
	})
	_ = held.Close()
	_ = typed.Close()
	<-done
	step.Terminal = screen.String()
	return step
}

// answer types each answer after its prompt, a line that ends in ": ", has shown.
func answer(typed *os.File, screen *bytes.Buffer, answers []string) {
	buffer := make([]byte, 256)
	asked := 0
	for {
		n, err := typed.Read(buffer)
		screen.Write(buffer[:n])
		if err != nil {
			return
		}
		for asked < strings.Count(screen.String(), ": ") && asked < len(answers) {
			_, _ = typed.WriteString(answers[asked] + "\n")
			asked++
		}
	}
}

// command runs one program from PATH with the run's environment, for at most limit.
func (r *run) command(name string, limit time.Duration, args ...string) Step {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	return r.commandIn(ctx, name, args...)
}

// commandIn is command inside a wait, which kills it when the wait runs out, and remembers it as the step
// the wait ran last.
func (r *run) commandIn(ctx context.Context, name string, args ...string) Step {
	r.last = runStep(ctx, name, args, func(cmd *exec.Cmd) { cmd.Env = r.env })
	return r.last
}

// runStep runs one step's program until it ends or ctx does. When ctx ends first, the step is killed with
// its process group and reads as timed out, with the output it wrote until then. prepare sets what the
// step needs beyond its arguments, and a step it gives no process attributes runs in a group of its own.
func runStep(ctx context.Context, name string, args []string, prepare func(*exec.Cmd)) Step {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if prepare != nil {
		prepare(cmd)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	// A new session or a new group is led by the step's own process, so its ID names the group.
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = outputGrace
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	step := Step{Name: name, Args: args, Stdout: stdout.String(), Stderr: stderr.String(), Millis: time.Since(start).Milliseconds()}
	step.TimedOut = err != nil && ctx.Err() != nil
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		step.Exit = exit.ExitCode()
	case err != nil:
		step.Exit = -1
		step.Stderr += err.Error()
	}
	if step.TimedOut && step.Exit == 0 {
		step.Exit = -1
	}
	return step
}

// must records a step and fails the run on a non-zero exit, or on a step that ran past its limit.
func (r *run) must(step Step) (Step, error) {
	r.result.Steps = append(r.result.Steps, step)
	switch {
	case step.TimedOut:
		return step, fmt.Errorf("the step %q did not end within its limit and was killed after %d ms: %s\n%s",
			step.Name, step.Millis, strings.Join(step.Args, " "), outputTail(step))
	case step.Exit != 0:
		return step, fmt.Errorf("%s exited %d: %s", strings.Join(step.Args, " "), step.Exit, strings.TrimSpace(step.Stderr))
	}
	return step, nil
}

// outputTail is the end of each stream a step wrote, so a failure shows what the step was doing.
func outputTail(step Step) string {
	var out strings.Builder
	for _, stream := range []struct{ name, text string }{{"stdout", step.Stdout}, {"stderr", step.Stderr}, {"terminal", step.Terminal}} {
		if text := tail(stream.text); text != "" {
			fmt.Fprintf(&out, "its %s, at most its last %d lines:\n%s\n", stream.name, tailLines, text)
		}
	}
	if out.Len() == 0 {
		return "it wrote nothing"
	}
	return strings.TrimSuffix(out.String(), "\n")
}

// tail is the last tailLines lines of text, and no more than its last tailBytes bytes.
func tail(text string) string {
	text = strings.TrimRight(text, "\n")
	if len(text) > tailBytes {
		text = strings.ToValidUTF8(text[len(text)-tailBytes:], "")
	}
	lines := strings.Split(text, "\n")
	if len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	return strings.Join(lines, "\n")
}

// waitFor runs done until it holds, for at most limit. A wait that runs out names the step it ran last,
// which it kills if that step still runs, with the end of that step's output and of the daemon's log.
func (r *run) waitFor(what string, limit time.Duration, done func(context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	r.last = Step{}
	ranOut := func() error {
		return fmt.Errorf("the wait for %s did not end within its limit of %v; %s\n%s", what, limit, r.lastStep(), r.daemonLog())
	}
	for {
		ok, err := done(ctx)
		switch {
		case ok && err == nil:
			return nil
		case ctx.Err() != nil:
			return ranOut()
		case err != nil:
			return err
		}
		select {
		case <-ctx.Done():
			return ranOut()
		case <-time.After(pollEvery):
		}
	}
}

// lastStep says what a wait that ran out ran last.
func (r *run) lastStep() string {
	if r.last.Name == "" {
		return "it ran no step"
	}
	verdict := fmt.Sprintf("exited %d", r.last.Exit)
	if r.last.TimedOut {
		verdict = "was killed at the wait's limit"
	}
	return fmt.Sprintf("its last step, %q, %s after %d ms: %s\n%s", r.last.Name, verdict, r.last.Millis, strings.Join(r.last.Args, " "), outputTail(r.last))
}

// theWorkOrder waits for intake to take the sample's one work order.
func (r *run) theWorkOrder() (string, error) {
	var id string
	err := r.waitFor("intake of the sample contract", intakeLimit, func(ctx context.Context) (bool, error) {
		step := r.commandIn(ctx, "work list", "ropeshift", "work", "list", "--json")
		if step.Exit != 0 {
			return false, nil
		}
		var list api.WorkOrderList
		if err := json.Unmarshal([]byte(step.Stdout), &list); err != nil {
			return false, fmt.Errorf("work list --json: %w", err)
		}
		if len(list.WorkOrders) == 0 {
			return false, nil
		}
		if len(list.WorkOrders) != 1 {
			return false, fmt.Errorf("intake took %d work orders from one sample", len(list.WorkOrders))
		}
		r.result.Steps = append(r.result.Steps, step)
		id = list.WorkOrders[0].ID
		r.saw(string(list.WorkOrders[0].State))
		return true, nil
	})
	r.result.WorkOrder = id
	return id, err
}

func (r *run) theWorker() (string, error) {
	step, err := r.must(r.command("worker list", commandLimit, "ropeshift", "worker", "list", "--json"))
	if err != nil {
		return "", err
	}
	var list api.WorkerList
	if err := json.Unmarshal([]byte(step.Stdout), &list); err != nil {
		return "", fmt.Errorf("worker list --json: %w", err)
	}
	if len(list.Workers) != 1 {
		return "", fmt.Errorf("%d workers are registered, want the one init registered", len(list.Workers))
	}
	return list.Workers[0].ID, nil
}

// reach waits for a work order to reach a state, and fails on one it cannot leave on its own.
func (r *run) reach(id string, want workorders.State, limit time.Duration) error {
	return r.waitFor("state "+string(want), limit, func(ctx context.Context) (bool, error) {
		step := r.commandIn(ctx, "work show", "ropeshift", "work", "show", id, "--json")
		if step.Exit != 0 {
			return false, fmt.Errorf("work show exited %d: %s", step.Exit, step.Stderr)
		}
		var row workorders.WorkOrder
		if err := json.Unmarshal([]byte(step.Stdout), &row); err != nil {
			return false, fmt.Errorf("work show --json: %w", err)
		}
		r.saw(string(row.State))
		r.result.Final = string(row.State)
		switch row.State {
		case want:
			return true, nil
		case workorders.Blocked, workorders.Failed, workorders.Canceled:
			r.result.Steps = append(r.result.Steps, step)
			return false, fmt.Errorf("the work order ended %s on the way to %s", row.State, want)
		}
		return false, nil
	})
}

func (r *run) saw(state string) {
	if len(r.result.States) == 0 || r.result.States[len(r.result.States)-1] != state {
		r.result.States = append(r.result.States, state)
	}
}

// webStatus opens the web UI as a person's browser does, on loopback, and returns the page's status.
func webStatus(address string) int {
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	response, err := client.Get("http://" + address + "/")
	if err != nil {
		return 0
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	return response.StatusCode
}

// interfaces lists the interfaces of this network namespace that could carry traffic, each one that is
// up or holds an address, and every device the namespace holds. A new namespace also holds the
// kernel's fallback tunnel devices, such as gre0 and sit0, which stay down with no address.
func interfaces() (usable, devices []string) {
	all, err := net.Interfaces()
	if err != nil {
		return []string{"unreadable: " + err.Error()}, nil
	}
	for _, device := range all {
		devices = append(devices, device.Name)
		addresses, err := device.Addrs()
		if err != nil || device.Flags&net.FlagUp != 0 || len(addresses) != 0 {
			usable = append(usable, device.Name)
		}
	}
	slices.Sort(usable)
	slices.Sort(devices)
	return usable, devices
}

// offLoopback reads every TCP and UDP socket in this namespace, and lists each one bound to an address
// off loopback or with a peer off loopback. A listener on every address counts, because it would take
// connections from beyond loopback.
func offLoopback() []string {
	var found []string
	for _, table := range []string{"tcp", "tcp6", "udp", "udp6"} {
		file, err := os.Open(filepath.Join("/proc/net", table))
		if err != nil {
			found = append(found, table+": "+err.Error())
			continue
		}
		scanner := bufio.NewScanner(file)
		scanner.Scan()
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 3 {
				continue
			}
			local, peer := procAddress(fields[1]), procAddress(fields[2])
			if local == nil || !local.IsLoopback() || peer == nil || (!peer.IsLoopback() && !peer.IsUnspecified()) {
				found = append(found, fmt.Sprintf("%s %s -> %s", table, fields[1], fields[2]))
			}
		}
		_ = file.Close()
	}
	return found
}

// procAddress decodes an address of /proc/net, which holds each 32-bit word in the host's byte order.
func procAddress(field string) net.IP {
	host, _, found := strings.Cut(field, ":")
	if !found {
		return nil
	}
	raw, err := hex.DecodeString(host)
	if err != nil || (len(raw) != net.IPv4len && len(raw) != net.IPv6len) {
		return nil
	}
	ip := make(net.IP, len(raw))
	for i := 0; i < len(raw); i += 4 {
		binary.BigEndian.PutUint32(ip[i:], binary.NativeEndian.Uint32(raw[i:]))
	}
	return ip
}
