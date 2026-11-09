//go:build sizing

package bench

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

// The sizing run is NFR-PERF-3 for one worker: 50 lanes at once on the double. It is behind its own
// tag, apart from `make bench`, because 50 sessions load a machine as no timing budget allows.
const (
	defaultLanes = 50
	// loadGuard stops the run before the machine's one-minute load average reaches it.
	loadGuard = 30.0
	// writeProbes is how many ledger writes are timed while every lane runs.
	writeProbes = 100
	// steadyFor is how long every lane runs at once before the release.
	steadyFor  = 10 * time.Second
	sizingWait = 5 * time.Minute
)

// sizingTicks run the daemon several times busier than its defaults, so lanes start within a second.
var sizingTicks = []string{"--intake-tick", "1s", "--heartbeat", "1s", "--lease-tick", "1s"}

// TestMain removes the double the run built.
func TestMain(m *testing.M) { os.Exit(doubles.Main(m)) }

// TestFiftyLanesOnOneWorker runs LANES lanes, 50 by default, on one worker through the real daemon,
// driver, shim, hooks, and MCP server, each on the double, held after its first prompt until all of
// them run at once. It records the memory and CPU of the daemon and of every process the lanes start,
// the time each ledger write takes through the API, and the load average, and it fails unless every
// lane runs at the same time and then completes.
func TestFiftyLanesOnOneWorker(t *testing.T) {
	lanes := defaultLanes
	if raw := os.Getenv("LANES"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			t.Fatalf("LANES=%q", raw)
		}
		lanes = n
	}
	s := newSizing(t, lanes)
	s.startDaemon()
	ids := s.workOrders()
	worker := s.worker()

	var reviews, claims, starts []time.Duration
	for i, id := range ids {
		lane := fmt.Sprintf("lane-%02d", i+1)
		doubles.ReapShims(t, filepath.Join(s.state, "lanes", lane))
		reviews = append(reviews, s.timed(func(ctx context.Context) error { _, err := s.client.Review(ctx, id); return err }))
		claims = append(claims, s.timed(func(ctx context.Context) error {
			_, err := s.client.Claim(ctx, id, api.ClaimRequest{ClaimID: "c-" + lane, WorkerID: worker})
			return err
		}))
		starts = append(starts, s.timed(func(ctx context.Context) error {
			_, err := s.client.StartLane(ctx, id, api.StartLane{LaneID: lane})
			return err
		}))
		s.sample()
	}
	// A work order reads running from the moment its lane starts in the ledger, before the worker has
	// spawned the session, so every lane runs at once only when each one's session is up and held.
	rampStart := time.Now()
	s.until("every lane's session running at once", func(counts map[workorders.State]int, now reading) bool {
		return counts[workorders.Running] == lanes && now.sessions == lanes
	})
	t.Logf("all %d sessions running %v after the last start", lanes, time.Since(rampStart).Round(time.Millisecond))

	steadyEnd := time.Now().Add(steadyFor)
	probes := make([]time.Duration, 0, writeProbes)
	for i := range writeProbes {
		probes = append(probes, s.timed(func(ctx context.Context) error {
			_, err := s.client.CreateWorkOrder(ctx, api.CreateWorkOrder{
				ID: fmt.Sprintf("probe-%03d", i), Title: "A write while every lane runs",
				Criteria: []planning.Criterion{{ID: "c1", Text: "It is written"}},
			})
			return err
		}))
		if i%10 == 0 {
			s.sample()
		}
	}
	for time.Now().Before(steadyEnd) {
		s.sample()
		time.Sleep(time.Second)
	}
	steady := s.sample()
	if steady.sessions != lanes {
		t.Errorf("%d sessions run after the writes, want all %d still held at once", steady.sessions, lanes)
	}

	if err := os.WriteFile(filepath.Join(doubles.BinDir(s.path), "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	released := time.Now()
	s.until("every lane's outcome", func(counts map[workorders.State]int, _ reading) bool {
		if n := counts[workorders.Blocked] + counts[workorders.Failed] + counts[workorders.Canceled]; n != 0 {
			t.Fatalf("%d lanes ended without completing: %v", n, counts)
		}
		return counts[workorders.Acceptance] == lanes
	})
	t.Logf("all %d lanes completed %v after the release", lanes, time.Since(released).Round(time.Millisecond))
	treeCPU, treePeak := s.stopDaemon()

	t.Logf("lanes=%d on one worker, %s/%s, ticks %v", lanes, runtime.GOOS, runtime.GOARCH, sizingTicks)
	t.Logf("ledger writes through the API: review %s; claim %s; start %s; with every lane running, create %s",
		spread(reviews), spread(claims), spread(starts), spread(probes))
	t.Logf("memory: the daemon's own RSS at most %d MiB, and %d MiB while all lanes ran; every process of the run at most %d MiB together, and %d MiB while all lanes ran (%d sessions, %d shims, %d MCP servers); the largest single process %d MiB",
		s.peakDaemonRSS>>10, steady.daemonRSS>>10, s.peakRSS>>10, steady.rss>>10, steady.sessions, steady.shims, steady.mcp, treePeak>>20)
	t.Logf("CPU: the daemon and every process it reaped used %v over %v (%.2f cores); every process of the run at most %.0f%% together, by ps",
		treeCPU.Round(time.Millisecond), s.wall().Round(time.Millisecond), treeCPU.Seconds()/s.wall().Seconds(), s.peakCPU)
	t.Logf("load average: at most %.2f in the run, %.2f with every lane running", s.peakLoad, steady.load)
}

// sizing is one run's installation, daemon, and samples.
type sizing struct {
	t       *testing.T
	lanes   int
	state   string
	run     string
	path    string
	env     []string
	marks   []string
	repo    *gittest.Repo
	binary  string
	daemon  *exec.Cmd
	exited  chan error
	started time.Time
	stopped time.Time
	client  *api.Client
	// lanesWork holds the IDs of the work orders the lanes run, and none of the write probes.
	lanesWork map[string]bool

	// The largest of the samples: RSS in KiB, every process of the run and the daemon's own, the
	// summed %CPU, and the one-minute load average.
	peakRSS       int64
	peakDaemonRSS int64
	peakCPU       float64
	peakLoad      float64
}

func newSizing(t *testing.T, lanes int) *sizing {
	t.Helper()
	dir := apitest.ShortDir(t)
	s := &sizing{t: t, lanes: lanes, state: filepath.Join(dir, "state"), run: filepath.Join(dir, "run")}
	s.path = doubles.InstallFakeClaude(t, "lane-held")
	s.env = []string{
		"HOME=" + dir, "PATH=" + s.path, api.EnvStateDir + "=" + s.state, api.EnvRunDir + "=" + s.run,
		api.EnvWeb + "=" + api.WebOff, api.EnvTelemetry + "=127.0.0.1:0",
	}
	if err := os.MkdirAll(s.state, 0o700); err != nil {
		t.Fatal(err)
	}
	// The builtin profile admits 4 lanes to a worker and to a sign-in; this machine's layer admits all.
	admission := fmt.Sprintf("[admission]\nmax_lanes = %d\nmax_lanes_per_sign_in = %d\n", lanes, lanes)
	if err := os.WriteFile(filepath.Join(s.state, "profile.toml"), []byte(admission), 0o600); err != nil {
		t.Fatal(err)
	}
	s.repo = gittest.New(t)
	s.repo.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	s.repo.Commit("contract.md", sizingContract(lanes))
	bin := t.TempDir()
	binary, err := doubles.Binary(bin, doubles.PrebuiltRopeshift, "ropeshift.com/ropeshift/cmd/ropeshift")
	if err != nil {
		t.Fatal(err)
	}
	daemon := filepath.Join(bin, doubles.PrebuiltDaemon)
	if err := os.Symlink(binary, daemon); err != nil {
		t.Fatal(err)
	}
	s.binary = daemon
	// The processes of this run are those whose command names its binary, its double, or its home.
	s.marks = []string{bin, doubles.BinDir(s.path), dir}
	doubles.EndTmuxOn(t, api.Paths{StateDir: s.state, RunDir: s.run}.TerminalSocket())
	return s
}

// sizingContract is one contract with a work order for each lane.
func sizingContract(lanes int) string {
	var b strings.Builder
	b.WriteString("+++\nkind = \"contract\"\nid = \"sizing\"\ntitle = \"Sizing\"\n\n[[criteria]]\nid = \"c1\"\ntext = \"It completes\"\n")
	for i := 1; i <= lanes; i++ {
		fmt.Fprintf(&b, "\n[[work_orders]]\nid = \"sizing-%02d\"\ntitle = \"Lane %d of the sizing run\"\npriority = 2\nsize = \"small\"\n", i, i)
	}
	b.WriteString("+++\n\nEvery work order is the same small piece of work, so only the count changes.\n")
	return b.String()
}

func (s *sizing) startDaemon() {
	t := s.t
	t.Helper()
	s.daemon = exec.Command(s.binary, append([]string{"solo", "--source", s.repo.Dir}, sizingTicks...)...)
	s.daemon.Env = s.env
	logFile, err := os.Create(filepath.Join(t.TempDir(), "daemon.log"))
	if err != nil {
		t.Fatal(err)
	}
	s.daemon.Stdout, s.daemon.Stderr = logFile, logFile
	s.started = time.Now()
	if err := s.daemon.Start(); err != nil {
		t.Fatal(err)
	}
	s.exited = make(chan error, 1)
	go func() { s.exited <- s.daemon.Wait() }()
	t.Cleanup(func() {
		if s.stopped.IsZero() {
			_ = s.daemon.Process.Signal(syscall.SIGKILL)
			if err := <-s.exited; err != nil {
				t.Logf("the daemon ended with %v", err)
			}
		}
		_ = logFile.Close()
		if t.Failed() {
			raw, _ := os.ReadFile(logFile.Name())
			t.Logf("the daemon said:\n%s", tail(string(raw), 60))
		}
	})
	s.client = api.NewUnixClient(api.Paths{StateDir: s.state, RunDir: s.run}.ControlSocket())
	t.Cleanup(s.client.Close)
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := s.client.Health(t.Context()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the daemon never answered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := apitest.SignIn(t.Context(), s.client); err != nil {
		t.Fatal(err)
	}
}

// stopDaemon ends the daemon by its own process ID. It returns what the kernel reports for the daemon
// together with every process it reaped, which holds the shims, and through them the sessions and their
// hooks: the CPU they used, and the largest peak RSS of any one of them.
func (s *sizing) stopDaemon() (time.Duration, int64) {
	t := s.t
	t.Helper()
	_ = s.daemon.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-s.exited:
		if err != nil {
			t.Errorf("the daemon exited on SIGTERM with %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the daemon did not stop within 30s of SIGTERM")
	}
	s.stopped = time.Now()
	state := s.daemon.ProcessState
	peak := int64(0)
	if usage, ok := state.SysUsage().(*syscall.Rusage); ok {
		peak = int64(usage.Maxrss)
		if runtime.GOOS == "linux" {
			peak <<= 10
		}
	}
	return state.UserTime() + state.SystemTime(), peak
}

func (s *sizing) wall() time.Duration { return s.stopped.Sub(s.started) }

// workOrders waits for intake to take the contract's work orders, and returns their IDs.
func (s *sizing) workOrders() []string {
	var ids []string
	s.wait("intake of every work order", func() bool {
		list, err := s.client.WorkOrders(s.t.Context(), "")
		if err != nil {
			s.t.Fatal(err)
		}
		ids = ids[:0]
		for _, row := range list {
			ids = append(ids, row.ID)
		}
		return len(ids) == s.lanes
	})
	slices.Sort(ids)
	s.lanesWork = map[string]bool{}
	for _, id := range ids {
		s.lanesWork[id] = true
	}
	return ids
}

func (s *sizing) worker() string {
	var id string
	s.wait("the worker's registration", func() bool {
		workers, err := s.client.Workers(s.t.Context())
		if err != nil {
			s.t.Fatal(err)
		}
		if len(workers) == 1 && workers[0].Epoch >= 1 {
			id = workers[0].ID
		}
		return id != ""
	})
	return id
}

// until waits for the work orders of the contract to reach a mix of states, and gives done each
// sample of the processes it takes as it goes.
func (s *sizing) until(what string, done func(map[workorders.State]int, reading) bool) {
	s.wait(what, func() bool {
		list, err := s.client.WorkOrders(s.t.Context(), "")
		if err != nil {
			s.t.Fatal(err)
		}
		counts := map[workorders.State]int{}
		for _, row := range list {
			if s.lanesWork[row.ID] {
				counts[row.State]++
			}
		}
		return done(counts, s.sample())
	})
}

// wait polls until done.
func (s *sizing) wait(what string, done func() bool) {
	deadline := time.Now().Add(sizingWait)
	for !done() {
		if time.Now().After(deadline) {
			s.t.Fatalf("%s did not happen within %v", what, sizingWait)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// timed runs one write through the API and returns how long its answer took.
func (s *sizing) timed(write func(context.Context) error) time.Duration {
	start := time.Now()
	if err := write(s.t.Context()); err != nil {
		s.t.Fatal(err)
	}
	return time.Since(start)
}

// reading is one look at the run's processes and the machine's load.
type reading struct {
	rss                  int64 // KiB, every process of the run
	daemonRSS            int64 // KiB, the daemon's own process
	cpu                  float64
	sessions, shims, mcp int
	load                 float64
}

// sample reads every process of this run with ps, which only reads, and the load average, and
// stops the run at the load guard. The daemon is the process with its exact ID.
func (s *sizing) sample() reading {
	t := s.t
	t.Helper()
	var got reading
	out, err := exec.Command("ps", "-axo", "pid=,rss=,%cpu=,command=").Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	daemon := ""
	if s.daemon != nil && s.daemon.Process != nil {
		daemon = strconv.Itoa(s.daemon.Process.Pid)
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		command := strings.Join(fields[3:], " ")
		if !slices.ContainsFunc(s.marks, func(mark string) bool { return strings.Contains(command, mark) }) {
			continue
		}
		rss, _ := strconv.ParseInt(fields[1], 10, 64)
		cpu, _ := strconv.ParseFloat(fields[2], 64)
		got.rss += rss
		got.cpu += cpu
		if fields[0] == daemon {
			got.daemonRSS = rss
		}
		switch {
		case strings.Contains(command, " lane-shim"):
			got.shims++
		case strings.Contains(command, " mcp"):
			got.mcp++
		case strings.Contains(command, doubles.BinDir(s.path)+"/claude"):
			got.sessions++
		}
	}
	got.load, err = loadAverage()
	if err != nil {
		t.Fatal(err)
	}
	s.peakRSS = max(s.peakRSS, got.rss)
	s.peakDaemonRSS = max(s.peakDaemonRSS, got.daemonRSS)
	s.peakCPU = max(s.peakCPU, got.cpu)
	s.peakLoad = max(s.peakLoad, got.load)
	if got.load >= loadGuard {
		t.Fatalf("the one-minute load average reached %.2f, past the guard of %.0f", got.load, loadGuard)
	}
	return got
}

// loadAverage is the machine's one-minute load average.
func loadAverage() (float64, error) {
	var raw string
	switch runtime.GOOS {
	case "linux":
		b, err := os.ReadFile("/proc/loadavg")
		if err != nil {
			return 0, err
		}
		raw = string(b)
	case "darwin":
		b, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
		if err != nil {
			return 0, err
		}
		raw = strings.Trim(strings.TrimSpace(string(b)), "{ }")
	default:
		return 0, errors.New("no load average on " + runtime.GOOS)
	}
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return 0, fmt.Errorf("a load average of %q", raw)
	}
	return strconv.ParseFloat(fields[0], 64)
}

// spread is the median, 99th percentile, and largest of a set of times.
func spread(times []time.Duration) string {
	if len(times) == 0 {
		return "none"
	}
	sorted := slices.Sorted(slices.Values(times))
	at := func(p int) time.Duration {
		return sorted[min(len(sorted)-1, len(sorted)*p/100)].Round(10 * time.Microsecond)
	}
	return fmt.Sprintf("n=%d p50=%v p99=%v max=%v", len(sorted), at(50), at(99), sorted[len(sorted)-1].Round(10*time.Microsecond))
}

func tail(text string, lines int) string {
	all := strings.Split(strings.TrimRight(text, "\n"), "\n")
	return strings.Join(all[max(0, len(all)-lines):], "\n")
}
