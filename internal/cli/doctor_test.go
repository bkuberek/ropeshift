package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
)

// doctorRun runs `ropeshift worker doctor` with the doctor's report given, and no daemon.
func doctorRun(manifest ledger.Manifest) (int, string) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"worker", "doctor"}, &stdout, &stderr, cli.Options{
		Doctor: func(context.Context) (ledger.Manifest, error) { return manifest, nil },
	})
	return code, stdout.String()
}

// TestTheDoctorSaysEachDriverInTheWordsTheDomainWrites holds each line of `worker doctor` to the result the
// domain gives for the same driver, which the web UI shows too, so the two cannot differ.
func TestTheDoctorSaysEachDriverInTheWordsTheDomainWrites(t *testing.T) {
	usable := ledger.DriverManifest{
		Driver: "claude-code", Present: true, Version: "2.1.316", Qualified: true,
		SignIn: ledger.SignIn{Kind: ledger.SignInSubscription, Status: ledger.SignedIn},
	}
	unqualified := usable
	unqualified.Qualified = false
	unqualified.SignIn = ledger.SignIn{Status: ledger.SignInUnknown}
	signedOut := usable
	signedOut.SignIn = ledger.SignIn{Kind: ledger.SignInSubscription, Status: ledger.SignedOut}
	tests := []struct {
		name   string
		driver ledger.DriverManifest
		line   string
		code   int
	}{
		{"usable", usable, "claude-code\t2.1.316\tqualified\tsign-in subscription\tsigned_in\n", cli.ExitOK},
		{"usable, unqualified, and its sign-in unread", unqualified, "claude-code\t2.1.316\tunqualified\tsign-in -\tunknown\n", cli.ExitOK},
		{"absent", ledger.DriverManifest{Driver: "claude-code", Detail: "claude is not on PATH"}, "claude-code\tabsent\tclaude is not on PATH\n", cli.ExitError},
		{
			"a failed version check", ledger.DriverManifest{Driver: "claude-code", Detail: "claude --version exited with status 1"},
			"claude-code\tunusable\tclaude --version exited with status 1. Run claude --version to see why, and repair or reinstall Claude Code.\n", cli.ExitError,
		},
		{"signed out", signedOut, "claude-code\tsigned out\tsign-in subscription\t2.1.316\tqualified\n", cli.ExitError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout := doctorRun(ledger.Manifest{Drivers: []ledger.DriverManifest{tt.driver}})
			if code != tt.code || stdout != tt.line {
				t.Errorf("exit %d and %q, want %d and %q", code, stdout, tt.code, tt.line)
			}
			if result := tt.driver.Result(); !result.Usable && result.Word == "" {
				t.Errorf("a driver that is not usable has no word: %+v", result)
			}
		})
	}
}

// TestTheDoctorGoldenPrintsAsTheWebTestsRead writes what `worker doctor` prints for the manifest of each worker in
// the golden of a shop floor whose workers reported different results, so a web test holds the Workers view to the
// command's own words. Each worker's lines follow a `worker` line naming it.
func TestTheDoctorGoldenPrintsAsTheWebTestsRead(t *testing.T) {
	var floor struct {
		Workers []struct {
			Worker struct {
				ID       string          `json:"id"`
				Manifest ledger.Manifest `json:"manifest"`
			} `json:"worker"`
		} `json:"workers"`
	}
	readGolden(t, "shop-floor-drivers.json", &floor)
	if len(floor.Workers) < 6 {
		t.Fatalf("the golden holds %d workers", len(floor.Workers))
	}
	var text strings.Builder
	for _, line := range floor.Workers {
		_, stdout := doctorRun(line.Worker.Manifest)
		fmt.Fprintf(&text, "worker\t%s\n%s", line.Worker.ID, stdout)
	}
	holdsText(t, "doctor.cli.txt", text.String())
}
