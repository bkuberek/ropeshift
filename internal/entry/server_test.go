package entry

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/apitest"
)

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// A server's two settings come from its flags, and from the environment a container gives it, and a flag
// wins over the environment.
func TestAServerTakesItsSettingsFromFlagsAndTheEnvironment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		env      map[string]string
		database string
		listen   string
	}{
		{"the defaults", []string{"--database", "postgres://db/x"}, nil, "postgres://db/x", "127.0.0.1:7400"},
		{"the environment", nil, map[string]string{"ROPESHIFT_DATABASE": "postgres://env/x", "ROPESHIFT_LISTEN": "127.0.0.1:9000"}, "postgres://env/x", "127.0.0.1:9000"},
		{"a flag over the environment's address", []string{"--listen", "[::1]:9100"}, map[string]string{"ROPESHIFT_DATABASE": "postgres://env/x", "ROPESHIFT_LISTEN": "127.0.0.1:9000"}, "postgres://env/x", "[::1]:9100"},
		{"the database by a flag", []string{"--database", "postgres://flag/x"}, map[string]string{"ROPESHIFT_LISTEN": "127.0.0.1:9000"}, "postgres://flag/x", "127.0.0.1:9000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			opts, ok := parseServer(tt.args, env(tt.env), &stderr)
			if !ok {
				t.Fatalf("refused: %s", &stderr)
			}
			if opts.database != tt.database || opts.listen != tt.listen {
				t.Errorf("database %q, listen %q", opts.database, opts.listen)
			}
		})
	}
}

func TestAServerRefusesWhatItDoesNotTake(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--database", "x", "stray"}, {"--database", "x", "--source", "/repo"}, {"--no-such-flag"}} {
		var stderr bytes.Buffer
		if _, ok := parseServer(args, env(nil), &stderr); ok {
			t.Errorf("%v is taken", args)
		}
	}
}

// This version binds loopback only, because nothing yet encrypts what a worker or a member sends.
func TestAServerListensOnLoopbackOnly(t *testing.T) {
	t.Parallel()
	for _, address := range []string{"127.0.0.1:7400", "127.0.0.1:0", "localhost:7400", "[::1]:7400", "127.1.2.3:80"} {
		if err := checkListen(address); err != nil {
			t.Errorf("%s is refused: %v", address, err)
		}
	}
	for _, address := range []string{"0.0.0.0:7400", "[::]:7400", ":7400", "192.168.1.5:7400", "10.0.0.1:7400", "server.example:7400", "127.0.0.1.example:7400"} {
		err := checkListen(address)
		if err == nil || err.Error() != "a server off loopback needs TLS, which this version does not have" {
			t.Errorf("%s answers %v", address, err)
		}
	}
	if err := checkListen("no port"); err == nil {
		t.Error("an address with no port is taken")
	}
}

// A server whose state directory is not the machine's own, and whose run directory is, would bind its control
// socket where another installation's lives, and the lock it holds is the lock of its own state. So a server
// that moves its state names its run directory too, and one that names it, or moves nothing, is left alone.
func TestAServerThatMovesItsStateNamesItsRunDirectory(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     map[string]string
		refused bool
	}{
		{"nothing moved", map[string]string{}, false},
		{"both moved", map[string]string{"ROPESHIFT_STATE_DIR": "/srv/state", "ROPESHIFT_RUN_DIR": "/srv/run"}, false},
		{"only the run directory moved", map[string]string{"ROPESHIFT_RUN_DIR": "/srv/run"}, false},
		{"only the state moved", map[string]string{"ROPESHIFT_STATE_DIR": "/srv/state"}, true},
		// The XDG directories are the machine's own, and every daemon of the user reads them alike, so the state
		// they move is the one a solo daemon holds the lock of.
		{"the machine's own directories moved by XDG", map[string]string{"XDG_STATE_HOME": "/srv/xdg-state", "XDG_RUNTIME_DIR": "/srv/xdg-run"}, false},
		{"only the worktrees moved", map[string]string{"ROPESHIFT_WORKTREE_DIR": "/srv/trees"}, false},
	}
	for _, goos := range []string{"linux", "darwin"} {
		for _, tt := range tests {
			t.Run(goos+" "+tt.name, func(t *testing.T) {
				err := checkServerDirectories(goos, env(tt.env), "/home/person")
				if (err != nil) != tt.refused {
					t.Fatalf("refused is %v: %v", err != nil, err)
				}
				if err != nil && !strings.Contains(err.Error(), "ROPESHIFT_RUN_DIR") {
					t.Errorf("the refusal does not say what to set: %v", err)
				}
			})
		}
	}
}

