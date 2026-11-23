package claudecode

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// A pane's CLI connects ropeshift's MCP server in the background, so the session's first prompt can arrive before the
// session holds a tool. The server makes a file in the generation's own directory once it has listed its tools, and the
// driver holds the brief for it: the pane's environment names the file, and the MCP configuration passes that name on to
// the server.
func TestAPaneNamesTheFileItsMCPServerMarksItsToolsIn(t *testing.T) {
	spec := policySpec(t)
	spec.RunMode = ports.RunModeInteractive
	want := filepath.Join(spec.ConfigDir, "tools-listed-1")
	if got := toolsListedPath(spec); got != want {
		t.Fatalf("the marker is %q, want %q", got, want)
	}
	if !slices.Contains(paneEnv(spec, Use{}), lanes.EnvToolsListed+"="+want) {
		t.Errorf("the pane's environment names no marker: %v", paneEnv(spec, Use{}))
	}
	if got := generateMCP(spec).Servers[ServerName].Env[lanes.EnvToolsListed]; got != "${"+lanes.EnvToolsListed+"}" {
		t.Errorf("the server's environment passes on %q", got)
	}
}

// A reference to a variable the session's environment lacks fails the server's configuration, so a session that has no
// marker names none anywhere: a headless one, whose first turn already waits for its servers, one with no directory of
// its own, and one with no server.
func TestASessionWithNoMarkerNamesNoneAnywhere(t *testing.T) {
	headless, bare, serverless := policySpec(t), policySpec(t), policySpec(t)
	headless.RunMode = ports.RunModeHeadless
	bare.RunMode, bare.ConfigDir = ports.RunModeInteractive, ""
	serverless.RunMode, serverless.Binary = ports.RunModeInteractive, ""
	for name, spec := range map[string]ports.SessionSpec{"headless": headless, "no directory": bare, "no server": serverless} {
		if got := toolsListedPath(spec); got != "" {
			t.Errorf("%s: the marker is %q", name, got)
		}
		for how, env := range map[string][]string{"pane": paneEnv(spec, Use{}), "headless": sessionEnv(spec, Use{})} {
			if slices.ContainsFunc(env, func(entry string) bool { return strings.HasPrefix(entry, lanes.EnvToolsListed+"=") }) {
				t.Errorf("%s: the %s environment names a marker: %v", name, how, env)
			}
		}
		if _, named := generateMCP(spec).Servers[ServerName].Env[lanes.EnvToolsListed]; named {
			t.Errorf("%s: the server passes on a marker", name)
		}
	}
}

// toolsPane is a pane session that waits on a marker, with the notes the driver would write to the worker's log.
func toolsPane(t *testing.T, wait time.Duration) (*paneSession, ports.SessionSpec, *[]string) {
	t.Helper()
	spec := policySpec(t)
	spec.RunMode = ports.RunModeInteractive
	if err := os.MkdirAll(spec.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var (
		lock  sync.Mutex
		notes []string
	)
	driver := &Driver{toolsWait: wait, notice: func(note string) {
		lock.Lock()
		defer lock.Unlock()
		notes = append(notes, note)
	}}
	s := driver.newPaneSession(spec, paneName(spec), ports.SessionHandle{ID: "s"}, lanes.CostMeter{})
	return s, spec, &notes
}

// The brief waits for the file, and goes once it stands.
func TestTheBriefWaitsForTheServerToListItsTools(t *testing.T) {
	s, spec, notes := toolsPane(t, 20*time.Second)
	released := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		close(released)
		_ = os.WriteFile(toolsListedPath(spec), nil, 0o600)
	}()
	started := time.Now()
	s.awaitTools(t.Context(), spec)
	select {
	case <-released:
	default:
		t.Fatalf("the brief went after %v, before the server listed its tools", time.Since(started))
	}
	if len(*notes) != 0 {
		t.Errorf("a wait that ended with the tools listed left %q in the log", *notes)
	}
}

func TestTheBriefDoesNotWaitWhenTheToolsAreListed(t *testing.T) {
	s, spec, notes := toolsPane(t, 20*time.Second)
	if err := os.WriteFile(toolsListedPath(spec), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	s.awaitTools(t.Context(), spec)
	if took := time.Since(started); took > time.Second || len(*notes) != 0 {
		t.Errorf("a session whose tools were listed waited %v and logged %q", took, *notes)
	}
}

// A server that never lists its tools, because the CLI failed to connect it or the version does not list at its start,
// holds the brief for a bounded wait only. The brief then goes, and the log says why it went without them.
func TestTheBriefGoesAnywayWhenTheServerListsNoTools(t *testing.T) {
	s, spec, notes := toolsPane(t, 100*time.Millisecond)
	started := time.Now()
	s.awaitTools(t.Context(), spec)
	if took := time.Since(started); took < 100*time.Millisecond {
		t.Errorf("the brief went after %v, before the wait ran out", took)
	}
	if len(*notes) != 1 {
		t.Fatalf("the log holds %q", *notes)
	}
	for _, want := range []string{"lane-1", "generation 1", "tools", "100ms", "brief"} {
		if !strings.Contains((*notes)[0], want) {
			t.Errorf("the note %q leaves out %q", (*notes)[0], want)
		}
	}
}

// A session with no marker has nothing to wait for, and a stopped start stops the wait.
func TestTheWaitForToolsEndsWithTheStartOrWhenThereIsNoMarker(t *testing.T) {
	s, spec, notes := toolsPane(t, 20*time.Second)
	headless := spec
	headless.RunMode = ports.RunModeHeadless
	started := time.Now()
	s.awaitTools(t.Context(), headless)
	if took := time.Since(started); took > time.Second {
		t.Errorf("a session with no marker waited %v", took)
	}
	stopped, end := context.WithCancel(t.Context())
	end()
	started = time.Now()
	s.awaitTools(stopped, spec)
	if took := time.Since(started); took > time.Second || len(*notes) != 0 {
		t.Errorf("a stopped start waited %v and logged %q", took, *notes)
	}
}

// A marker an earlier start of the same generation left says nothing of this start's server, so a start clears it
// before its pane opens.
func TestAStartClearsTheMarkerAnEarlierStartLeft(t *testing.T) {
	spec := policySpec(t)
	spec.RunMode = ports.RunModeInteractive
	if err := os.MkdirAll(spec.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(toolsListedPath(spec), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := clearToolsListed(spec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(toolsListedPath(spec)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the marker stands: %v", err)
	}
	if err := clearToolsListed(spec); err != nil {
		t.Errorf("clearing a marker that is not there: %v", err)
	}
}
