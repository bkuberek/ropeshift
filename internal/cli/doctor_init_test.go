package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
)

// The doctor says when the mark of the daemon `ropeshift init` starts is set in its environment. A daemon started
// from that environment is such a daemon: it qualifies no agent CLI and takes no work, so a person who finds their
// daemon idle learns why. Without the mark the doctor says nothing of it, in text and in JSON.
func TestTheDoctorSaysWhenTheInitMarkIsSet(t *testing.T) {
	manifest := ledger.Manifest{Drivers: []ledger.DriverManifest{{
		Driver: "claude-code", Present: true, Version: "2.1.316", Qualified: true,
		SignIn: ledger.SignIn{Kind: ledger.SignInSubscription, Status: ledger.SignedIn},
	}}}
	for _, marked := range []bool{false, true} {
		for _, asJSON := range []bool{false, true} {
			args := []string{"worker", "doctor"}
			if asJSON {
				args = append(args, "--json")
			}
			var stdout, stderr bytes.Buffer
			code := cli.Run(args, &stdout, &stderr, cli.Options{
				Doctor:     func(context.Context) (ledger.Manifest, error) { return manifest, nil },
				InitMarked: marked,
			})
			if code != cli.ExitOK {
				t.Errorf("marked %v, json %v: exit %d: %s", marked, asJSON, code, stderr.String())
			}
			if asJSON {
				var report struct {
					InitDaemon bool `json:"init_daemon"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
					t.Fatal(err)
				}
				if report.InitDaemon != marked {
					t.Errorf("marked %v: the report says init_daemon %v", marked, report.InitDaemon)
				}
				continue
			}
			said := strings.Contains(stdout.String(), "init daemon\tmarked\t")
			if said != marked || marked && !strings.Contains(stdout.String(), "takes no work") {
				t.Errorf("marked %v: the doctor printed %q", marked, stdout.String())
			}
		}
	}
}