// The refusal comes before the server makes a directory, takes a lock, or opens a socket, so a mistaken
// environment costs the machine's own installation nothing.
func TestAServerThatMovesItsStateTouchesNothingOfTheDefaultInstallation(t *testing.T) {
	home := apitest.ShortDir(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "xdg-run"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "xdg-state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg-data"))
	t.Setenv("ROPESHIFT_STATE_DIR", filepath.Join(home, "moved-state"))
	t.Setenv("ROPESHIFT_RUN_DIR", "")
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	code, _, stderr := runMain(commandDaemon, "server", "--database", "host=/nowhere dbname=none")
	if code != exitUsage || !strings.Contains(stderr, "ROPESHIFT_RUN_DIR") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, path := range []string{filepath.Join(home, "xdg-run"), filepath.Join(home, ".ropeshift"), filepath.Join(home, "moved-state"), filepath.Join(home, "xdg-state")} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("a refused server made %s", path)
		}
	}
}

// A control socket something answers on is another daemon's, whoever's directory it is in, and the server leaves it
// as it is: it never takes the path of a live socket, which binding would do by removing it first. The check and
// the bind are one step, so no daemon that starts between them has its socket removed.
func TestTheControlSocketOfALiveDaemonIsNeverTaken(t *testing.T) {
	t.Parallel()
	socket := filepath.Join(apitest.ShortDir(t), "run", "control.sock")
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	live, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })
	before, err := os.Stat(socket)
	if err != nil {
		t.Fatal(err)
	}
	taken, err := listenControl(socket)
	if err == nil {
		_ = taken.Close()
		t.Fatal("a live daemon's socket was bound over")
	}
	if !strings.Contains(err.Error(), "answers") {
		t.Errorf("the refusal says %q", err)
	}
	after, err := os.Stat(socket)
	if err != nil || !os.SameFile(before, after) {
		t.Errorf("the live daemon's socket was replaced: %v", err)
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("the live daemon's socket does not answer: %v", err)
	}
	_ = conn.Close()
}

// A path nothing answers on is free: a file a daemon left when it died is replaced, and a path with nothing at it is
// bound, in a socket only its user reaches.
func TestTheControlSocketOfNoLiveDaemonIsBound(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(apitest.ShortDir(t), "run")
	socket := filepath.Join(dir, "control.sock")
	fresh, err := listenControl(socket)
	if err != nil {
		t.Fatalf("a path with nothing at it: %v", err)
	}
	if info, err := os.Stat(socket); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the socket is %v, %v", info, err)
	}
	fresh.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = fresh.Close()
	again, err := listenControl(socket)
	if err != nil {
		t.Fatalf("a socket file nothing listens on: %v", err)
	}
	_ = again.Close()
}

// The check is the last step before the bind, after the server has opened its database, which is the longest
// thing it does between taking its lock and binding. A server that cannot reach its database reports that, and
// has looked at no socket.
func TestTheLiveSocketCheckComesAfterTheDatabaseIsOpen(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	paths, err := installationPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.RunDir, 0o700); err != nil {
		t.Fatal(err)
	}
	live, err := net.Listen("unix", paths.ControlSocket())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })
	code, _, stderr := runMain(commandDaemon, "server", "--database", "host=/nowhere dbname=none")
	if code != exitError || strings.Contains(stderr, "answers") || !strings.Contains(stderr, "postgres") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

