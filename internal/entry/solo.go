package entry

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/checkouts"
	"ropeshift.com/ropeshift/internal/adapters/claudecode"
	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/ghcli"
	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/adapters/hookrun"
	"ropeshift.com/ropeshift/internal/adapters/inventory"
	"ropeshift.com/ropeshift/internal/adapters/lexical"
	"ropeshift.com/ropeshift/internal/adapters/localauth"
	"ropeshift.com/ropeshift/internal/adapters/machine"
	"ropeshift.com/ropeshift/internal/adapters/osexec"
	"ropeshift.com/ropeshift/internal/adapters/profiles"
	"ropeshift.com/ropeshift/internal/adapters/repodocs"
	"ropeshift.com/ropeshift/internal/adapters/repofiles"
	"ropeshift.com/ropeshift/internal/adapters/secrets"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
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
	// forgeTick is how often the worker reads each repository's forge sign-in and the pull requests it
	// watches, and how long a failed forge request waits before its next attempt.
	forgeTick time.Duration
	// telemetryTick is how often the ledger is read into spans, and the emitter flushes at twice it.
	telemetryTick time.Duration
	// deliveryTick is how often the delivery fold reads what each project's log added (ADR-0026).
	deliveryTick time.Duration
	leaseTTL     time.Duration
	heartbeat    time.Duration
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
	flags.DurationVar(&opts.forgeTick, "forge-tick", time.Minute, "how often the worker reads gh's sign-in and watched pull requests")
	flags.DurationVar(&opts.telemetryTick, "telemetry-tick", defaultSpansEvery, "how often the ledger is read into spans and metrics")
	flags.DurationVar(&opts.deliveryTick, "delivery-tick", 30*time.Second, "how often each project's timelines and delivery records are folded from its log, and its open work estimated again")
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

