package contract

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/claudecode"
	"ropeshift.com/ropeshift/internal/adapters/osexec"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/doubles"
)

// peerRig is the real driver and the real shim, with fakeclaude behind them, and one headless
// session. A socket surface runs only on a version the matrix pins, so a fixture names its version.
type peerRig struct {
	t      *testing.T
	driver *claudecode.Driver
	path   string
	spec   ports.SessionSpec
}

func newPeerRig(t *testing.T, fixture string) *peerRig {
	t.Helper()
	driver := claudecode.New(osexec.Runner{}, claudecode.Options{})
	path := doubles.InstallFakeClaude(t, fixture)
	driver.UseEnv([]string{"PATH=" + path})
	spec := baseSpec(t)
	spec.Env = []string{"PATH=" + path, "HOME=" + t.TempDir()}
	spec.Binary, spec.TempDir = doubles.Ropeshift(t), apitest.ShortDir(t)
	doubles.ReapShims(t, spec.ConfigDir)
	return &peerRig{t: t, driver: driver, path: path, spec: spec}
}

func (r *peerRig) start() *driverSession {
	r.t.Helper()
	handle, err := r.driver.Start(r.t.Context(), r.spec)
	if err != nil {
		r.t.Fatal(err)
	}
	events, err := r.driver.Events(r.t.Context(), handle)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = r.driver.Stop(context.Background(), handle, ports.StopNow) })
	ctx, cancel := context.WithTimeout(r.t.Context(), 20*time.Second)
	r.t.Cleanup(cancel)
	return &driverSession{t: r.t, ctx: ctx, rig: DriverRig{Driver: r.driver}, handle: handle, events: events}
}

