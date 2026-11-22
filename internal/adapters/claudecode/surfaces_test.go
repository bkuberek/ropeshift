package claudecode_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/claudecode"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/osexec"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/doubles"
)

// matrixVersion is a version the shipped matrix holds, on which no surface the driver relies on failed.
const matrixVersion = "2.1.316"

// surfacesCases are the two answers a driver gives for a version: the shipped matrix's before a probe's result, and
// the probe's after it.
var surfacesCases = []struct {
	name  string
	probe bool
	want  string
}{
	{"before any probe", false, lanes.QualifiedByMatrix},
	{"after a probe", true, lanes.QualifiedByProbe},
}

// A headless session's handle says where the surfaces its process started on came from, and a new driver that finds
// the session reads the same from what its launch wrote down.
func TestAHeadlessSessionAndItsFindSayWhereTheirSurfacesCameFrom(t *testing.T) {
	for _, tt := range surfacesCases {
		t.Run(tt.name, func(t *testing.T) {
			first, path, spec := session(t, "lane-survives", nil)
			doubles.SetFakeClaudeVersion(t, path, matrixVersion)
			if tt.probe {
				first.Qualify(probed(matrixVersion))
			}
			handle, err := first.Start(t.Context(), spec)
			if err != nil {
				t.Fatal(err)
			}
			if handle.Version != matrixVersion || handle.Degraded || handle.Source != tt.want {
				t.Errorf("the started session reads %+v, want surfaces from %q", handle, tt.want)
			}
			events, err := first.Events(t.Context(), handle)
			if err != nil {
				t.Fatal(err)
			}
			for event := range events {
				if event.Kind == ports.EventSessionBound {
					break
				}
			}
			next := claudecode.New(osexec.Runner{}, claudecode.Options{Env: []string{"PATH=" + path}, StopGrace: 2 * time.Second})
			if tt.probe {
				next.Qualify(probed(matrixVersion))
			}
			located, err := next.Find(t.Context(), spec)
			if err != nil {
				t.Fatal(err)
			}
			if located.Handle.Version != matrixVersion || located.Handle.Degraded || located.Handle.Source != tt.want {
				t.Errorf("the found session reads %+v, want surfaces from %q", located.Handle, tt.want)
			}
			if err := os.WriteFile(filepath.Join(doubles.BinDir(path), "release"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := first.Stop(t.Context(), handle, ports.StopNow); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A pane's handle says it too.
func TestAPaneSessionSaysWhereItsSurfacesCameFrom(t *testing.T) {
	ready, err := os.ReadFile(filepath.Join("testdata", "panes", "ready.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range surfacesCases {
		t.Run(tt.name, func(t *testing.T) {
			terminal := fake.NewTerminal()
			terminal.Play(fake.PaneScript{Screen: string(ready)})
			path := doubles.InstallFakeClaudeVersion(t, "lane-completed", matrixVersion)
			_, _, spec := session(t, "lane-completed", func(spec *ports.SessionSpec) {
				spec.RunMode, spec.Brief = ports.RunModeInteractive, ""
				spec.Env = []string{"PATH=" + path, "ROPESHIFT_LANE_ID=lane-1"}
			})
			driver := claudecode.New(osexec.Runner{}, claudecode.Options{Env: []string{"PATH=" + path}, Terminal: terminal, StopGrace: 20 * time.Millisecond})
			if tt.probe {
				driver.Qualify(probed(matrixVersion))
			}
			type started struct {
				handle ports.SessionHandle
				err    error
			}
			done := make(chan started, 1)
			go func() {
				handle, err := driver.Start(t.Context(), spec)
				done <- started{handle, err}
			}()
			// A pane has no other way to say where its socket is than its SessionStart hook.
			deadline := time.Now().Add(20 * time.Second)
			for len(terminal.Opened()) == 0 {
				if time.Now().After(deadline) {
					t.Fatal("the pane never opened")
				}
				time.Sleep(time.Millisecond)
			}
			driver.TakeHook(t.Context(), lanes.HookEvent{Lane: spec.Lane, Generation: spec.Generation, Event: "SessionStart", Socket: filepath.Join(spec.TempDir, "pane.sock")})
			var got started
			select {
			case got = <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("the pane never started")
			}
			if got.err != nil {
				t.Fatal(got.err)
			}
			t.Cleanup(func() { _ = driver.Stop(t.Context(), got.handle, ports.StopNow) })
			if got.handle.Version != matrixVersion || got.handle.Degraded || got.handle.Source != tt.want {
				t.Errorf("the started pane reads %+v, want surfaces from %q", got.handle, tt.want)
			}
		})
	}
}