// Only a socket something answers on is live: a file a daemon that died left, and no file at all, are free.
func TestOnlyASocketSomethingAnswersOnIsLive(t *testing.T) {
	t.Parallel()
	dir := apitest.ShortDir(t)
	path := filepath.Join(dir, "control.sock")
	if socketAnswers(path) {
		t.Error("a path with nothing at it answers")
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if !socketAnswers(path) {
		t.Error("a socket with a daemon behind it does not answer")
	}
	// A listener that closes through its Go value removes the file, so a leftover is made by keeping the file.
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = listener.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no leftover: %v", err)
	}
	if socketAnswers(path) {
		t.Error("a socket nothing listens on answers")
	}
}

// The refusal comes before the server touches a database, a lock, or a socket, so a mistaken address costs nothing.
func TestAServerOffLoopbackIsRefusedBeforeItConnects(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	code, stdout, stderr := runMain(commandDaemon, "server", "--database", "host=/nowhere dbname=none", "--listen", "0.0.0.0:7400")
	if code != exitUsage || stdout != "" {
		t.Fatalf("exit %d, stdout %q", code, stdout)
	}
	if !strings.Contains(stderr, "a server off loopback needs TLS, which this version does not have") {
		t.Errorf("stderr %q", stderr)
	}
}

// The connection string holds the database's password, so neither the help nor an error about the command line
// shows what the environment gives it, and the environment still reaches the server after the flags are read.
func TestAServersHelpAndUsageErrorsNeverShowTheDatabaseURL(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	const url = "postgres://ropeshift:hunter2-the-password@db.internal/ropeshift"
	t.Setenv("ROPESHIFT_DATABASE", url)
	for _, args := range [][]string{
		{"server", "--help"}, {"server", "-h"}, {"help", "server"}, {"server", "--no-such-flag"},
		{"server", "stray"}, {"server", "--listen"}, {"server", "--lease-tick", "soon"},
	} {
		_, stdout, stderr := runMain(append([]string{commandDaemon}, args...)...)
		for _, shown := range []string{stdout, stderr} {
			if strings.Contains(shown, "hunter2") || strings.Contains(shown, url) {
				t.Errorf("%v shows the database's password:\n%s", args, shown)
			}
		}
	}
	opts, ok := parseServer(nil, env(map[string]string{"ROPESHIFT_DATABASE": url}), io.Discard)
	if !ok || opts.database != url {
		t.Errorf("the environment gives the server %q, %t", opts.database, ok)
	}
	opts, ok = parseServer([]string{"--database", "host=flag"}, env(nil), io.Discard)
	if !ok || opts.database != "host=flag" {
		t.Errorf("a flag gives the server %q, %t", opts.database, ok)
	}
}

// Two ways to give one connection string leave no way to say which is meant, so a flag does not quietly win
// over the environment: the server refuses both, as a worker does for its join token, and names neither value.
func TestTheDatabaseGivenTwiceIsRefused(t *testing.T) {
	var stderr bytes.Buffer
	if _, ok := parseServer([]string{"--database", "host=flag password=from-the-flag"}, env(map[string]string{"ROPESHIFT_DATABASE": "host=env password=from-the-environment"}), &stderr); ok {
		t.Fatal("a flag and the environment together are taken")
	}
	if !strings.Contains(stderr.String(), "ROPESHIFT_DATABASE") || !strings.Contains(stderr.String(), "--database") {
		t.Errorf("the refusal does not name both ways: %q", &stderr)
	}
	if strings.Contains(stderr.String(), "from-the") || strings.Contains(stderr.String(), "host=") {
		t.Errorf("the refusal repeats a connection string: %q", &stderr)
	}
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	t.Setenv("ROPESHIFT_DATABASE", "host=env password=from-the-environment")
	code, stdout, shown := runMain(commandDaemon, "server", "--database", "host=flag password=from-the-flag")
	if code != exitUsage || stdout != "" || strings.Contains(shown, "from-the") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, shown)
	}
}

