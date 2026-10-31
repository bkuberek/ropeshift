//go:build postgres

// Package pgtest runs a private Postgres for the contract suite. It starts its own
// server in a temporary directory, on a unix socket with no TCP listener, and it
// never connects to a server it did not start.
package pgtest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	// The driver registers itself as "pgx".
	_ "github.com/jackc/pgx/v5/stdlib"
)

// BinEnv names the directory of the Postgres binaries. Without it the binaries
// come from PATH.
const BinEnv = "ROPESHIFT_TEST_PG_BIN"

const (
	socketFile = ".s.PGSQL.5432"
	user       = "ropeshift_test"
	// socketLimit stays under the 104 bytes macOS allows in a socket path.
	socketLimit  = 100
	stopDeadline = 30 * time.Second
)

// Server is one private Postgres.
type Server struct {
	dir     string
	data    string
	cmd     *exec.Cmd
	done    chan struct{}
	counter atomic.Int64
}

func binary(name string) (string, error) {
	if dir := os.Getenv(BinEnv); dir != "" {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("%s: %w", BinEnv, err)
		}
		return path, nil
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s not found: set %s or add it to PATH: %w", name, BinEnv, err)
	}
	return path, nil
}

// runDir makes the temporary directory. The socket lives inside it, so a deep
// temporary root falls back to a short one.
func runDir() (string, error) {
	base := os.TempDir()
	if len(filepath.Join(base, "rspg0000000000", socketFile)) > socketLimit {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "rspg")
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(dir)
}

// Start creates and starts a server.
func Start(ctx context.Context) (*Server, error) {
	initdb, err := binary("initdb")
	if err != nil {
		return nil, err
	}
	postgres, err := binary("postgres")
	if err != nil {
		return nil, err
	}
	dir, err := runDir()
	if err != nil {
		return nil, err
	}
	s := &Server{dir: dir, data: filepath.Join(dir, "data"), done: make(chan struct{})}
	out, err := exec.CommandContext(ctx, initdb, "-D", s.data, "-U", user, "--auth=trust",
		"-N", "-E", "UTF8", "--locale=C").CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("initdb: %w: %s", err, out)
	}
	if err := s.start(ctx, postgres); err != nil {
		_ = s.Stop()
		return nil, err
	}
	return s, nil
}

func (s *Server) start(ctx context.Context, postgres string) error {
	log, err := os.OpenFile(filepath.Join(s.dir, "server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	// An empty listen_addresses means no TCP listener. These databases are
	// thrown away, so fsync adds only time.
	cmd := exec.Command(postgres, "-D", s.data, "-k", s.dir,
		"-c", "listen_addresses=", "-c", "max_connections=200",
		"-c", "unix_socket_permissions=0700", "-c", "fsync=off")
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	s.cmd = cmd
	go func() {
		_ = cmd.Wait()
		close(s.done)
	}()
	s.watch(cmd.Process.Pid)
	return s.waitReady(ctx)
}

// watchdog outlives a test binary that panics on its timeout and so runs no
// cleanup. It stops the server with SIGQUIT and removes the run directory.
const watchdog = `
owner=$1; server=$2; dir=$3
while kill -0 "$owner" 2>/dev/null && kill -0 "$server" 2>/dev/null; do sleep 1; done
kill -0 "$owner" 2>/dev/null && exit 0
kill -QUIT "$server" 2>/dev/null
while kill -0 "$server" 2>/dev/null; do sleep 1; done
rm -rf "$dir"
`

func (s *Server) watch(server int) {
	cmd := exec.Command("sh", "-c", watchdog, "ropeshift-pgtest-watchdog",
		strconv.Itoa(os.Getpid()), strconv.Itoa(server), s.dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}

func (s *Server) waitReady(ctx context.Context) error {
	db, err := sql.Open("pgx", s.DSN("postgres"))
	if err != nil {
		return err
	}
	defer db.Close()
	deadline := time.Now().Add(30 * time.Second)
	for {
		probe, cancel := context.WithTimeout(ctx, time.Second)
		err := db.PingContext(probe)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("private Postgres did not start: %w", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// DSN is the connection string of a database on this server.
func (s *Server) DSN(database string) string {
	return fmt.Sprintf("host=%s port=5432 user=%s dbname=%s sslmode=disable", s.dir, user, database)
}

var databaseName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)

// NewDatabase creates an empty database and returns its name.
func (s *Server) NewDatabase(ctx context.Context) (string, error) {
	name := fmt.Sprintf("contract_%d", s.counter.Add(1))
	if !databaseName.MatchString(name) {
		return "", fmt.Errorf("bad database name %q", name)
	}
	admin, err := sql.Open("pgx", s.DSN("postgres"))
	if err != nil {
		return "", err
	}
	defer admin.Close()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		return "", err
	}
	return name, nil
}

// segment reads the SysV shared memory ID from postmaster.pid, or "" if the
// server left none.
func (s *Server) segment() string {
	raw, err := os.ReadFile(filepath.Join(s.data, "postmaster.pid"))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) < 7 {
		return ""
	}
	fields := strings.Fields(lines[6])
	if len(fields) != 2 {
		return ""
	}
	return fields[1]
}

func segmentExists(id string) bool {
	out, err := exec.Command("ipcs", "-m").Output()
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && fields[1] == id {
			return true
		}
	}
	return false
}

// Stop sends SIGQUIT, which is Postgres's immediate shutdown and releases its
// shared memory. SIGKILL would leak a SysV segment, and those are a small
// system-wide resource, so a server that outlives the deadline is reported and
// never killed.
func (s *Server) Stop() error {
	var failure error
	if s.cmd != nil {
		segment := s.segment()
		if err := s.cmd.Process.Signal(syscall.SIGQUIT); err != nil && !errors.Is(err, os.ErrProcessDone) {
			failure = err
		}
		select {
		case <-s.done:
		case <-time.After(stopDeadline):
			return fmt.Errorf("private Postgres %d did not stop on SIGQUIT", s.cmd.Process.Pid)
		}
		if segment != "" && segmentExists(segment) {
			failure = errors.Join(failure, fmt.Errorf("shared memory segment %s outlived the server", segment))
		}
	}
	return errors.Join(failure, os.RemoveAll(s.dir))
}
