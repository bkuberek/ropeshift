package entry

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/clock"
	"ropeshift.dev/ropeshift/internal/adapters/repofiles"
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/adapters/workerlink"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/api/server"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/worker"
)

// soloScope is the one implicit workspace of solo mode.
const soloScope = ledger.WorkspaceID("default")

const lockFileName = "ropeshiftd.lock"

// exitBusy says another daemon holds this installation.
const exitBusy = 3

// tickActor is who a tick's records name.
func tickActor(name string) ledger.Actor {
	return ledger.Actor{Kind: ledger.ActorSystem, ID: name}
}

// soloOptions is the command line of `ropeshiftd solo`.
type soloOptions struct {
	sources    repeatedFlag
	ref        string
	directory  string
	leaseTick  time.Duration
	intakeTick time.Duration
	leaseTTL   time.Duration
	heartbeat  time.Duration
}

type repeatedFlag []string

func (r *repeatedFlag) String() string { return strings.Join(*r, ",") }

func (r *repeatedFlag) Set(value string) error {
	*r = append(*r, value)
	return nil
}

func parseSolo(args []string, stderr io.Writer) (soloOptions, bool) {
	var opts soloOptions
	flags := flag.NewFlagSet("ropeshiftd solo", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Var(&opts.sources, "source", "a repository to read work from, repeatable")
	flags.StringVar(&opts.ref, "ref", "", "the ref intake reads, by default the default branch")
	flags.StringVar(&opts.directory, "dir", "", "the directory intake reads, by default the whole repository")
	flags.DurationVar(&opts.leaseTick, "lease-tick", 5*time.Second, "how often expired leases lapse")
	flags.DurationVar(&opts.intakeTick, "intake-tick", 30*time.Second, "how often sources are polled")
	flags.DurationVar(&opts.leaseTTL, "lease-ttl", 60*time.Second, "the policy's lease")
	flags.DurationVar(&opts.heartbeat, "heartbeat", 15*time.Second, "how often the worker beats")
	if err := flags.Parse(args); err != nil {
		return opts, false
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "ropeshiftd solo: unexpected argument %q\n", flags.Arg(0))
		return opts, false
	}
	return opts, true
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

// holdInstallation takes the lock that makes this daemon the only one. The kernel
// releases it when the process dies, so a crash leaves nothing to clean up.
func holdInstallation(stateDir string) (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(stateDir, lockFileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, errBusy
	}
	return file, nil
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
	httpServer := &http.Server{
		Handler: server.New(server.Config{
			Scope: soloScope, Store: store, LeaseTTL: opts.leaseTTL.Milliseconds(), HeartbeatMs: opts.heartbeat.Milliseconds(),
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(listener) }()
	fmt.Fprintf(stdout, "ropeshiftd: serving %s\n", paths.ControlSocket())

	ticks := soloTicks(store, opts)
	ticksDone := make(chan struct{})
	go func() {
		defer close(ticksDone)
		control.RunTicks(ctx, ticks, func(name string, err error) {
			fmt.Fprintf(stderr, "ropeshiftd: %s tick: %v\n", name, err)
		})
	}()
	workerDone, err := startSoloWorker(ctx, paths, store, opts, stderr)
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
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// startSoloWorker runs the worker in this process, on the real protocol over the
// private socket, so solo mode exercises the path a group's worker takes. A first
// start makes its own join token, because the person who started the daemon owns
// the control plane.
func startSoloWorker(ctx context.Context, paths api.Paths, store ports.Store, opts soloOptions, stderr io.Writer) (<-chan struct{}, error) {
	journal, err := sqlite.OpenJournal(filepath.Join(paths.StateDir, api.JournalFileName))
	if err != nil {
		return nil, err
	}
	if err := journal.Migrate(ctx); err != nil {
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
		Capabilities: map[string]string{"mode": "solo"},
		Report: func(err error) {
			fmt.Fprintf(stderr, "ropeshiftd: worker: %v\n", err)
		},
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer journal.Close()
		defer client.Close()
		if err := subject.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(stderr, "ropeshiftd: worker: %v\n", err)
		}
	}()
	return done, nil
}

func soloTicks(store ports.Store, opts soloOptions) []control.Tick {
	leases := control.NewLeases(store)
	intake := control.NewIntake(store)
	reviews := control.NewReviews(store)
	sources := make([]ports.WorkSource, 0, len(opts.sources))
	for _, path := range opts.sources {
		sources = append(sources, repofiles.New(repofiles.Config{Repository: path, Ref: opts.ref, Directory: opts.directory}))
	}
	return []control.Tick{
		{
			Name:  "lease",
			Every: opts.leaseTick,
			Run: func(ctx context.Context) error {
				_, err := leases.Sweep(ctx, soloScope, tickActor("lease-tick"))
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
				if _, err := reviews.Promote(ctx, soloScope, tickActor("intake-tick")); err != nil {
					failed = append(failed, err)
				}
				return errors.Join(failed...)
			},
		},
	}
}
