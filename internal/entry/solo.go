package entry

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/claudecode"
	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/adapters/hookrun"
	"ropeshift.com/ropeshift/internal/adapters/lexical"
	"ropeshift.com/ropeshift/internal/adapters/localauth"
	"ropeshift.com/ropeshift/internal/adapters/machine"
	"ropeshift.com/ropeshift/internal/adapters/osexec"
	"ropeshift.com/ropeshift/internal/adapters/profiles"
	"ropeshift.com/ropeshift/internal/adapters/repodocs"
	"ropeshift.com/ropeshift/internal/adapters/repofiles"
	"ropeshift.com/ropeshift/internal/adapters/secrets"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/adapters/systemd"
	"ropeshift.com/ropeshift/internal/adapters/tmux"
	"ropeshift.com/ropeshift/internal/adapters/workerlink"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/web"
)

// soloScope is the one implicit workspace of solo mode.
const soloScope = ledger.WorkspaceID("default")

const lockFileName = "ropeshiftd.lock"

// exitBusy says another daemon holds this installation.
const exitBusy = 3

// envSessionOverride carries a real-session suite's guard flags. Only the realsession
// build reads what it holds.
const envSessionOverride = "ROPESHIFT_SESSION_OVERRIDE"

// tickActor is who a tick's records name.
func tickActor(name string) ledger.Actor {
	return ledger.Actor{Kind: ledger.ActorSystem, ID: name}
}

// soloOptions is the command line of `ropeshiftd solo`.
type soloOptions struct {
	sources    repeatedFlag
	ref        string
	directory  string
	web        string
	telemetry  string
	leaseTick  time.Duration
	intakeTick time.Duration
	batchTick  time.Duration
	wakeTick   time.Duration
	notifyTick time.Duration
	// telemetryTick is how often the ledger is read into spans, and the emitter flushes at twice it.
	telemetryTick time.Duration
	leaseTTL      time.Duration
	heartbeat     time.Duration
}

type repeatedFlag []string

func (r *repeatedFlag) String() string { return strings.Join(*r, ",") }

func (r *repeatedFlag) Set(value string) error {
	*r = append(*r, value)
	return nil
}

func parseSolo(args []string, stderr io.Writer) (soloOptions, bool) {
	var opts soloOptions
	flags := soloFlags(&opts)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		return opts, false
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "ropeshiftd solo: unexpected argument %q\n", flags.Arg(0))
		return opts, false
	}
	return opts, true
}

// soloFlags is the daemon's flag set, which its help prints too.
func soloFlags(opts *soloOptions) *flag.FlagSet {
	flags := flag.NewFlagSet("ropeshiftd solo", flag.ContinueOnError)
	flags.Var(&opts.sources, "source", "a repository to read work from, repeatable")
	flags.StringVar(&opts.ref, "ref", "", "the ref intake reads, by default the default branch")
	flags.StringVar(&opts.directory, "dir", "", "the directory intake reads, by default the whole repository")
	flags.StringVar(&opts.web, "web", cmp.Or(os.Getenv(api.EnvWeb), api.DefaultWebAddress), "the loopback address of the web UI, or off")
	flags.StringVar(&opts.telemetry, "telemetry", cmp.Or(os.Getenv(api.EnvTelemetry), api.DefaultTelemetryAddress), "the loopback address of the OTLP receiver, or off")
	flags.DurationVar(&opts.leaseTick, "lease-tick", 5*time.Second, "how often expired leases lapse")
	flags.DurationVar(&opts.intakeTick, "intake-tick", 30*time.Second, "how often sources are polled")
	flags.DurationVar(&opts.batchTick, "batch-tick", 5*time.Second, "how often closed batches reach members")
	flags.DurationVar(&opts.wakeTick, "wake-tick", time.Second, "how often due wakes are evaluated")
	flags.DurationVar(&opts.notifyTick, "notify-tick", 30*time.Second, "how often members are told what waits")
	flags.DurationVar(&opts.telemetryTick, "telemetry-tick", defaultSpansEvery, "how often the ledger is read into spans and metrics")
	flags.DurationVar(&opts.leaseTTL, "lease-ttl", 60*time.Second, "the policy's lease")
	flags.DurationVar(&opts.heartbeat, "heartbeat", 15*time.Second, "how often the worker beats")
	return flags
}