// pgx hides a password in the error for a connection string that does not parse only by matching a pattern, and a
// pattern misses a password with a space or a quote in it. So the server parses the string first, and says only
// that it does not parse, never what it read.
func TestAConnectionStringThatDoesNotParseIsRefusedWithAFixedMessage(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	for _, bad := range []string{
		"host=h port=notaport password='correct horse battery staple'",
		"host=h password='a b c d' port=99999999",
		`host=h password="open sesame" sslmode=nonsense`,
		"postgres://user:p%40ss%20word-and-more@host:notaport/db",
		"postgres://user:the-secret@host/db?sslmode=nonsense",
		"host=h password=the-secret unknownoption=1 port=x",
	} {
		t.Setenv("ROPESHIFT_DATABASE", bad)
		code, stdout, stderr := runMain(commandDaemon, "server")
		if code != exitUsage || stdout != "" {
			t.Errorf("%q: exit %d, stdout %q, stderr %q", bad, code, stdout, stderr)
		}
		for _, secret := range []string{"correct", "horse", "battery", "staple", "sesame", "open", "word-and-more", "p%40ss", "the-secret", "a b c d", "notaport", "nonsense"} {
			if strings.Contains(stderr, secret) {
				t.Errorf("%q: the refusal shows %q: %s", bad, secret, stderr)
			}
		}
		if !strings.Contains(stderr, "does not parse") {
			t.Errorf("%q: the refusal says %q", bad, stderr)
		}
	}
}

func TestAServerNeedsADatabase(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	t.Setenv("ROPESHIFT_DATABASE", "")
	code, _, stderr := runMain(commandDaemon, "server")
	if code != exitUsage || !strings.Contains(stderr, "ROPESHIFT_DATABASE") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

// What the Postgres adapter goes on past reaches the server's own log, as a migration's warning reaches
// solo's, so an operator sees a schema the sweep dropped or an index it reconciled.
func TestWhatTheDatabaseWarnsOfReachesTheServersLog(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	options := serverDatabase(&stderr)
	if options.Store.Warn == nil {
		t.Fatal("the adapter's warnings go nowhere")
	}
	options.Store.Warn("dropped the schema project_gone")
	if got, want := stderr.String(), "ropeshiftd: warning: dropped the schema project_gone\n"; got != want {
		t.Errorf("the log holds %q, want %q", got, want)
	}
}

// The listener's server bounds every read: the headers, the whole request, and the time a connection sits idle,
// so a client that sends a request a byte at a time, or none, holds nothing for long.
func TestTheNetworkServerBoundsEveryRead(t *testing.T) {
	s := networkHTTPServer(http.NotFoundHandler())
	if s.ReadHeaderTimeout <= 0 || s.ReadTimeout <= 0 || s.IdleTimeout <= 0 || s.MaxHeaderBytes <= 0 {
		t.Fatalf("the server's bounds are header %s, read %s, idle %s, header bytes %d", s.ReadHeaderTimeout, s.ReadTimeout, s.IdleTimeout, s.MaxHeaderBytes)
	}
	if s.ReadHeaderTimeout > s.ReadTimeout || s.ReadTimeout > 10*time.Minute || s.IdleTimeout > 10*time.Minute {
		t.Errorf("the bounds are not generous but bounded: header %s, read %s, idle %s", s.ReadHeaderTimeout, s.ReadTimeout, s.IdleTimeout)
	}
}

// With the bounds short, a request whose headers or body trickle in is cut off, and a request that arrives whole
// is answered.
func TestASlowRequestToTheNetworkServerIsCutOff(t *testing.T) {
	was := networkLimits
	networkLimits = serverLimits{readHeader: 300 * time.Millisecond, read: 600 * time.Millisecond, idle: time.Second}
	t.Cleanup(func() { networkLimits = was })
	listener := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			http.Error(w, "cut off", http.StatusRequestTimeout)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	listener.Config = networkHTTPServer(listener.Config.Handler)
	listener.Start()
	t.Cleanup(listener.Close)
	address := listener.Listener.Addr().String()
	for _, test := range []struct{ name, sent string }{
		{"headers", "POST /x HTTP/1.1\r\nHost: " + address + "\r\nX-Slow: "},
		{"a body", "POST /x HTTP/1.1\r\nHost: " + address + "\r\nContent-Length: 1000\r\n\r\n{\"a\":"},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn, err := net.Dial("tcp", address)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err := io.WriteString(conn, test.sent); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			if err := conn.SetReadDeadline(started.Add(10 * time.Second)); err != nil {
				t.Fatal(err)
			}
			_, _ = io.ReadAll(conn)
			if waited := time.Since(started); waited > 5*time.Second {
				t.Errorf("the server held a half-sent request for %s", waited)
			}
		})
	}
	response, err := http.Post(listener.URL+"/x", "application/json", strings.NewReader(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Errorf("a request that arrives whole is answered %d", response.StatusCode)
	}
}

