package claudecode_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/claudecode"
	"ropeshift.com/ropeshift/internal/adapters/osexec"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/doubles"
)

// A session an earlier worker started uses the surfaces its process started with, as the launch recorded them, and not what
// the driver holds of its version now: a probe that ended since may differ, and the process cannot take what it did not
// start with. The version here was in the shipped matrix at the launch, which no probe had yet replaced.
func TestAnAdoptedSessionUsesTheSurfacesItLaunchedWith(t *testing.T) {
	first, path, spec := session(t, "lane-survives", nil)
	doubles.SetFakeClaudeVersion(t, path, "2.1.317")
	handle, err := first.Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if handle.Source != lanes.QualifiedByMatrix || handle.Degraded {
		t.Fatalf("the launch on a version the matrix holds reads %+v", handle)
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
	// The worker that took over has since probed the version, and the probe found its socket failed.
	next := claudecode.New(osexec.Runner{}, claudecode.Options{Env: []string{"PATH=" + path}, StopGrace: 2 * time.Second})
	next.Qualify(probed("2.1.317", claudecode.SurfaceSocketLine))
	located, err := next.Find(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := located.Handle; got.Version != "2.1.317" || got.Source != lanes.QualifiedByMatrix || got.Degraded {
		t.Errorf("found %+v, want the version, the matrix, and no degraded mode the launch recorded", got)
	}
	if err := next.Adopt(t.Context(), located.Handle, ports.AdoptFrom{Spec: spec}); err != nil {
		t.Fatal(err)
	}
	// With no hook naming its socket, a session whose launch passed the socket's check has no channel now, which is not the
	// version's doing: the use it adopted with is the launch's.
	if _, err := next.Send(t.Context(), located.Handle, ports.Input{Text: "from a peer", Peer: true}); !errors.Is(err, ports.ErrChannelUnavailable) {
		t.Errorf("an adopted session whose launch passed the socket's check answered %v, want its channel unavailable", err)
	}
	if err := os.WriteFile(filepath.Join(doubles.BinDir(path), "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := next.Stop(t.Context(), located.Handle, ports.StopNow); err != nil {
		t.Fatal(err)
	}
}

// A session that launched degraded stays degraded for its life: a later probe that passed every check for its version
// qualifies the processes that start after it, and leaves this one as the launch recorded it.
func TestAnAdoptedSessionKeepsItsLaunchedCondition(t *testing.T) {
	first, path, spec := session(t, "lane-survives", nil)
	doubles.SetFakeClaudeVersion(t, path, "2.1.399")
	first.Qualify(probed("2.1.399", claudecode.SurfaceSocketLine))
	handle, err := first.Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !handle.Degraded {
		t.Fatalf("the launch whose socket failed reads %+v", handle)
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
	next.Qualify(probed("2.1.399"))
	located, err := next.Find(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := located.Handle; got.Version != "2.1.399" || !got.Degraded || got.Source != lanes.QualifiedByProbe {
		t.Errorf("found %+v, want the degraded launch recorded, not the pass that came after", got)
	}
	if err := next.Adopt(t.Context(), located.Handle, ports.AdoptFrom{Spec: spec}); err != nil {
		t.Fatal(err)
	}
	if _, err := next.Send(t.Context(), located.Handle, ports.Input{Text: "from a peer", Peer: true}); !errors.Is(err, ports.ErrNoChannel) || errors.Is(err, ports.ErrChannelUnavailable) {
		t.Errorf("an adopted session whose launch failed the socket's check answered %v, want no channel for the version", err)
	}
	if err := os.WriteFile(filepath.Join(doubles.BinDir(path), "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := next.Stop(t.Context(), located.Handle, ports.StopNow); err != nil {
		t.Fatal(err)
	}
}