// installationPaths applies D19 on this machine.
func installationPaths() (api.Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return api.Paths{}, err
	}
	return api.ResolvePaths(runtime.GOOS, os.Getenv, home)
}

func runSolo(args []string, stdout, stderr io.Writer) int {
	opts, ok := parseSolo(args, stderr)
	if !ok {
		return exitUsage
	}
	paths, err := installationPaths()
	if err != nil {
		fmt.Fprintf(stderr, "ropeshiftd solo: %v\n", err)
		return exitError
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := serveSolo(ctx, paths, opts, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "ropeshiftd solo: %v\n", err)
		if errors.Is(err, errBusy) {
			return exitBusy
		}
		return exitError
	}
	return exitOK
}

var errBusy = errors.New("already running for this installation")

// holdInstallation takes the lock that makes this daemon the only one. It is a record
// lock, which belongs to this process, so the kernel releases it when the process dies
// and a crash leaves nothing to clean up. A flock would belong to the open file instead,
// and a child forked an instant before a kill shares that file until it execs, which
// under load kept a killed daemon's installation held for hundreds of milliseconds. Only
// this file opens the lock, because closing any descriptor of it would end the lock.
func holdInstallation(stateDir string) (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(stateDir, lockFileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	whole := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: io.SeekStart}
	for {
		err = syscall.FcntlFlock(file.Fd(), syscall.F_SETLK, &whole)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	switch {
	case err == nil:
		return file, nil
	case errors.Is(err, syscall.EAGAIN), errors.Is(err, syscall.EACCES):
		err = errBusy
	}
	_ = file.Close()
	return nil, err
}

func openLedger(ctx context.Context, stateDir string) (ports.Store, error) {
	store, err := sqlite.Open(filepath.Join(stateDir, api.LedgerFileName), sqlstore.Options{})
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	_, err = store.CreateWorkspace(ctx, ledger.Workspace{ID: soloScope, Name: "Solo"}, tickActor("solo"))
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

// serveSolo runs until the context ends. It has no shutdown step that correctness
// needs, because the daemon is crash-only.
func serveSolo(ctx context.Context, paths api.Paths, opts soloOptions, stdout, stderr io.Writer) error {
	if err := server.PrivateDir(paths.StateDir); err != nil {
		return err
	}
	lock, err := holdInstallation(paths.StateDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	store, err := openLedger(ctx, paths.StateDir)
	if err != nil {
		return err
	}
	defer store.Close()
	listener, err := server.Listen(paths.ControlSocket())
	if err != nil {
		return err
	}
	telemetry, err := startTelemetry(ctx, paths, opts.telemetry, liveTelemetryDeps(opts.telemetryTick), stdout, stderr)
	if err != nil {
		return err
	}
	defer telemetry.close()
	// The worker's lanes and the web pane share one terminal server, so a member's browser reaches the
	// same pane a terminal attaches to.
	laneRuntime, terminal, err := soloLanes(paths, opts, stderr)
	if err != nil {
		return err
	}
	batches, windows := soloBatches(paths), soloWindows(paths)
	config := server.Config{
		Scope: soloScope, Store: store, LeaseTTL: opts.leaseTTL.Milliseconds(), HeartbeatMs: opts.heartbeat.Milliseconds(),
		Knowledge: soloKnowledge(opts), Matcher: lexical.Matcher{Store: store},
		Identity: localauth.New(store, localauth.DefaultParams), Batches: batches, Windows: windows, PackTokens: soloPackTokens(paths),
		Telemetry: telemetry.store, Backup: sqlite.LedgerBackup{File: filepath.Join(paths.StateDir, api.LedgerFileName)},
		Panes: soloPanes(terminal), Starts: soloStarts(opts),
	}
	httpServer := &http.Server{Handler: server.New(config), ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(listener) }()
	fmt.Fprintf(stdout, "ropeshiftd: serving %s\n", paths.ControlSocket())
	webServer, err := startWeb(config, opts.web, stdout, stderr)
	if err != nil {
		return err
	}
	if webServer != nil {
		defer webServer.Close()
	}

	spans, err := telemetry.spansTick(ctx, store)
	if err != nil {
		return err
	}
	ticks := append(soloTicks(store, opts, batches, windows), noticeTick(config, liveNotifyDeps(), opts.notifyTick), spans, telemetry.measuresTick(store))
	ticksDone := make(chan struct{})
	go func() {
		defer close(ticksDone)
		control.RunTicks(ctx, ticks, func(name string, err error) {
			fmt.Fprintf(stderr, "ropeshiftd: %s tick: %v\n", name, err)
		})
	}()
	workerDone, err := startSoloWorker(ctx, paths, store, opts, laneRuntime, telemetry, stderr)
	if err != nil {
		return err
	}

	select {
	case err = <-serveErr:
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
		err = nil
	}
	<-ticksDone
	<-workerDone
	telemetry.lastRead(spans, stderr)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// startWeb serves the web UI on loopback. An address that is no loopback address is a mistake, and the
// daemon stops on it, because a person asked for something solo mode never does. A port something else
// holds costs only the UI, so the daemon goes on: lanes do not wait on a page.
func startWeb(config server.Config, address string, stdout, stderr io.Writer) (*http.Server, error) {
	if address == api.WebOff {
		return nil, nil
	}
	listener, err := server.ListenWeb(address)
	if errors.Is(err, syscall.EADDRINUSE) {
		fmt.Fprintf(stderr, "ropeshiftd: web: %v\n", err)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	assets, err := web.Assets()
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	bound := listener.Addr().(*net.TCPAddr)
	webServer := &http.Server{
		Handler:           server.NewWeb(config, server.WebOptions{Port: bound.Port, Assets: assets}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := webServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "ropeshiftd: web: %v\n", err)
		}
	}()
	fmt.Fprintf(stdout, "ropeshiftd: web http://%s\n", bound)
	return webServer, nil
}

// startSoloWorker runs the worker in this process, on the real protocol over the
// private socket, so solo mode exercises the path a group's worker takes. A first
// start makes its own join token, because the person who started the daemon owns
// the control plane.
func startSoloWorker(
	ctx context.Context, paths api.Paths, store ports.Store, opts soloOptions, runtime *worker.LaneRuntime, telemetry *soloTelemetry, stderr io.Writer,
) (<-chan struct{}, error) {
	if err := keepStableBinary(ctx, paths, osexec.Runner{}); err != nil {
		return nil, err
	}
	journal, err := sqlite.OpenJournal(filepath.Join(paths.StateDir, api.JournalFileName))
	if err != nil {
		return nil, err
	}
	if err := journal.Migrate(ctx); err != nil {
		_ = journal.Close()
		return nil, err
	}
	runtime.Telemetry = telemetry.lane
	socket, err := server.Listen(paths.WorkerSocket())
	if err != nil {
		_ = journal.Close()
		return nil, err
	}
	plane := control.NewWorkers(store, soloScope, control.WorkerOptions{
		HeartbeatMs: opts.heartbeat.Milliseconds(), LeaseTTLMs: opts.leaseTTL.Milliseconds(),
	})
	client := api.NewUnixClient(paths.ControlSocket())
	subject := worker.New(workerlink.New(client), journal, clock.System{}, worker.Config{
		Name: "solo",
		JoinToken: func(ctx context.Context) (string, error) {
			return plane.CreateJoinToken(ctx, tickActor("solo"))
		},
		Manifest:  soloManifest(paths),
		Spool:     paths.Spool(),
		Lanes:     runtime,
		Telemetry: telemetry.emitter,
		Report: func(err error) {
			fmt.Fprintf(stderr, "ropeshiftd: worker: %v\n", err)
		},
	})
	socketDone := make(chan struct{})
	go func() {
		defer close(socketDone)
		if err := subject.ServeLanes(ctx, socket); err != nil {
			fmt.Fprintf(stderr, "ropeshiftd: worker socket: %v\n", err)
		}
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer journal.Close()
		defer client.Close()
		if err := subject.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(stderr, "ropeshiftd: worker: %v\n", err)
		}
		<-socketDone
	}()
	return done, nil
}

// soloManifest is what this worker says of itself. A user's layer that does not parse
// leaves capacity unstated, and a lane that reads it reports the fault.
func soloManifest(paths api.Paths) ledger.Manifest {
	var maxLanes int64
	if raw, err := os.ReadFile(filepath.Join(paths.StateDir, profiles.UserFile)); err == nil || errors.Is(err, os.ErrNotExist) {
		if profile, err := profiles.Resolve(string(raw)); err == nil {
			maxLanes = profile.Admission.MaxLanes
		}
	}
	return worker.BaseManifest(ledger.Owner{Kind: string(ledger.ActorMember), MemberID: localauth.Member}, maxLanes)
}

// soloDriver is the agent CLI, as this machine would start it.
func soloDriver(runner ports.ProcessRunner, terminal ports.Terminal, env []string) *claudecode.Driver {
	return claudecode.New(runner, claudecode.Options{Env: env, Terminal: terminal})
}

// laneRunner starts a lane's processes. A stop of a systemd unit ends its whole control group, so on
// Linux a process that must outlive the worker's service starts in a scope of the user's manager
// (D20). A machine with no user manager has no unit to stop, unless the daemon runs in one with no
// way to reach it, which the daemon says, because a stop would then end its lanes.
func laneRunner(goos string, getenv func(string) string, stderr io.Writer) ports.ProcessRunner {
	if goos != "linux" {
		return osexec.Runner{}
	}
	if scope, ok := systemd.UserScope(osexec.Runner{}, getenv); ok {
		return scope
	}
	if getenv("INVOCATION_ID") != "" {
		fmt.Fprintln(stderr, "ropeshiftd: no systemd user manager answers, so a stop of this service ends its lanes")
	}
	return osexec.Runner{}
}

// runDoctor is `ropeshift worker doctor`. It needs no daemon, because a person runs it
// when nothing works yet.
func runDoctor(ctx context.Context, paths api.Paths) (ledger.Manifest, error) {
	env, err := lanes.BuildEnvironment(lanes.EnvironmentSpec{Lookup: os.LookupEnv})
	if err != nil {
		return ledger.Manifest{}, err
	}
	return worker.BuildManifest(ctx, soloDriver(osexec.Runner{}, nil, env), soloManifest(paths))
}

// soloLanes wires what a lane runs on: the unmodified agent CLI, the repositories this
// daemon reads work from, and the worker's own paths. It returns the worker's terminal server
// too, which is nil on a machine with no tmux.
func soloLanes(paths api.Paths, opts soloOptions, stderr io.Writer) (*worker.LaneRuntime, ports.Terminal, error) {
	override, err := lanes.ParseOverride(os.Getenv(envSessionOverride))
	if err != nil {
		return nil, nil, err
	}
	env, err := lanes.BuildEnvironment(lanes.EnvironmentSpec{Lookup: os.LookupEnv})
	if err != nil {
		return nil, nil, err
	}
	runner := laneRunner(runtime.GOOS, os.Getenv, stderr)
	terminal, err := soloTerminal(runner, paths, env)
	if err != nil {
		return nil, nil, err
	}
	return &worker.LaneRuntime{
		Driver: soloDriver(runner, terminal, env),
		Provisioner: worker.Provisioner{
			Profiles:  profiles.Filesystem{User: filepath.Join(paths.StateDir, profiles.UserFile)},
			Worktrees: gitcli.Worktrees{},
			Hooks:     hookrun.Runner{Processes: osexec.Runner{}},
		},
		Secrets:    secrets.Resolver{},
		Machine:    machine.Reader{Processes: osexec.Runner{}},
		Repository: checkouts(opts),
		Socket:     paths.WorkerSocket(),
		Spool:      paths.Spool(),
		Binary:     paths.StableBinary(),
		StateDir:   paths.StateDir,
		RunDir:     paths.RunDir,
		Override:   override,
	}, terminal, nil
}

// soloPanes is the worker's terminal server as the web pane reaches it, by the name the driver gives
// each generation's pane (ADR-0018). A machine with no tmux holds no pane, and the web pane says so.
func soloPanes(terminal ports.Terminal) server.Panes {
	if terminal == nil {
		return nil
	}
	return server.TerminalPanes{Terminal: terminal, Name: claudecode.PaneName}
}

// soloTerminal is the worker's private server. A machine with no tmux runs headless lanes
// and refuses an interactive one at its start, because a worker should still run what it
// can.
func soloTerminal(runner ports.ProcessRunner, paths api.Paths, env []string) (ports.Terminal, error) {
	terminal, err := tmux.New(runner, tmux.Options{Socket: paths.TerminalSocket(), Env: env})
	if errors.Is(err, tmux.ErrNoTmux) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return terminal, nil
}

// checkouts finds a work order's repository among the daemon's sources. A repository
// is known by its history, so a moved checkout still matches.
func checkouts(opts soloOptions) func(context.Context, string) (string, error) {
	return func(ctx context.Context, id string) (string, error) {
		for _, path := range opts.sources {
			instance, err := repofiles.New(repofiles.Config{Repository: path, Ref: opts.ref, Directory: opts.directory}).Instance(ctx)
			if err != nil {
				return "", err
			}
			if instance == id {
				return filepath.Abs(path)
			}
		}
		return "", fmt.Errorf("no source holds repository %q", id)
	}
}

// soloStarts places the lane of every claim from the daemon's checkouts: where the base branch stands, and
// whether the work a lane depends on is in it.
func soloStarts(opts soloOptions) *control.Starts {
	return &control.Starts{Bases: gitcli.Worktrees{}, Checkout: checkouts(opts)}
}

// acceptedCommits reads a repository at the commit a person accepted, under the same directory the
// daemon reads it in. A lane's branch lives in the repository its worktree came from.
func acceptedCommits(opts soloOptions) control.PlanSource {
	repository := checkouts(opts)
	return func(ctx context.Context, id, commit string) (ports.WorkSource, error) {
		path, err := repository(ctx, id)
		if err != nil {
			return nil, err
		}
		return repofiles.New(repofiles.Config{Repository: path, Ref: commit, Directory: opts.directory}), nil
	}
}

// keepStableBinary points the stable path at this binary, so generated settings and
// a running lane survive an upgrade that removes a versioned path (D18). A new target
// runs once, because the first run of a new binary can exceed the hook budget.
func keepStableBinary(ctx context.Context, paths api.Paths, runner ports.ProcessRunner) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	target, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	link := paths.StableBinary()
	if current, err := os.Readlink(link); err == nil && current == target {
		return nil
	}
	if err := server.PrivateDir(filepath.Dir(link)); err != nil {
		return err
	}
	replacement := link + ".new"
	_ = os.Remove(replacement)
	if err := os.Symlink(target, replacement); err != nil {
		return err
	}
	if err := os.Rename(replacement, link); err != nil {
		return err
	}
	process, err := runner.Start(ctx, ports.ProcessSpec{Path: link, Args: []string{"version"}})
	if err != nil {
		return err
	}
	_, err = process.Wait()
	return err
}

// soloKnowledge reads documents from the repositories this daemon reads work from, at the
// ref intake reads.
func soloKnowledge(opts soloOptions) []ports.KnowledgeSource {
	sources := make([]ports.KnowledgeSource, 0, len(opts.sources))
	for _, path := range opts.sources {
		sources = append(sources, repodocs.New(repofiles.Config{Repository: path, Ref: opts.ref, Directory: opts.directory}))
	}
	return sources
}

// soloBatches reads how long questions gather from the user's layer. A layer that does not
// parse leaves the defaults, and a lane that reads it reports the fault.
func soloBatches(paths api.Paths) questions.BatchPolicy {
	raw, err := os.ReadFile(filepath.Join(paths.StateDir, profiles.UserFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return questions.BatchPolicy{}
	}
	profile, err := profiles.Resolve(string(raw))
	if err != nil {
		return questions.BatchPolicy{}
	}
	window, _ := time.ParseDuration(profile.Lifecycle.HumanBatchWindow)
	maxWait, _ := time.ParseDuration(profile.Lifecycle.HumanBatchMaxWait)
	return questions.BatchPolicy{WindowMs: window.Milliseconds(), MaxWaitMs: maxWait.Milliseconds()}
}

// soloPackTokens reads a pack's budget from the user's layer. A layer that does not parse leaves
// the control plane's default, and a lane that reads it reports the fault.
func soloPackTokens(paths api.Paths) int64 {
	raw, err := os.ReadFile(filepath.Join(paths.StateDir, profiles.UserFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0
	}
	profile, err := profiles.Resolve(string(raw))
	if err != nil {
		return 0
	}
	return profile.Context.PackBudgetTokens
}

// soloWindows reads how long a message stays useful from the user's layer. A layer that does not
// parse leaves the defaults, and a lane that reads it reports the fault.
func soloWindows(paths api.Paths) messages.Windows {
	raw, err := os.ReadFile(filepath.Join(paths.StateDir, profiles.UserFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	profile, err := profiles.Resolve(string(raw))
	if err != nil {
		return nil
	}
	return profile.MessageWindows()
}

func soloTicks(store ports.Store, opts soloOptions, batches questions.BatchPolicy, windows messages.Windows) []control.Tick {
	leases := control.NewLeases(store).Starting(soloStarts(opts))
	delivering := control.NewMessages(store, windows)
	asked := control.NewQuestions(store, batches)
	intake := control.NewIntake(store).Telling(windows)
	reviews := control.NewReviews(store)
	accepting := control.NewAcceptances(store)
	wakes := control.NewWakes(store)
	sources := make([]ports.WorkSource, 0, len(opts.sources))
	for _, path := range opts.sources {
		sources = append(sources, repofiles.New(repofiles.Config{Repository: path, Ref: opts.ref, Directory: opts.directory}))
	}
	return []control.Tick{
		{
			Name:  "batches",
			Every: opts.batchTick,
			Run: func(ctx context.Context) error {
				_, err := asked.DeliverBatches(ctx, soloScope, tickActor("batch-tick"))
				return err
			},
		},
		{
			Name:  "lease",
			Every: opts.leaseTick,
			Run: func(ctx context.Context) error {
				_, err := leases.Sweep(ctx, soloScope, tickActor("lease-tick"))
				return err
			},
		},
		{
			Name:  "wakes",
			Every: opts.wakeTick,
			Run: func(ctx context.Context) error {
				_, err := wakes.WakeTick(ctx, soloScope, tickActor("wake-tick"))
				return err
			},
		},
		{
			Name:  "approvals",
			Every: opts.wakeTick,
			Run: func(ctx context.Context) error {
				_, err := control.NewApprovals(store).ExpireDue(ctx, soloScope)
				return err
			},
		},
		{
			Name:  "messages",
			Every: opts.wakeTick,
			Run: func(ctx context.Context) error {
				_, err := delivering.ExpireMessages(ctx, soloScope, tickActor("message-tick"))
				return err
			},
		},
		{
			// No release in phase 1 ships a Forge adapter, so a lane's checks wait for a second
			// principal (D16). The tick still sends back work with a failed check.
			Name:  "acceptance",
			Every: opts.leaseTick,
			Run: func(ctx context.Context) error {
				_, err := accepting.Settle(ctx, soloScope, nil, tickActor("acceptance-tick"))
				return err
			},
		},
		{
			Name:  "intake",
			Every: opts.intakeTick,
			Run: func(ctx context.Context) error {
				var failed []error
				for _, source := range sources {
					if _, err := intake.Poll(ctx, soloScope, source); err != nil {
						failed = append(failed, err)
					}
				}
				if _, err := intake.TakeInPlans(ctx, soloScope, acceptedCommits(opts)); err != nil {
					failed = append(failed, err)
				}
				if _, err := reviews.Promote(ctx, soloScope, tickActor("intake-tick")); err != nil {
					failed = append(failed, err)
				}
				if _, err := leases.RefreshStarts(ctx, soloScope, tickActor("intake-tick")); err != nil {
					failed = append(failed, err)
				}
				return errors.Join(failed...)
			},
		},
	}
}