// The time a request has to arrive in is no limit on a stream the server answers it with: a worker's watch and
// a member's event stream go on past it.
func TestTheReadTimeoutEndsNoStream(t *testing.T) {
	was := networkLimits
	networkLimits = serverLimits{readHeader: 200 * time.Millisecond, read: 300 * time.Millisecond, idle: time.Second}
	t.Cleanup(func() { networkLimits = was })
	streaming := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		flusher.Flush()
		select {
		case <-time.After(1200 * time.Millisecond):
			_, _ = io.WriteString(w, "data: late\n\n")
			flusher.Flush()
		case <-r.Context().Done():
		}
	}))
	streaming.Config = networkHTTPServer(streaming.Config.Handler)
	streaming.Start()
	t.Cleanup(streaming.Close)
	response, err := http.Get(streaming.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), "late") {
		t.Errorf("the stream was cut at the read timeout: %q", body)
	}
}

// emptyProjects is an installation with no project, which a tick that reads each project finds nothing in.
type emptyProjects struct{ ports.Projects }

func (emptyProjects) List(context.Context) ([]ledger.Project, error) { return nil, nil }

// soloTickNames is every tick solo's daemon runs, by name: the control plane's own and the ones it adds in
// serveSolo, each built as serveSolo builds it.
func soloTickNames(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, tick := range soloTicks(emptyProjects{}, soloScope, soloOptions{}, nil, questions.BatchPolicy{}, nil) {
		names = append(names, tick.Name)
	}
	spans, err := (&soloTelemetry{}).spansTick(t.Context(), emptyProjects{})
	if err != nil {
		t.Fatal(err)
	}
	extras := []string{
		noticeTick(server.Config{}, emptyProjects{}, notifyDeps{}, time.Second).Name,
		spans.Name,
		(&soloTelemetry{}).measuresTick(emptyProjects{}).Name,
		liveInstalledBuild(io.Discard).tick().Name,
	}
	return append(names, extras...)
}

// The server runs every control-plane tick solo runs, by name, except the ones that act on the machine of a
// worker beside it: the two wirings are two lists, and this is what keeps them from drifting.
func TestServerRunsEverySoloControlTick(t *testing.T) {
	t.Parallel()
	solo := soloTickNames(t)
	for _, name := range workerMachineTicks {
		if !slices.Contains(solo, name) {
			t.Errorf("%q is excluded as a tick of the worker's machine, and solo runs no such tick", name)
		}
	}
	want := slices.DeleteFunc(slices.Clone(solo), func(name string) bool { return slices.Contains(workerMachineTicks, name) })
	var got []string
	for _, tick := range serverTicks(emptyProjects{}, serverOptions{}, unavailableServerReads(), questions.BatchPolicy{}, nil) {
		got = append(got, tick.Name)
	}
	got = append(got, liveInstalledBuild(io.Discard).tick().Name)
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Errorf("solo runs %v, less %v, and the server runs %v", solo, workerMachineTicks, got)
	}
	if len(want) < 12 {
		t.Errorf("only %d ticks were compared", len(want))
	}
}

