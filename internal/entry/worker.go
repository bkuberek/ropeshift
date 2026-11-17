package entry

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/workerlink"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// A group worker is the worker of solo mode on its own (ADR-0031): its lanes, terminal, checkouts, drivers,
// hooks, and MCP on this machine's worker socket, and a link to a server over HTTP. It runs the code
// startSoloWorker runs, over another link, and holds neither a ledger nor a tick that decides. It joins a
// server once, with a join token a member made, and keeps the server's address and its credential in its
// journal.

// workerOptions is the command line of `ropeshiftd worker`.
type workerOptions struct {
	telemetry string
	forgeTick time.Duration
}

func workerFlags(opts *workerOptions) *flag.FlagSet {
	flags := flag.NewFlagSet("ropeshiftd worker", flag.ContinueOnError)
	flags.StringVar(&opts.telemetry, "telemetry", cmp.Or(os.Getenv(api.EnvTelemetry), api.DefaultTelemetryAddress), "the loopback address of the OTLP receiver, or off")
	flags.DurationVar(&opts.forgeTick, "forge-tick", time.Minute, "how often the worker reads gh's sign-in and watched pull requests")
	return flags
}

// joinOptions is the command line of `ropeshiftd worker join`.
type joinOptions struct {
	server string
	name   string
	token  string
}

func joinFlags(opts *joinOptions) *flag.FlagSet {
	flags := flag.NewFlagSet("ropeshiftd worker join", flag.ContinueOnError)
	flags.StringVar(&opts.server, "server", "", "the server's address, a loopback http:// URL in this version")
	flags.StringVar(&opts.name, "name", "", "the worker's name in the server's list, by default this machine's host name")
	return flags
}

// runWorker is `ropeshiftd worker`, and `ropeshiftd worker join` when its first argument says so.
func runWorker(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "join" {
		return runJoin(args[1:], stdout, stderr)
	}
	var opts workerOptions
	flags := workerFlags(&opts)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "ropeshiftd worker: unexpected argument %q\n", flags.Arg(0))
		return exitUsage
	}
	paths, err := installationPaths()
	if err != nil {
		fmt.Fprintf(stderr, "ropeshiftd worker: %v\n", err)
		return exitError
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := serveWorker(ctx, paths, opts, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "ropeshiftd worker: %v\n", err)
		if errors.Is(err, errBusy) {
			return exitBusy
		}
		return exitError
	}
	return exitOK
}

// openWorkerJournal opens the worker's journal, which holds its credential, so the file is one only its user
// reads whoever else shares the directory.
func openWorkerJournal(ctx context.Context, paths api.Paths, stderr io.Writer) (*sqlite.Journal, error) {
	if err := server.PrivateDir(paths.StateDir); err != nil {
		return nil, err
	}
	file := filepath.Join(paths.StateDir, api.JournalFileName)
	made, err := os.OpenFile(file, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := made.Close(); err != nil {
		return nil, err
	}
	journal, err := sqlite.OpenJournal(file)
	if err != nil {
		return nil, err
	}
	journal.Warn = warnTo(stderr)
	if err := journal.Migrate(ctx); err != nil {
		_ = journal.Close()
		return nil, err
	}
	return journal, nil
}

func runJoin(args []string, stdout, stderr io.Writer) int {
	var opts joinOptions
	flags := joinFlags(&opts)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if opts.server == "" || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: ropeshiftd worker join --server <url> [--name <name>] <join token>")
		return exitUsage
	}
	opts.token = flags.Arg(0)
	address, err := api.ServerAddress(opts.server)
	if err != nil {
		fmt.Fprintf(stderr, "ropeshiftd worker join: %v\n", err)
		return exitUsage
	}
	paths, err := installationPaths()
	if err != nil {
		fmt.Fprintf(stderr, "ropeshiftd worker join: %v\n", err)
		return exitError
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := joinServer(ctx, paths, address, opts, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "ropeshiftd worker join: %v\n", err)
		if errors.Is(err, errBusy) {
			return exitBusy
		}
		return exitError
	}
	return exitOK
}

