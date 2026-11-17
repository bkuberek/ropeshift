package worker_test

import (
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
)

// effectsOfLane lists the journaled effects of a lane, in the order they began.
func (r *installationRig) effectsOfLane(lane string) []lanes.Effect {
	r.t.Helper()
	all, err := r.journal.Effects(r.t.Context())
	if err != nil {
		r.t.Fatal(err)
	}
	var out []lanes.Effect
	for _, effect := range all {
		if effect.Lane == lane {
			out = append(out, effect)
		}
	}
	return out
}

// TestAMessageToALaneOfAnotherProjectIsJournaledUnderThatProject delivers a message to a lane of the
// installation's second project. The write of the delivery names the lane's project, so what the
// ledger is told of it reaches that project and not the first.
func TestAMessageToALaneOfAnotherProjectIsJournaledUnderThatProject(t *testing.T) {
	t.Parallel()
	r := newInstallationRig(t)
	r.dispatch("pb", "lane-b")
	reached, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		r.driver.StopEverySession()
		r.subject.Wait()
	})
	t.Cleanup(func() { close(release) })
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Do: func(ports.SessionSpec) {
		close(reached)
		<-release
	}}, completed(0.01)}))
	r.pass()
	select {
	case <-reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the session never reached its work")
	}
	sending := control.NewMessages(r.stores["pb"], nil)
	sent, err := sending.Send(t.Context(), "pb", messages.Draft{
		ID: "m-1", To: messages.Target{Lane: "lane-b"}, Kind: messages.KindInstruction, Body: "Use two retries.",
	}, member)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		r.pass()
		if err := r.subject.Upload(t.Context()); err != nil {
			t.Fatal(err)
		}
		m, err := sending.ReadMessage(t.Context(), "pb", sent.ID)
		if err != nil {
			t.Fatal(err)
		}
		if m.State == messages.HandedOff {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("message %s reads %s, want %s", sent.ID, m.State, messages.HandedOff)
		}
		time.Sleep(time.Millisecond)
	}
	var delivery *lanes.Effect
	for _, effect := range r.effectsOfLane("lane-b") {
		if effect.Kind == lanes.EffectInput && strings.Contains(effect.Key, sent.ID) {
			delivery = &effect
		}
	}
	if delivery == nil {
		t.Fatalf("no delivery of %s on record: %+v", sent.ID, r.effectsOfLane("lane-b"))
	}
	if delivery.Project != "pb" {
		t.Errorf("the delivery is journaled under project %q, want %q", delivery.Project, ledger.WorkspaceID("pb"))
	}
}