// openLedger brings the state directory to this build (ADR-0021) and opens the default project's ledger,
// which keeps the ledger file and the solo workspace, with the records every project shares in the
// installation database beside it. A repository a second project holds too stays with the first, and
// the daemon says so.
func openLedger(ctx context.Context, stateDir string, stderr io.Writer) (*sqlite.State, ports.Store, error) {
	state, store, err := sqlite.Upgrade(ctx, stateDir, api.LedgerFileName, ledger.Workspace{ID: soloScope, Name: "Solo"}, tickActor("solo"))
	if err != nil {
		return nil, nil, err
	}
	for _, conflict := range state.IndexConflicts {
		fmt.Fprintf(stderr, "ropeshiftd: repository %s is in another project, which keeps it; project %s holds it as %s too\n",
			conflict.Identity, conflict.Project, conflict.Repository)
	}
	return state, store, nil
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
	state, store, err := openLedger(ctx, paths.StateDir, stderr)
	if err != nil {
		return err
	}
	defer state.Close()
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
	kept := soloCheckouts(paths)
	laneRuntime, terminal, err := soloLanes(paths, opts, kept, stderr)
	if err != nil {
		return err
	}
	batches, windows := soloBatches(paths), soloWindows(paths)
	local := &soloWorker{}
	webListener, err := listenWeb(opts.web, stderr)
	if err != nil {
		return err
	}
	config := server.Config{
		Scope: soloScope, Store: store, Projects: state, Installation: state.Installation, LeaseTTL: opts.leaseTTL.Milliseconds(), HeartbeatMs: opts.heartbeat.Milliseconds(),
		Knowledge: soloKnowledge(opts), Matcher: lexical.Matcher{Store: store},
		Matching: func(store ports.Store) ports.Matcher { return lexical.Matcher{Store: store} },
		Identity: localauth.New(store, localauth.DefaultParams), Batches: batches, Windows: windows, PackTokens: soloPackTokens(paths),
		Telemetry: telemetry.store,
		Panes:     soloPanes(terminal), Starts: soloStarts(opts, kept), WebAddress: webAddressOf(webListener),
		Branches: branchSource(opts, kept), LaneBranchPrefix: soloLaneBranchPrefix(paths),
		Checkouts: kept, ModelClasses: soloModelClasses(paths),
		Profile: profiles.Filesystem{User: filepath.Join(paths.StateDir, profiles.UserFile)}, LocalWorker: local.id,
	}
	hosts := &soloHosts{kept: kept, runtime: laneRuntime}
	config.AssistantHosts = hosts.read
	httpServer := &http.Server{Handler: server.New(config), ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(listener) }()
	fmt.Fprintf(stdout, "ropeshiftd: serving %s\n", paths.ControlSocket())
	webServer, err := startWeb(config, webListener, stdout, stderr)
	if err != nil {
		return err
	}
	if webServer != nil {
		defer webServer.Close()
	}

	spans, err := telemetry.spansTick(ctx, state)
	if err != nil {
		return err
	}
	ticks := append(soloTicks(state, soloScope, opts, kept, batches, windows),
		noticeTick(config, state, liveNotifyDeps(), opts.notifyTick), spans, telemetry.measuresTick(state))
	ticksDone := make(chan struct{})
	go func() {
		defer close(ticksDone)
		control.RunTicks(ctx, ticks, func(name string, err error) {
			fmt.Fprintf(stderr, "ropeshiftd: %s tick: %v\n", name, err)
		})
	}()
	workerDone, err := startSoloWorker(ctx, paths, store, opts, laneRuntime, telemetry, hosts, local, stderr)
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

// listenWeb binds the web UI's listener on loopback, before the daemon serves anything, so the address
// it bound is known to every handler. An address that is no loopback address is a mistake, and the
// daemon stops on it, because a person asked for something solo mode never does. A port something else
// holds costs only the UI, so the daemon goes on with no listener: lanes do not wait on a page. A web UI
// that is off has none either.
func listenWeb(address string, stderr io.Writer) (net.Listener, error) {
	if address == api.WebOff {
		return nil, nil
	}
	listener, err := server.ListenWeb(address)
	if errors.Is(err, syscall.EADDRINUSE) {
		fmt.Fprintf(stderr, "ropeshiftd: web: %v\n", err)
		return nil, nil
	}
	return listener, err
}

// webAddressOf is the address a web listener bound, with the port the kernel picked when it was asked
// for port 0, or nothing when there is no listener.
func webAddressOf(listener net.Listener) string {
	if listener == nil {
		return ""
	}
	return listener.Addr().String()
}

// startWeb serves the web UI on the listener listenWeb bound, and serves nothing without one.
func startWeb(config server.Config, listener net.Listener, stdout, stderr io.Writer) (*http.Server, error) {
	if listener == nil {
		return nil, nil
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
	ctx context.Context, paths api.Paths, store ports.Store, opts soloOptions, runtime *worker.LaneRuntime, telemetry *soloTelemetry, hosts *soloHosts,
	local *soloWorker, stderr io.Writer,
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
		Manifest:  soloManifest(),
		Spool:     paths.Spool(),
		Lanes:     runtime,
		Telemetry: telemetry.emitter,
		Report: func(err error) {
			fmt.Fprintf(stderr, "ropeshiftd: worker: %v\n", err)
		},
	})
	hosts.serve(subject)
	local.worker.Store(subject)
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

// soloWorker holds the worker this daemon runs, once it has one, for the routes that act on its machine.
type soloWorker struct {
	worker atomic.Pointer[worker.Worker]
}

// id names the daemon's worker once it has registered.
func (s *soloWorker) id(context.Context) (string, error) {
	if subject := s.worker.Load(); subject != nil {
		if id := subject.Auth().WorkerID; id != "" {
			return id, nil
		}
	}
	return "", errors.New("this daemon's worker has not registered yet")
}

// soloManifest is what this worker says of itself. The worker states its capacity, the cap in force
// from the user's layer and its recommendation, when it opens a session.
func soloManifest() ledger.Manifest {
	return worker.BaseManifest(ledger.Owner{Kind: string(ledger.ActorMember), MemberID: localauth.Member}, 0)
}

// soloModelClasses reads the model this worker's profile maps each capability class to, from the user's
// layer as it stands at each read, because only that layer may change the mapping (ADR-0025).
func soloModelClasses(paths api.Paths) func(context.Context) (map[string]map[string]string, error) {
	return func(context.Context) (map[string]map[string]string, error) {
		raw, err := os.ReadFile(filepath.Join(paths.StateDir, profiles.UserFile))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		profile, err := profiles.Resolve(string(raw))
		if err != nil {
			return nil, err
		}
		return map[string]map[string]string{profile.Driver: profile.Agent.Classes}, nil
	}
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
func runDoctor(ctx context.Context) (ledger.Manifest, error) {
	env, err := lanes.BuildEnvironment(lanes.EnvironmentSpec{Lookup: os.LookupEnv})
	if err != nil {
		return ledger.Manifest{}, err
	}
	return worker.BuildManifest(ctx, soloDriver(osexec.Runner{}, nil, env), soloManifest())
}

// soloLanes wires what a lane runs on: the unmodified agent CLI, the repositories this
// daemon reads work from, and the worker's own paths. It returns the worker's terminal server
// too, which is nil on a machine with no tmux.
func soloLanes(paths api.Paths, opts soloOptions, kept ports.Checkouts, stderr io.Writer) (*worker.LaneRuntime, ports.Terminal, error) {
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
	commands, named := assistantShell()
	return &worker.LaneRuntime{
		Driver: soloDriver(runner, terminal, env),
		Provisioner: worker.Provisioner{
			Profiles:  profiles.Filesystem{User: filepath.Join(paths.StateDir, profiles.UserFile)},
			Worktrees: gitcli.Worktrees{},
			Hooks:     hookrun.Runner{Processes: osexec.Runner{}},
		},
		Secrets:    secrets.Resolver{},
		Machine:    machine.Reader{Processes: osexec.Runner{}},
		Repository: laneCheckout(checkoutOf(opts, kept)),
		Socket:     paths.WorkerSocket(),
		Spool:      paths.Spool(),
		Binary:     paths.StableBinary(),
		StateDir:   paths.StateDir,
		RunDir:     paths.RunDir,
		Override:   override,
		Inventory:  inventory.Git{},
		// gh runs with the daemon's own environment, where the person's sign-in is (ADR-0020).
		Forge:      ghcli.Forge{},
		ForgeEvery: opts.forgeTick,
		// What an assistant's shell may run of this binary, and what its rules deny by name (ADR-0024).
		AssistantCommands: commands, Routes: named, ControlEnv: controlEnv(paths),
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

// soloCheckouts is the checkouts this machine's worker keeps, in its state directory.
func soloCheckouts(paths api.Paths) *checkouts.File {
	return &checkouts.File{Path: filepath.Join(paths.StateDir, checkouts.FileName)}
}

// checkoutOf finds this machine's checkout of a project's repository: the one the worker keeps for the
// project, or else, in the first project, the daemon's source with that history. A repository is known
// by its history, so a moved source still matches. An empty project is the first.
func checkoutOf(opts soloOptions, kept ports.Checkouts) func(context.Context, ledger.WorkspaceID, string) (string, error) {
	return func(ctx context.Context, project ledger.WorkspaceID, id string) (string, error) {
		project = cmp.Or(project, soloScope)
		held, err := kept.Of(ctx, project)
		if err != nil {
			return "", err
		}
		if path, ok := held[id]; ok {
			return path, nil
		}
		if project == soloScope {
			for _, path := range opts.sources {
				instance, err := repofiles.New(repofiles.Config{Repository: path, Ref: opts.ref, Directory: opts.directory}).Instance(ctx)
				if err != nil {
					return "", err
				}
				if instance == id {
					return filepath.Abs(path)
				}
			}
		}
		return "", fmt.Errorf("this worker keeps no checkout of repository %q in project %s", id, project)
	}
}

// laneCheckout is a checkout lookup as a lane's runtime calls it, with the project the plane protocol names.
func laneCheckout(find func(context.Context, ledger.WorkspaceID, string) (string, error)) func(context.Context, string, string) (string, error) {
	return func(ctx context.Context, project, id string) (string, error) {
		return find(ctx, ledger.WorkspaceID(project), id)
	}
}

// projectSources are the repositories intake reads in a project: the daemon's sources in the first
// project, and every repository of the project this worker keeps a checkout of. A path both name is read
// once.
func projectSources(ctx context.Context, opts soloOptions, kept ports.Checkouts, home, project ledger.WorkspaceID) ([]ports.WorkSource, error) {
	var paths []string
	if project == home {
		for _, path := range opts.sources {
			absolute, err := filepath.Abs(path)
			if err != nil {
				return nil, err
			}
			paths = append(paths, absolute)
		}
	}
	held, err := kept.Of(ctx, project)
	if err != nil {
		return nil, err
	}
	for _, path := range slices.Sorted(maps.Values(held)) {
		if !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	sources := make([]ports.WorkSource, 0, len(paths))
	for _, path := range paths {
		sources = append(sources, repofiles.New(repofiles.Config{Repository: path, Ref: opts.ref, Directory: opts.directory}))
	}
	return sources, nil
}

// soloStarts places the lane of every claim from this machine's checkouts: where the base branch stands,
// and whether the work a lane depends on is in it.
func soloStarts(opts soloOptions, kept ports.Checkouts) *control.Starts {
	return &control.Starts{Bases: gitcli.Worktrees{}, Checkout: checkoutOf(opts, kept)}
}

// branchSource opens a project's repository at a branch or a commit, under the same directory the daemon
// reads it in. A lane's branch lives in the repository its worktree came from.
func branchSource(opts soloOptions, kept ports.Checkouts) server.BranchSource {
	repository := checkoutOf(opts, kept)
	return func(ctx context.Context, project ledger.WorkspaceID, id, ref string) (ports.WorkSource, error) {
		path, err := repository(ctx, project, id)
		if err != nil {
			return nil, err
		}
		return repofiles.New(repofiles.Config{Repository: path, Ref: ref, Directory: opts.directory}), nil
	}
}

// acceptedCommits reads a project's repository at the commit a person accepted.
func acceptedCommits(opts soloOptions, kept ports.Checkouts, project ledger.WorkspaceID) control.PlanSource {
	open := branchSource(opts, kept)
	return func(ctx context.Context, id, commit string) (ports.WorkSource, error) {
		return open(ctx, project, id, commit)
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

// soloLaneBranchPrefix reads what the name of a lane's branch begins with from the user's layer. A layer
// that does not parse leaves the built-in prefix, and a lane that reads it reports the fault.
func soloLaneBranchPrefix(paths api.Paths) string {
	raw, err := os.ReadFile(filepath.Join(paths.StateDir, profiles.UserFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		raw = nil
	}
	profile, err := profiles.Resolve(string(raw))
	if err != nil {
		profile, _ = profiles.Resolve("")
	}
	return profile.Worktree.BranchPrefix
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

// eachProject runs one tick's work in every active project, each in its own database (ADR-0021). An
// archived project runs no tick, and one project's failure stops no other.
func eachProject(projects ports.Projects, work func(context.Context, ledger.Project, ports.Store) error) func(context.Context) error {
	return func(ctx context.Context) error {
		listed, err := projects.List(ctx)
		if err != nil {
			return err
		}
		var failed []error
		for _, project := range listed {
			if project.State != ledger.ProjectActive {
				continue
			}
			store, err := projects.Open(ctx, project)
			if err == nil {
				err = work(ctx, project, store)
			}
			if err != nil {
				failed = append(failed, fmt.Errorf("project %s: %w", project.Name, err))
			}
		}
		return errors.Join(failed...)
	}
}

// soloTicks are the control plane's ticks, each run in every active project. Intake reads the repositories
// of each project this worker keeps a checkout of, and the daemon's sources in the first project.
func soloTicks(projects ports.Projects, home ledger.WorkspaceID, opts soloOptions, kept ports.Checkouts, batches questions.BatchPolicy, windows messages.Windows) []control.Tick {
	tick := func(name string, every time.Duration, work func(context.Context, ledger.Project, ports.Store) error) control.Tick {
		return control.Tick{Name: name, Every: every, Run: eachProject(projects, work)}
	}
	return []control.Tick{
		tick("batches", opts.batchTick, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			_, err := control.NewQuestions(store, batches).DeliverBatches(ctx, p.ID, tickActor("batch-tick"))
			return err
		}),
		tick("lease", opts.leaseTick, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			_, err := control.NewLeases(store).Starting(soloStarts(opts, kept)).Sweep(ctx, p.ID, tickActor("lease-tick"))
			return err
		}),
		tick("wakes", opts.wakeTick, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			_, err := control.NewWakes(store).WakeTick(ctx, p.ID, tickActor("wake-tick"))
			return err
		}),
		tick("approvals", opts.wakeTick, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			_, err := control.NewApprovals(store).ExpireDue(ctx, p.ID)
			return err
		}),
		tick("messages", opts.wakeTick, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			_, err := control.NewMessages(store, windows).ExpireMessages(ctx, p.ID, tickActor("message-tick"))
			return err
		}),
		// Each finished work order's timeline and delivery record, folded from the log past its cursor; the
		// project's share of the installation's delivery pool, while its estimate policy shares history; and then
		// each open work order's current estimate, drawn on the records the fold kept.
		tick("delivery", opts.deliveryTick, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			if _, err := control.NewDeliveryFold(store).Fold(ctx, p.ID); err != nil {
				return err
			}
			if _, err := control.NewDeliveryPool(store).Share(ctx, p.ID); err != nil {
				return err
			}
			_, err := control.NewEstimates(store).Refresh(ctx, p.ID)
			return err
		}),
		// An assistant's session idle past the assistant policy stops; closing a view stops nothing.
		tick("conversations", opts.wakeTick, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			_, err := control.NewConversations(store, windows).IdleStop(ctx, p.ID, tickActor("conversation-tick"))
			return err
		}),
		// The checks a pull request's base requires are the check source: a worker reads them on the head, and
		// a failed one sends the work back (FR-MERGE-2). A lane's own checks still wait for a second principal
		// (D16), and the tick still sends back work with a failed check.
		tick("acceptance", opts.leaseTick, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			_, err := control.NewAcceptances(store).Settle(ctx, p.ID, control.PullRequestChecks{Store: store}, tickActor("acceptance-tick"))
			return err
		}),
		// The integration step: a pull request a person merged ends its work at the levels where the person
		// merges, and at the levels where ropeshift merges, accepted work is merged once the integration rule
		// allows it (ADR-0020). The worker that opened a pull request reads it, and this acts on what it found.
		tick("integration", opts.leaseTick, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			_, err := control.NewIntegrations(store).Settle(ctx, p.ID, tickActor("integration-tick"))
			return err
		}),
		tick("intake", opts.intakeTick, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			var failed []error
			intake := control.NewIntake(store).Telling(windows).Checking(soloStarts(opts, kept))
			sources, err := projectSources(ctx, opts, kept, home, p.ID)
			if err != nil {
				failed = append(failed, err)
			}
			for _, source := range sources {
				if _, err := intake.Poll(ctx, p.ID, source); err != nil {
					failed = append(failed, err)
				}
			}
			if _, err := intake.TakeInPlans(ctx, p.ID, acceptedCommits(opts, kept, p.ID)); err != nil {
				failed = append(failed, err)
			}
			if _, err := control.NewReviews(store).Promote(ctx, p.ID, tickActor("intake-tick")); err != nil {
				failed = append(failed, err)
			}
			if _, err := control.NewLeases(store).Starting(soloStarts(opts, kept)).RefreshStarts(ctx, p.ID, tickActor("intake-tick")); err != nil {
				failed = append(failed, err)
			}
			return errors.Join(failed...)
		}),
	}
}