// joinServer registers this machine's worker with a server and keeps the answer. The worker's ID is on
// disk before it asks, so a join that stops between the exchange and the saving of its answer repeats the
// same registration, as a worker's first start does. A worker that holds a credential already keeps it: it
// joins the server it joined, and no other.
func joinServer(ctx context.Context, paths api.Paths, address string, opts joinOptions, stdout, stderr io.Writer) error {
	if err := server.PrivateDir(paths.StateDir); err != nil {
		return err
	}
	lock, err := holdInstallation(paths.StateDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	journal, err := openWorkerJournal(ctx, paths, stderr)
	if err != nil {
		return err
	}
	defer journal.Close()
	identity, err := journal.Identity(ctx)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		if identity.WorkerID, err = newWorkerID(); err != nil {
			return err
		}
		if err := journal.SaveIdentity(ctx, identity); err != nil {
			return err
		}
	case err != nil:
		return err
	}
	if identity.Credential != "" {
		joined, err := journal.ServerURL(ctx)
		if err == nil && joined != address {
			return fmt.Errorf("this worker joined %s, and a worker joins one server: remove its state directory to join another", joined)
		}
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return err
		}
	} else {
		client, err := api.NewTCPClient(address)
		if err != nil {
			return err
		}
		defer client.Close()
		name := cmp.Or(opts.name, hostName())
		credential, err := workerlink.New(client).Register(ctx, ledger.Registration{JoinToken: opts.token, WorkerID: identity.WorkerID, Name: name})
		if err != nil {
			return fmt.Errorf("the server refused the join: %w", err)
		}
		identity.Credential = credential.Credential
		if err := journal.SaveIdentity(ctx, identity); err != nil {
			return err
		}
	}
	if err := journal.SaveServerURL(ctx, address); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "ropeshiftd: worker %s joined %s\n", identity.WorkerID, address)
	return nil
}

// newWorkerID makes the ID a worker registers under, as a worker's first start does.
func newWorkerID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// hostName is what a worker is called when its member names it nothing.
func hostName() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "worker"
	}
	return name
}

// joinedServer reads the server a worker joined from its journal, or says how to join one.
func joinedServer(ctx context.Context, paths api.Paths, stderr io.Writer) (string, error) {
	journal, err := openWorkerJournal(ctx, paths, stderr)
	if err != nil {
		return "", err
	}
	defer journal.Close()
	identity, err := journal.Identity(ctx)
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		return "", err
	}
	address, urlErr := journal.ServerURL(ctx)
	if urlErr != nil && !errors.Is(urlErr, ports.ErrNotFound) {
		return "", urlErr
	}
	if identity.Credential == "" || address == "" {
		return "", errors.New("this worker has joined no server: run `ropeshiftd worker join --server <url> <join token>` first")
	}
	return address, nil
}

// serveWorker runs this machine's worker until the context ends. It reaches its server over the link the
// worker of solo mode reaches its own over, and a server that is down costs it nothing it keeps.
func serveWorker(ctx context.Context, paths api.Paths, opts workerOptions, stdout, stderr io.Writer) error {
	if err := server.PrivateDir(paths.StateDir); err != nil {
		return err
	}
	lock, err := holdInstallation(paths.StateDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	address, err := joinedServer(ctx, paths, stderr)
	if err != nil {
		return err
	}
	client, err := api.NewTCPClient(address)
	if err != nil {
		return err
	}
	defer client.Close()
	solo := soloOptions{telemetry: opts.telemetry, forgeTick: opts.forgeTick}
	kept := soloCheckouts(paths)
	laneRuntime, _, err := soloLanes(paths, solo, kept, stderr)
	if err != nil {
		return err
	}
	telemetry, err := startTelemetry(ctx, paths, opts.telemetry, liveTelemetryDeps(defaultSpansEvery), stdout, stderr)
	if err != nil {
		return err
	}
	defer telemetry.close()
	done, err := startSoloWorker(ctx, paths, workerConnection{link: workerlink.New(client), name: hostName(), close: client.Close},
		laneRuntime, telemetry, &soloHosts{kept: kept, runtime: laneRuntime}, &soloWorker{}, stderr)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "ropeshiftd: worker for %s\n", address)
	<-done
	return nil
}
