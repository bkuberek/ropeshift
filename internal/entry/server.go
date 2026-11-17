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
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"ropeshift.com/ropeshift/internal/adapters/clock"
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
	// envSigninMaxFailures and envSigninLockout give the limit on wrong guesses.
	envSigninMaxFailures = "ROPESHIFT_SIGNIN_MAX_FAILURES"
	envSigninLockout     = "ROPESHIFT_SIGNIN_LOCKOUT"
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
	// tlsCert and tlsKey name the files of a certificate a person supplies, and names the host names and
	// addresses the certificate the server makes covers (ADR-0031).
	tlsCert, tlsKey string
	names           []string
	// signinMax is how many wrong guesses at a passphrase or a join token a client address may make within
	// signinLockout, which is also how long it is refused after (ADR-0031).
	signinMax     int
	signinLockout time.Duration
	// problems are what the environment gave that is no setting, which parseServer reports with the flags'.
	problems []string

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
	flags.StringVar(&opts.listen, "listen", cmp.Or(getenv(envListen), defaultListen), "the address of the one listener, which serves TLS on any address but a loopback one ("+envListen+")")
	flags.StringVar(&opts.tlsCert, "tls-cert", getenv(envTLSCert), "the file of a certificate to serve in place of the server's own, with --tls-key ("+envTLSCert+")")
	flags.StringVar(&opts.tlsKey, "tls-key", getenv(envTLSKey), "the file of that certificate's key ("+envTLSKey+")")
	maxFailures, lockout := server.DefaultMaxFailures, server.DefaultLockout
	if value := getenv(envSigninMaxFailures); value != "" {
		if number, err := strconv.Atoi(value); err != nil {
			opts.problems = append(opts.problems, fmt.Sprintf("%s is %q, which is no whole number", envSigninMaxFailures, value))
		} else {
			maxFailures = number
		}
	}
	if value := getenv(envSigninLockout); value != "" {
		if duration, err := time.ParseDuration(value); err != nil {
			opts.problems = append(opts.problems, fmt.Sprintf("%s is %q, which is no duration such as 15m", envSigninLockout, value))
		} else {
			lockout = duration
		}
	}
	flags.IntVar(&opts.signinMax, "signin-max-failures", maxFailures, "how many wrong passphrases or join tokens one client address may try within --signin-lockout ("+envSigninMaxFailures+")")
	flags.DurationVar(&opts.signinLockout, "signin-lockout", lockout, "how long wrong guesses count, and how long a client address is refused once it has made too many ("+envSigninLockout+")")
	flags.Func("name", "a host name or address the server's own certificate covers, repeated or comma separated, which makes a loopback listener serve TLS too ("+envServerNames+")", func(value string) error {
		opts.names = append(opts.names, splitList(value)...)
		return nil
	})
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
	// Two ways to give one connection string leave no way to say which is meant, so neither quietly wins.
	if opts.database != "" && getenv(envDatabase) != "" {
		fmt.Fprintf(stderr, "ropeshiftd server: give the database in --database or in %s, not both\n", envDatabase)
		return opts, false
	}
	opts.database = cmp.Or(opts.database, getenv(envDatabase))
	for _, problem := range opts.problems {
		fmt.Fprintf(stderr, "ropeshiftd server: %s\n", problem)
	}
	if len(opts.problems) > 0 {
		return opts, false
	}
	// A limit that allows no failure, or that lasts no time, limits nothing a person could use: it would refuse
	// every sign-in, or none.
	if opts.signinMax < 1 {
		fmt.Fprintf(stderr, "ropeshiftd server: --signin-max-failures (%s) is at least 1, and is %d\n", envSigninMaxFailures, opts.signinMax)
		return opts, false
	}
	if opts.signinLockout <= 0 {
		fmt.Fprintf(stderr, "ropeshiftd server: --signin-lockout (%s) is a duration above zero, and is %s\n", envSigninLockout, opts.signinLockout)
		return opts, false
	}
	// A flag wins over the environment, as it does for every other setting.
	named := false
	flags.Visit(func(f *flag.Flag) { named = named || f.Name == "name" })
	if !named {
		opts.names = splitList(getenv(envServerNames))
	}
	return opts, true
}

// splitList reads a comma separated list, as a variable gives one, and leaves out what is empty.
func splitList(value string) []string {
	var list []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			list = append(list, item)
		}
	}
	return list
}

// checkServerDirectories refuses a server whose state directory is not the machine's own while its run directory
// is. The control socket lives in the run directory, and a server that is not the machine's own installation
// holds the lock of another state directory, so it would bind where the installation that is the machine's own
// has its socket, and removing a socket before it binds is how a daemon takes a path it holds the lock for. A
// server that moves its state names its run directory too.
func checkServerDirectories(goos string, getenv func(string) string, home string) error {
	paths, err := api.ResolvePaths(goos, getenv, home)
	if err != nil {
		return err
	}
	if getenv(api.EnvRunDir) != "" {
		return nil
	}
	unmoved := func(name string) string {
		switch name {
		case api.EnvStateDir, api.EnvRunDir, api.EnvWorktreeDir:
			return ""
		}
		return getenv(name)
	}
	machine, err := api.ResolvePaths(goos, unmoved, home)
	if err != nil {
		return err
	}
	if paths.StateDir != machine.StateDir {
		return fmt.Errorf("this server keeps its state apart from the machine's own, so its control socket needs a run directory of its own: set %s beside %s", api.EnvRunDir, api.EnvStateDir)
	}
	return nil
}