func (r *peerRig) messagesSeen() []string {
	r.t.Helper()
	raw, err := os.ReadFile(filepath.Join(doubles.BinDir(r.path), "messages.log"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for line := range strings.Lines(string(raw)) {
		_, id, _ := strings.Cut(strings.TrimSpace(line), " ")
		out = append(out, id)
	}
	return out
}

func peerSocket(t *testing.T, spec ports.SessionSpec) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		found, err := filepath.Glob(filepath.Join(spec.TempDir, "cc-socks", "[0-9]*.sock"))
		if err != nil {
			t.Fatal(err)
		}
		if len(found) == 1 {
			return found[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no session socket")
	return ""
}

// TestAPeerMessageGoesByTheSocketAndReturnsAsAReplay is the headless path of the design's table.
func TestAPeerMessageGoesByTheSocketAndReturnsAsAReplay(t *testing.T) {
	r := newPeerRig(t, "peer-messages")
	session := r.start()
	session.until(ports.EventTurnEnded)
	input := ports.Input{ID: messages.InputID("m-1"), Text: "[ropeshift message m-1 kind=notice age=0m01s tier=fresh from=member]\nA notice.", Peer: true}
	receipt, err := r.driver.Send(session.ctx, session.handle, input)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Path != ports.SendSocket || receipt.ID != input.ID {
		t.Errorf("receipt %+v", receipt)
	}
	for {
		if event := session.until(ports.EventInputAdmitted); event.InputID == input.ID {
			break
		}
	}
	if seen := r.messagesSeen(); !slices.Equal(seen, []string{input.ID}) {
		t.Errorf("the socket saw %v", seen)
	}
}

// A worker's word for a running turn takes the one path a turn reads between tool calls, because
// stdin waits for the turn to end. The turn here is over, and the path is what this states.
func TestAnInstructionForARunningTurnGoesByTheSocket(t *testing.T) {
	r := newPeerRig(t, "peer-messages")
	session := r.start()
	session.until(ports.EventTurnEnded)
	input := ports.Input{ID: messages.InputID("context-1"), Text: "[ropeshift message context-1 kind=instruction age=0m00s tier=fresh from=worker]\nHand off.", MidTurn: true}
	receipt, err := r.driver.Send(session.ctx, session.handle, input)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Path != ports.SendSocket {
		t.Errorf("receipt %+v", receipt)
	}
	for {
		if event := session.until(ports.EventInputAdmitted); event.InputID == input.ID {
			break
		}
	}
}

// policyHeld reads what a hook would read.
func policyHeld(t *testing.T, spec ports.SessionSpec) lanes.DenyPolicy {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(spec.ConfigDir, "policy-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var policy lanes.DenyPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	return policy
}

func checkHold(t *testing.T, driver ports.AgentDriver, handle ports.SessionHandle, spec ports.SessionSpec) {
	t.Helper()
	before := policyHeld(t, spec)
	if before.Hold != "" || before.OwnTools != "mcp__ropeshift__" {
		t.Fatalf("a session starts held, or with no way out: %+v", before)
	}
	if err := driver.HoldTools(t.Context(), handle, lanes.ReasonHandOff); err != nil {
		t.Fatal(err)
	}
	after := policyHeld(t, spec)
	if after.Hold != lanes.ReasonHandOff {
		t.Errorf("hold %q", after.Hold)
	}
	if !slices.Equal(after.Roots, before.Roots) || len(after.Deny) != len(before.Deny) || after.QuestionTool != before.QuestionTool {
		t.Errorf("a hold changed more than itself: %+v", after)
	}
	if err := driver.HoldTools(t.Context(), handle, ""); err != nil {
		t.Fatal(err)
	}
	if lifted := policyHeld(t, spec); lifted.Hold != "" {
		t.Errorf("hold %q", lifted.Hold)
	}
	left, err := filepath.Glob(filepath.Join(spec.ConfigDir, "policy-1.json.*"))
	if err != nil || len(left) != 0 {
		t.Errorf("left beside the policy: %v, %v", left, err)
	}
}

// A hook reads the file with no lock, and a file it cannot read denies every tool, so a hold going
// on and off must never show a reader an empty or half-written policy.
func TestAHookNeverReadsAHalfWrittenPolicy(t *testing.T) {
	r := newPeerRig(t, "peer-messages")
	session := r.start()
	session.until(ports.EventSessionBound)
	path := filepath.Join(r.spec.ConfigDir, "policy-1.json")
	done := make(chan struct{})
	bad := make(chan string, 1)
	go func() {
		defer close(bad)
		for {
			select {
			case <-done:
				return
			default:
			}
			raw, err := os.ReadFile(path)
			var policy lanes.DenyPolicy
			if err != nil || json.Unmarshal(raw, &policy) != nil {
				bad <- string(raw)
				return
			}
		}
	}()
	for i := range 400 {
		reason := lanes.ReasonHandOff
		if i%2 == 1 {
			reason = ""
		}
		if err := r.driver.HoldTools(t.Context(), session.handle, reason); err != nil {
			t.Fatal(err)
		}
	}
	close(done)
	if raw, read := <-bad; read {
		t.Errorf("a hook read %q", raw)
	}
}

func TestAHoldReachesTheFileAHookReads(t *testing.T) {
	r := newPeerRig(t, "peer-messages")
	session := r.start()
	session.until(ports.EventSessionBound)
	checkHold(t, r.driver, session.handle, r.spec)
}

func TestAHoldReachesAPanesHook(t *testing.T) {
	r := newPaneRig(t, "pane-two-turns")
	session := r.start()
	session.until(ports.EventTurnEnded)
	checkHold(t, r.driver, session.handle, r.spec)
}

// TestAWorkersOwnInputStaysOnStdin keeps a peer's path to peers: nothing the worker says for
// itself reaches the socket.
func TestAWorkersOwnInputStaysOnStdin(t *testing.T) {
	r := newPeerRig(t, "peer-messages")
	session := r.start()
	session.until(ports.EventTurnEnded)
	session.send("the worker's own word")
	session.until(ports.EventTurnEnded)
	if seen := r.messagesSeen(); len(seen) != 0 {
		t.Errorf("the socket saw %v", seen)
	}
}

// TestAPeerMessageBeforeAFirstPromptUsesTheHooksSocket is a session that bound its socket and has
// no init yet: only SessionStart can have named it.
func TestAPeerMessageBeforeAFirstPromptUsesTheHooksSocket(t *testing.T) {
	r := newPeerRig(t, "peer-messages")
	r.spec.Brief = ""
	session := r.start()
	input := ports.Input{ID: messages.InputID("m-1"), Text: "A message.", Peer: true}
	if _, err := r.driver.Send(session.ctx, session.handle, input); !errors.Is(err, ports.ErrNoChannel) {
		t.Fatalf("a send with no socket named: %v", err)
	}
	r.driver.TakeHook(session.ctx, lanes.HookEvent{
		Lane: r.spec.Lane, Generation: r.spec.Generation, Event: "SessionStart", Socket: peerSocket(t, r.spec),
	})
	if _, err := r.driver.Send(session.ctx, session.handle, input); err != nil {
		t.Fatal(err)
	}
	for {
		if event := session.until(ports.EventInputAdmitted); event.InputID == input.ID {
			break
		}
	}
}

// TestAPeerMessageGoesOnlyToThisSessionsProcess refuses a socket named by anything but its owner's
// process ID, which is the guard between a message and another session on this machine.
func TestAPeerMessageGoesOnlyToThisSessionsProcess(t *testing.T) {
	r := newPeerRig(t, "peer-messages")
	session := r.start()
	session.until(ports.EventTurnEnded)
	real := peerSocket(t, r.spec)
	stranger := filepath.Join(filepath.Dir(real), "1.sock")
	r.driver.TakeHook(session.ctx, lanes.HookEvent{Lane: r.spec.Lane, Generation: r.spec.Generation, Event: "SessionStart", Socket: stranger})
	_, err := r.driver.Send(session.ctx, session.handle, ports.Input{ID: messages.InputID("m-1"), Text: "A message.", Peer: true})
	if err == nil || !strings.Contains(err.Error(), "does not name this session's process") {
		t.Errorf("a send to another process's socket: %v", err)
	}
	if seen := r.messagesSeen(); len(seen) != 0 {
		t.Errorf("the socket saw %v", seen)
	}
}

// TestAnUnqualifiedVersionHasNoPeerPath is the plan's check: a socket surface runs only on a
// version that passed it. stdin does not stand in, because that gives a message too much standing.
func TestAnUnqualifiedVersionHasNoPeerPath(t *testing.T) {
	r := newPeerRig(t, "unqualified-version")
	capabilities, err := r.driver.Capabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.Injection != ports.InjectionNone {
		t.Errorf("injection %s", capabilities.Injection)
	}
	session := r.start()
	session.until(ports.EventSessionBound)
	// A hook can name a socket on any version, so only the version stops this write.
	r.driver.TakeHook(session.ctx, lanes.HookEvent{
		Lane: r.spec.Lane, Generation: r.spec.Generation, Event: "SessionStart", Socket: filepath.Join(r.spec.TempDir, "cc-socks", "1.sock"),
	})
	_, err = r.driver.Send(session.ctx, session.handle, ports.Input{ID: messages.InputID("m-1"), Text: "A message.", Peer: true})
	if !errors.Is(err, ports.ErrNoChannel) {
		t.Errorf("a send on an unqualified version: %v", err)
	}
	_, err = r.driver.Send(session.ctx, session.handle, ports.Input{ID: messages.InputID("m-2"), Text: "Hand off.", MidTurn: true})
	if !errors.Is(err, ports.ErrNoChannel) {
		t.Errorf("a mid-turn send on an unqualified version: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.spec.TempDir, "cc-socks", "r1.sock")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an address for receipts on an unqualified version: %v", err)
	}
}

// TestAnUnqualifiedPaneTakesItsBriefAndNoPeer keeps a pane's one input path for the worker: a brief
// still reaches it on any version, and a peer's message does not.
func TestAnUnqualifiedPaneTakesItsBriefAndNoPeer(t *testing.T) {
	r := newPaneRig(t, "peer-unqualified-pane")
	r.spec.Env = append(r.spec.Env, "FAKECLAUDE_SOCKETS="+r.spec.TempDir)
	session := r.start()
	session.until(ports.EventInputAdmitted)
	_, err := r.driver.Send(session.ctx, session.handle, ports.Input{ID: messages.InputID("m-1"), Text: "A message.", Peer: true})
	if !errors.Is(err, ports.ErrNoChannel) {
		t.Errorf("a send on an unqualified version: %v", err)
	}
	_, err = r.driver.Send(session.ctx, session.handle, ports.Input{ID: messages.InputID("m-2"), Text: "Hand off.", MidTurn: true})
	if !errors.Is(err, ports.ErrNoChannel) {
		t.Errorf("a mid-turn send on an unqualified version: %v", err)
	}
}

// TestAQualifiedVersionDeclaresItsChannel is the other half of what a core reads.
func TestAQualifiedVersionDeclaresItsChannel(t *testing.T) {
	r := newPeerRig(t, "peer-messages")
	capabilities, err := r.driver.Capabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.Injection != ports.InjectionBetweenTools {
		t.Errorf("injection %s", capabilities.Injection)
	}
}

// TestARefusingSessionAnswersTheReplyAddress is the one receipt that moves a message.
func TestARefusingSessionAnswersTheReplyAddress(t *testing.T) {
	r := newPeerRig(t, "peer-refused")
	session := r.start()
	session.until(ports.EventTurnEnded)
	input := ports.Input{ID: messages.InputID("m-1"), Text: "A message.", Peer: true}
	if _, err := r.driver.Send(session.ctx, session.handle, input); err != nil {
		t.Fatal(err)
	}
	if event := session.until(ports.EventInputRefused); event.InputID != input.ID {
		t.Errorf("refused %s", event.InputID)
	}
}

// TestAHeldMessageReportsNoRefusal leaves a held message handed off: the sender reads it expire.
func TestAHeldMessageReportsNoRefusal(t *testing.T) {
	r := newPeerRig(t, "peer-held")
	session := r.start()
	session.until(ports.EventTurnEnded)
	if _, err := r.driver.Send(session.ctx, session.handle, ports.Input{ID: messages.InputID("m-1"), Text: "A message.", Peer: true}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case event := <-session.events:
			if event.Kind == ports.EventInputRefused || event.Kind == ports.EventInputAdmitted {
				t.Fatalf("a held message read %+v", event)
			}
		case <-deadline:
			return
		}
	}
}

// TestAPaneRefusalReachesTheStream is the same receipt in the other run mode.
func TestAPaneRefusalReachesTheStream(t *testing.T) {
	r := newPaneRig(t, "peer-refused")
	session := r.start()
	input := ports.Input{ID: messages.InputID("m-1"), Text: "A message.", Peer: true}
	deadline := time.Now().Add(20 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		if _, err = r.driver.Send(session.ctx, session.handle, input); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if event := session.until(ports.EventInputRefused); event.InputID != input.ID {
		t.Errorf("refused %s", event.InputID)
	}
}
