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
	"syscall"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/lexical"
	"ropeshift.com/ropeshift/internal/adapters/localauth"
	"ropeshift.com/ropeshift/internal/adapters/postgres"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/web"
)

// A group server is the control plane alone on Postgres (ADR-0031): the API, the worker plane, and the web
// UI on one TCP listener, every control-plane tick solo runs, and a control socket in its run directory for
// the commands a person runs on its machine. It holds no worker's adapter, and a test of the source keeps
// every file of this wiring free of tmux, checkouts, worktrees, and the agent drivers (FR-DEP-7).

const (
	// envDatabase and envListen are a container's way to give the server what its flags give.
	envDatabase = "ROPESHIFT_DATABASE"
	envListen   = "ROPESHIFT_LISTEN"
	// defaultListen is a loopback address that no installation of solo mode holds.
	defaultListen = "127.0.0.1:7400"
	// firstProjectDisplayName is what the first project of a new server shows, as solo's first one does.
	firstProjectDisplayName = "Default"
)

// workerMachineTicks are the ticks solo's daemon runs that act on the machine of the worker beside it: a
// desktop notification, and the telemetry of that worker's lanes. A server runs every other tick solo runs.
var workerMachineTicks = []string{"notices", "telemetry", "measures"}

// serverOptions is the command line of `ropeshiftd server`.
type serverOptions struct {
	database string
	listen   string

	leaseTick    time.Duration
	intakeTick   time.Duration
	batchTick    time.Duration
	wakeTick     time.Duration
	deliveryTick time.Duration
	forecastTick time.Duration
	leaseTTL     time.Duration
	heartbeat    time.Duration
}

// serverFlags is the server's flag set, which its help prints too, defaults and all. The connection string
// holds the database's password, so its flag has no default: the environment gives it after the flags are
// read, and nothing the help or a usage error prints can show it. getenv gives the address a container's
// environment sets.
func serverFlags(opts *serverOptions, getenv func(string) string) *flag.FlagSet {
	flags := flag.NewFlagSet("ropeshiftd server", flag.ContinueOnError)
	flags.StringVar(&opts.database, "database", "", "the Postgres connection string, which holds a password, so "+envDatabase+" is the way to give it")
	flags.StringVar(&opts.listen, "listen", cmp.Or(getenv(envListen), defaultListen), "the loopback address of the one listener ("+envListen+")")
	flags.DurationVar(&opts.leaseTick, "lease-tick", 5*time.Second, "how often expired leases lapse")
	flags.DurationVar(&opts.intakeTick, "intake-tick", 30*time.Second, "how often sources are polled")
	flags.DurationVar(&opts.batchTick, "batch-tick", 5*time.Second, "how often closed batches reach members")
	flags.DurationVar(&opts.wakeTick, "wake-tick", time.Second, "how often due wakes are evaluated")
	flags.DurationVar(&opts.deliveryTick, "delivery-tick", 30*time.Second, "how often each project's timelines and delivery records are folded from its log, and its open work estimated again")
	flags.DurationVar(&opts.forecastTick, "forecast-tick", time.Minute, "how often each plan's forecast is made again when what it rests on changed, and at least hourly")
	flags.DurationVar(&opts.leaseTTL, "lease-ttl", 60*time.Second, "the policy's lease")
	flags.DurationVar(&opts.heartbeat, "heartbeat", 15*time.Second, "how often a worker beats")
	return flags
}

func parseServer(args []string, getenv func(string) string, stderr io.Writer) (serverOptions, bool) {
	var opts serverOptions
	flags := serverFlags(&opts, getenv)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		return opts, false
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "ropeshiftd server: unexpected argument %q\n", flags.Arg(0))
		return opts, false
	}
	opts.database = cmp.Or(opts.database, getenv(envDatabase))
	return opts, true
}

// checkListen holds the listener to a loopback address: nothing in this version encrypts what a worker or a
// member sends, so a server reachable from another machine is a mistake it stops on.
func checkListen(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("--listen %q: %w", address, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return api.ErrServerOffLoopback
	}
	return nil
}