// Every tick of the server runs more often than never, and none shares a name with another.
func TestServerTicksHaveNamesAndIntervals(t *testing.T) {
	t.Parallel()
	opts, ok := parseServer([]string{"--database", "x"}, env(nil), io.Discard)
	if !ok {
		t.Fatal("refused")
	}
	seen := map[string]bool{}
	for _, tick := range serverTicks(emptyProjects{}, opts, unavailableServerReads(), questions.BatchPolicy{}, nil) {
		if tick.Name == "" || tick.Every <= 0 || tick.Run == nil {
			t.Errorf("tick %+v", tick)
		}
		if seen[tick.Name] {
			t.Errorf("two ticks are named %q", tick.Name)
		}
		seen[tick.Name] = true
	}
}

// Group commands start only with the preview on: without it they exit 2 with the refusal, whatever else the
// command line holds, and the help they answer to is the same as for a command that does not exist.
func TestAGroupCommandRefusesWithoutThePreviewGate(t *testing.T) {
	isolate(t)
	for _, value := range []string{"", "yes", "1", "true", "Preview", "preview "} {
		t.Run("value "+value, func(t *testing.T) {
			t.Setenv("ROPESHIFT_GROUP_MODE", value)
			for _, args := range [][]string{
				{"server"}, {"server", "--database", "x"}, {"server", "--help"}, {"server", "--listen", "0.0.0.0:1"},
			} {
				code, stdout, stderr := runMain(append([]string{commandDaemon}, args...)...)
				if code != exitUsage || stdout != "" || !strings.Contains(stderr, groupRefusal) {
					t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, stdout, stderr)
				}
			}
			code, _, stderr := runMain(commandDaemon, "help", "server")
			if code != exitUsage || !strings.Contains(stderr, "unknown command") {
				t.Errorf("help server: exit %d, stderr %q", code, stderr)
			}
		})
	}
}

// With the gate off, help is what it was before group mode: the list a binary prints is its public routes
// and nothing more. With it on, the group commands are listed.
func TestGroupCommandsAreListedOnlyWithThePreviewGate(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "")
	_, off, _ := runMain(commandDaemon, "help")
	var public bytes.Buffer
	var plain []cli.Group
	for _, group := range routes(commandDaemon) {
		listed := cli.Group{Title: group.title}
		for _, r := range group.routes {
			listed.Commands = append(listed.Commands, r.Command)
		}
		plain = append(plain, listed)
	}
	cli.WriteList(&public, commandDaemon, plain)
	if off != public.String() {
		t.Errorf("help with the gate off is\n%s\nand the public routes list\n%s", off, public.String())
	}
	if listed(off, "server") || listed(off, "worker") {
		t.Errorf("help lists a group command with the gate off:\n%s", off)
	}
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	_, on, _ := runMain(commandDaemon, "help")
	if !listed(on, "server") {
		t.Errorf("help lists no server with the gate on:\n%s", on)
	}
	for _, command := range []string{"solo", "help", "version"} {
		if !listed(on, command) {
			t.Errorf("help with the gate on lists no %s:\n%s", command, on)
		}
	}
	// The CLI's help is the CLI's, with or without the gate.
	t.Setenv("ROPESHIFT_GROUP_MODE", "")
	_, cliOff, _ := runMain(commandCLI, "help")
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	_, cliOn, _ := runMain(commandCLI, "help")
	if cliOff != cliOn {
		t.Errorf("the CLI's help changes with the gate")
	}
}

func TestAGroupCommandAnswersHelpWithTheGateOn(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	_, usage, _ := runMain(commandDaemon, "server", "--help")
	for _, want := range []string{"usage: ropeshiftd server", "-database", "-listen", "ROPESHIFT_DATABASE", "ROPESHIFT_LISTEN", "ROPESHIFT_GROUP_MODE=preview"} {
		if !strings.Contains(usage, want) {
			t.Errorf("the usage of server lacks %q:\n%s", want, usage)
		}
	}
	code, viaHelp, _ := runMain(commandDaemon, "help", "server")
	if code != exitOK || viaHelp != usage {
		t.Errorf("help server prints exit %d\n%s", code, viaHelp)
	}
}
