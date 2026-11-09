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

// How long each wait may take. The daemon runs at its default ticks, so a lane waits for the worker's
// next beat, and the first lane also waits for the probe of the agent CLI's version.
const (
	readyWait = 30 * time.Second
	stepWait  = 3 * time.Minute
	pollEvery = 250 * time.Millisecond
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

	if _, err := r.must(r.command("version", "ropeshift", "version", "--json")); err != nil {
		return err
	}
	if _, err := r.must(r.onTerminal("init", []string{"ropeshift", "init"}, passphrase, passphrase)); err != nil {
		return err
	}
	repo := filepath.Join(home, "project")
	if err := r.newRepository(repo, filepath.Join(paths.StateDir, cli.SampleContractName)); err != nil {
		return err
	}
	if err := r.startDaemon(repo); err != nil {
		return err
	}
	if err := r.waitFor("the daemon's answer", readyWait, func() (bool, error) {
		return r.command("health", "ropeshift", "health").Exit == 0, nil
	}); err != nil {
		return err
	}
	login, err := r.must(r.onTerminal("login", []string{"ropeshift", "login"}, passphrase))
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
	if _, err := r.must(r.command("review", "ropeshift", "work", "review", id)); err != nil {
		return err
	}
	if err := r.reach(id, workorders.Ready); err != nil {
		return err
	}
	worker, err := r.theWorker()
	if err != nil {
		return err
	}
	if _, err := r.must(r.command("claim", "ropeshift", "work", "claim", id, "--worker", worker, "--claim-id", claimID)); err != nil {
		return err
	}
	if _, err := r.must(r.command("lane start", "ropeshift", "lane", "start", id, "--lane", laneID)); err != nil {
		return err
	}
	if err := r.reach(id, workorders.Acceptance); err != nil {
		return err
	}
	r.result.WebStatus = webStatus(api.DefaultWebAddress)
	r.result.OffLoopback = offLoopback()
	if _, err := r.must(r.command("accept", "ropeshift", "work", "accept", id)); err != nil {
		return err
	}
	if err := r.reach(id, workorders.Done); err != nil {
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
		if _, err := r.must(r.command("git", args...)); err != nil {
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
	case <-time.After(30 * time.Second):
		return errors.New("ropeshiftd did not stop within 30s of SIGTERM")
	}
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
			kill := exec.Command(tool, "-S", r.paths.TerminalSocket(), "kill-server")
			kill.Env = r.env
			if kill.Run() == nil {
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
		for _, pid := range []int{state.ChildPID, state.ShimPID} {
			if pid > 0 && syscall.Kill(pid, syscall.SIGKILL) == nil {
				r.result.Left = append(r.result.Left, fmt.Sprintf("process %d from %s", pid, filepath.Base(dir)))
			}
		}
	}
}

// onTerminal runs a command whose controlling terminal is a new one, and types each answer once its
// prompt shows, as a person does. Its stdout stays a pipe, as in `$(ropeshift login)`.
func (r *run) onTerminal(name string, args []string, answers ...string) Step {
	typed, held, err := ptypair.Open()
	if err != nil {
		return Step{Name: name, Args: args, Exit: -1, Stderr: err.Error()}
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = r.env
	cmd.Stdin = strings.NewReader("")
	cmd.ExtraFiles = []*os.File{held}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 3}
	var screen bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		answer(typed, &screen, answers)
	}()
	step := r.execute(name, cmd)
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

// command runs one program from PATH with the run's environment.
func (r *run) command(name string, args ...string) Step {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = r.env
	return r.execute(name, cmd)
}

func (r *run) execute(name string, cmd *exec.Cmd) Step {
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	step := Step{Name: name, Args: cmd.Args, Stdout: stdout.String(), Stderr: stderr.String(), Millis: time.Since(start).Milliseconds()}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		step.Exit = exit.ExitCode()
	case err != nil:
		step.Exit = -1
		step.Stderr += err.Error()
	}
	return step
}

// must records a step and fails the run on a non-zero exit.
func (r *run) must(step Step) (Step, error) {
	r.result.Steps = append(r.result.Steps, step)
	if step.Exit != 0 {
		return step, fmt.Errorf("%s exited %d: %s", strings.Join(step.Args, " "), step.Exit, strings.TrimSpace(step.Stderr))
	}
	return step, nil
}

func (r *run) waitFor(what string, limit time.Duration, done func() (bool, error)) error {
	deadline := time.Now().Add(limit)
	for {
		ok, err := done()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not happen within %v", what, limit)
		}
		time.Sleep(pollEvery)
	}
}

// theWorkOrder waits for intake to take the sample's one work order.
func (r *run) theWorkOrder() (string, error) {
	var id string
	err := r.waitFor("intake of the sample contract", stepWait, func() (bool, error) {
		step := r.command("work list", "ropeshift", "work", "list", "--json")
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
	step, err := r.must(r.command("worker list", "ropeshift", "worker", "list", "--json"))
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
func (r *run) reach(id string, want workorders.State) error {
	return r.waitFor("state "+string(want), stepWait, func() (bool, error) {
		step := r.command("work show", "ropeshift", "work", "show", id, "--json")
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