func runServer(args []string, stdout, stderr io.Writer) int {
	opts, ok := parseServer(args, os.Getenv, stderr)
	if !ok {
		return exitUsage
	}
	if opts.database == "" {
		fmt.Fprintf(stderr, "ropeshiftd server: a server needs a database: set --database or %s\n", envDatabase)
		return exitUsage
	}
	if err := checkListen(opts.listen); err != nil {
		fmt.Fprintf(stderr, "ropeshiftd server: %v\n", err)
		return exitUsage
	}
	paths, err := installationPaths()
	if err != nil {
		fmt.Fprintf(stderr, "ropeshiftd server: %v\n", err)
		return exitError
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := serveServer(ctx, paths, opts, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "ropeshiftd server: %v\n", err)
		if errors.Is(err, errBusy) {
			return exitBusy
		}
		return exitError
	}
	return exitOK
}

// serverDatabase is how the server connects: what the adapter goes on past, such as a schema its sweep
// dropped or an index it reconciled, reaches the server's own log as a migration's warning reaches solo's.
func serverDatabase(stderr io.Writer) postgres.Options {
	return postgres.Options{Store: sqlstore.Options{Warn: warnTo(stderr)}}
}

// firstProject is the project the plane protocol serves by default: the one called `default` when the
// installation holds it, else its oldest, and a new installation gets a project `default`, as solo's does,
// because a worker one protocol version behind serves only the default project (NFR-COMPAT-2).
func firstProject(ctx context.Context, state *postgres.State) (ledger.Project, error) {
	listed, err := state.List(ctx)
	if err != nil {
		return ledger.Project{}, err
	}
	for _, project := range listed {
		if project.ID == soloScope {
			return project, nil
		}
	}
	if len(listed) > 0 {
		return listed[0], nil
	}
	return state.Create(ctx, ledger.Project{
		ID: soloScope, Name: ledger.DefaultProjectName, DisplayName: firstProjectDisplayName,
	}, tickActor("server"))
}

