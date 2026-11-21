package worker_test

import (
	"errors"
	"io/fs"
	"os"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ports"
)

// FR-ORCH-9: what a consultation's session leaves on the machine, its private directory, its settings, and its short
// temporary directory for sockets, goes when the consultation ends, because nothing reads them after.
func TestAConsultationLeavesNothingOfItsSessionOnTheMachine(t *testing.T) {
	t.Parallel()
	r := aToolRig(t)
	existed := make(chan map[string]bool, 1)
	r.nextSession([]fake.DriverStep{{Bound: true}, {Do: func(spec ports.SessionSpec) {
		found := map[string]bool{}
		for name, path := range map[string]string{"private directory": spec.Dir, "settings": spec.ConfigDir, "socket directory": spec.TempDir} {
			_, err := os.Stat(path)
			found[name] = path != "" && err == nil
		}
		existed <- found
	}}, r.reportsConsultation(t, consultReport), completed(0.1)})
	r.consult(t, map[string]any{"question": "q1", "ask": consultAsk})
	r.awaitConsulted(t, "q1")
	for name, found := range <-existed {
		if !found {
			t.Errorf("the %s did not exist while the session ran, so this test shows nothing", name)
		}
	}
	spec := r.consultSpecs()[0]
	for name, path := range map[string]string{"private directory": spec.Dir, "settings": spec.ConfigDir, "socket directory": spec.TempDir} {
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the %s %s remains after the consultation ended: %v", name, path, err)
		}
	}
}