// socketAnswers says something answers on a unix socket's path. A file a daemon left when it died answers
// nothing, and a path with nothing at it neither.
func socketAnswers(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// listenControl binds the control socket, unless something answers on it. Binding removes the path first, on the
// strength of the lock this server holds, and a socket something answers on is another daemon's, however it came to
// be at this path. The check comes right before the bind and nothing else runs between them, so a daemon that
// starts while this server opens its database is not a daemon whose socket this server removes.
func listenControl(path string) (net.Listener, error) {
	if socketAnswers(path) {
		return nil, fmt.Errorf("another daemon answers on %s, so this server binds nothing there", path)
	}
	return server.Listen(path)
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
	transport, err := planTransport(opts)
	if err != nil {
		fmt.Fprintf(stderr, "ropeshiftd server: %v\n", err)
		return exitUsage
	}
	// The driver's error for a string that does not parse repeats part of it, and hides a password only by a
	// pattern that a password with a space or a quote in it escapes. So the string is parsed here, and a
	// failure says that it did not parse and nothing of what the server read.
	if _, err := pgxpool.ParseConfig(opts.database); err != nil {
		fmt.Fprintln(stderr, "ropeshiftd server: the database connection string does not parse as a Postgres connection string, and it is not shown here because it holds a password")
		return exitUsage
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "ropeshiftd server: %v\n", err)
		return exitError
	}
	if err := checkServerDirectories(runtime.GOOS, os.Getenv, home); err != nil {
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
	if err := serveServer(ctx, paths, opts, transport, stdout, stderr); err != nil {
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

// serverLimits bound how long the listener's server waits on a client: for the headers of a request, for the
// whole of it, and for the next one on a connection that has none.
type serverLimits struct {
	readHeader, read, idle time.Duration
}

// networkLimits are generous, because a worker uploads a batch of events and a member's browser holds streams
// open, and bounded, because the listener answers anyone who can reach its address. A request's read time
// does not limit a stream's answer, so the ReadTimeout ends nothing a member watches.
var networkLimits = serverLimits{readHeader: 10 * time.Second, read: 2 * time.Minute, idle: 2 * time.Minute}

// maxHeaderBytes is the most of a request's headers the listener reads, which a session or a credential never
// comes near.
const maxHeaderBytes = 32 << 10

// networkHTTPServer is the server of the listener, with every read bounded.
func networkHTTPServer(handler http.Handler) *http.Server {
	// HTTP/1.1 serves plaintext and TLS alike, with the limits above: the listener's TLS config offers no other
	// protocol, and this says so for any listener handed to the server.
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	return &http.Server{
		Handler: handler, ReadHeaderTimeout: networkLimits.readHeader, ReadTimeout: networkLimits.read,
		IdleTimeout: networkLimits.idle, MaxHeaderBytes: maxHeaderBytes, Protocols: protocols,
	}
}

// serveServer runs until the context ends. Like solo's daemon it has no shutdown step that correctness
// needs, because the control plane is crash-only.
func serveServer(ctx context.Context, paths api.Paths, opts serverOptions, transport serverTransport, stdout, stderr io.Writer) error {
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
	controlListener, err := listenControl(paths.ControlSocket())
	if err != nil {
		return err
	}
	defer controlListener.Close()
	listener, err := transport.listen(opts.listen, paths.StateDir, clock.System{}, func(err error) { warnTo(stderr)(err.Error()) })
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
		Installed: installed.Installed, WebAddress: listener.webAddress(),
		Attempts: server.NewAttempts(server.AttemptLimits{MaxFailures: opts.signinMax, Lockout: opts.signinLockout}),
		Matcher:  lexical.Matcher{Store: store}, Matching: func(store ports.Store) ports.Matcher { return lexical.Matcher{Store: store} },
		Identity: localauth.New(store, localauth.DefaultParams), Batches: batches, Windows: windows, PackTokens: soloPackTokens(paths),
		LaneBranchPrefix: soloLaneBranchPrefix(paths),
		Knowledge:        reads.knowledge, Starts: reads.starts, Branches: reads.branches, Checkouts: reads.checkouts,
		ParseManifest: reads.parseManifest, Panes: reads.panes,
	}
	controlServer := &http.Server{Handler: server.New(config), ReadHeaderTimeout: 10 * time.Second}
	networkServer := networkHTTPServer(server.NewNetwork(config, server.WebOptions{Port: bound.Port, Assets: assets}))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	serveErr := make(chan error, 2)
	go func() { serveErr <- controlServer.Serve(controlListener) }()
	go func() { serveErr <- networkServer.Serve(listener) }()
	fmt.Fprintf(stdout, "ropeshiftd: serving %s\n", paths.ControlSocket())
	fmt.Fprintf(stdout, "ropeshiftd: server %s://%s\n", listener.scheme(), bound)
	if listener.fingerprint != "" {
		fmt.Fprintf(stdout, "ropeshiftd: certificate authority %s\n", listener.authorityFile)
		fmt.Fprintf(stdout, "ropeshiftd: certificate authority sha256 %s\n", listener.fingerprint)
	}

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