// serveServer runs until the context ends. Like solo's daemon it has no shutdown step that correctness
// needs, because the control plane is crash-only.
func serveServer(ctx context.Context, paths api.Paths, opts serverOptions, stdout, stderr io.Writer) error {
	if err := server.PrivateDir(paths.StateDir); err != nil {
		return err
	}
	lock, err := holdInstallation(paths.StateDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	state, err := postgres.Open(ctx, opts.database, serverDatabase(stderr))
	if err != nil {
		return err
	}
	defer state.Close()
	home, err := firstProject(ctx, state)
	if err != nil {
		return err
	}
	store, err := state.Open(ctx, home)
	if err != nil {
		return err
	}
	controlListener, err := server.Listen(paths.ControlSocket())
	if err != nil {
		return err
	}
	defer controlListener.Close()
	listener, err := server.ListenServer(opts.listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	assets, err := web.Assets()
	if err != nil {
		return err
	}
	bound := listener.Addr().(*net.TCPAddr)
	installed := liveInstalledBuild(stderr)
	reads := unavailableServerReads()
	batches, windows := soloBatches(paths), soloWindows(paths)
	config := server.Config{
		Mode:  api.ModeGroup,
		Scope: home.ID, Store: store, Projects: state, Installation: state.Installation,
		LeaseTTL: opts.leaseTTL.Milliseconds(), HeartbeatMs: opts.heartbeat.Milliseconds(),
		Installed: installed.Installed, WebAddress: bound.String(),
		Matcher: lexical.Matcher{Store: store}, Matching: func(store ports.Store) ports.Matcher { return lexical.Matcher{Store: store} },
		Identity: localauth.New(store, localauth.DefaultParams), Batches: batches, Windows: windows, PackTokens: soloPackTokens(paths),
		LaneBranchPrefix: soloLaneBranchPrefix(paths),
		Knowledge:        reads.knowledge, Starts: reads.starts, Branches: reads.branches, Checkouts: reads.checkouts,
		ParseManifest: reads.parseManifest, Panes: reads.panes,
	}
	controlServer := &http.Server{Handler: server.New(config), ReadHeaderTimeout: 10 * time.Second}
	networkServer := &http.Server{
		Handler:           server.NewNetwork(config, server.WebOptions{Port: bound.Port, Assets: assets}),
		ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	serveErr := make(chan error, 2)
	go func() { serveErr <- controlServer.Serve(controlListener) }()
	go func() { serveErr <- networkServer.Serve(listener) }()
	fmt.Fprintf(stdout, "ropeshiftd: serving %s\n", paths.ControlSocket())
	fmt.Fprintf(stdout, "ropeshiftd: server http://%s\n", bound)

	ticks := append(serverTicks(state, opts, reads, batches, windows), installed.tick())
	ticksDone := make(chan struct{})
	go func() {
		defer close(ticksDone)
		control.RunTicks(ctx, ticks, func(name string, err error) {
			fmt.Fprintf(stderr, "ropeshiftd: %s tick: %v\n", name, err)
		})
	}()

	select {
	case err = <-serveErr:
	case <-ctx.Done():
		err = nil
	}
	cancel()
	shutdown, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	_ = networkServer.Shutdown(shutdown)
	_ = controlServer.Shutdown(shutdown)
	<-ticksDone
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// serverSources are the sources intake reads in a project: one for each repository the project holds, which
// a server reads from no clone, so each is a refusal that names the repository.
func serverSources(ctx context.Context, store ports.Store, project ledger.WorkspaceID, reads serverReads) ([]ports.WorkSource, error) {
	var sources []ports.WorkSource
	err := store.Read(ctx, project, func(tx ports.ReadTx) error {
		records, err := tx.Repositories().Repositories()
		for _, record := range records {
			if !record.Removed() {
				sources = append(sources, reads.workSource(record))
			}
		}
		return err
	})
	return sources, err
}

// serverTicks are the control plane's ticks on a server, each run in every active project. They are solo's,
// by name, less workerMachineTicks, and `TestServerRunsEverySoloControlTick` holds the two lists together.
// What solo reads from a clone, these read through reads, which refuses each such read by name.
func serverTicks(projects ports.Projects, opts serverOptions, reads serverReads, batches questions.BatchPolicy, windows messages.Windows) []control.Tick {
	tick := func(name string, every time.Duration, work func(context.Context, ledger.Project, ports.Store) error) control.Tick {
		return control.Tick{Name: name, Every: every, Run: eachProject(projects, work)}
	}
	return []control.Tick{
		tick("batches", opts.batchTick, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			_, err := control.NewQuestions(store, batches).DeliverBatches(ctx, p.ID, tickActor("batch-tick"))
			return err
		}),
		tick("lease", opts.leaseTick, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			_, err := control.NewLeases(store).Starting(reads.starts).Sweep(ctx, p.ID, tickActor("lease-tick"))
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
		tick("delivery", opts.deliveryTick, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			return control.DeliveryTick(ctx, store, p.ID)
		}),
		tick("forecast", opts.forecastTick, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			_, err := control.NewForecasts(store).Telling(windows).Reporting(func(err error) {
				fmt.Fprintf(os.Stderr, "ropeshiftd: forecast tick: project %s: %v\n", p.ID, err)
			}).Refresh(ctx, p.ID)
			return err
		}),
		tick("conversations", opts.wakeTick, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			_, err := control.NewConversations(store, windows).IdleStop(ctx, p.ID, tickActor("conversation-tick"))
			return err
		}),
		tick("acceptance", opts.leaseTick, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			_, err := control.NewAcceptances(store).Settle(ctx, p.ID, control.PullRequestChecks{Store: store}, tickActor("acceptance-tick"))
			return err
		}),
		tick("integration", opts.leaseTick, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			_, err := control.NewIntegrations(store).Settle(ctx, p.ID, tickActor("integration-tick"))
			return err
		}),
		tick("intake", opts.intakeTick, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			var failed []error
			intake := control.NewIntake(store).Telling(windows).Checking(reads.starts)
			sources, err := serverSources(ctx, store, p.ID, reads)
			if err != nil {
				failed = append(failed, err)
			}
			for _, source := range sources {
				if _, err := intake.Poll(ctx, p.ID, source); err != nil {
					failed = append(failed, err)
				}
			}
			if _, err := intake.TakeInPlans(ctx, p.ID, reads.acceptedCommits); err != nil {
				failed = append(failed, err)
			}
			if _, err := control.NewReviews(store).Promote(ctx, p.ID, tickActor("intake-tick")); err != nil {
				failed = append(failed, err)
			}
			if _, err := control.NewLeases(store).Starting(reads.starts).RefreshStarts(ctx, p.ID, tickActor("intake-tick")); err != nil {
				failed = append(failed, err)
			}
			return errors.Join(failed...)
		}),
	}
}
