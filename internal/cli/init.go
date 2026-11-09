package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
)

// SampleContractName is the file ropeshift init writes in the installation's state directory.
const SampleContractName = "sample-contract.md"

// sampleContract is a contract intake reads once a person commits it to a repository the daemon
// reads. Its work is small and safe to run, so a first lane on it risks nothing.
const sampleContract = `+++
kind = "contract"
id = "sample-test-command"
title = "Say how to run the tests"

[[criteria]]
id = "c1"
text = "README.md has a section that names the command which runs the tests"

[[work_orders]]
id = "sample-test-command-order"
title = "Write the section of README.md that says how to run the tests"
priority = 3
size = "small"
needs = { driver = "claude-code" }
+++

This sample contract comes from ropeshift init. A contract states what a piece of work must deliver,
and each criterion is something a reviewer can check against the result.

Change the identifiers, the title, the criteria, and this statement to describe your own work. Commit
the file to a repository, and start the daemon with that repository as a source. Intake reads it at
the commit it reads, and the work order waits for a member to review it.
`

// registrationWait bounds how long a first start may take to register its worker.
const registrationWait = 60 * time.Second

// vendor is what a person needs to know to install and sign in to one agent CLI, and the terms
// a subscription to it is held to.
type vendor struct{ product, company, command, terms string }

var vendors = map[string]vendor{
	"claude-code": {product: "Claude Code", company: "Anthropic", command: "claude", terms: "Anthropic's Consumer Terms"},
}

func vendorOf(driver string) vendor {
	if v, ok := vendors[driver]; ok {
		return v
	}
	return vendor{product: driver, company: "its vendor", command: driver, terms: "Its vendor's terms"}
}

// credentialsStatement is NFR-SEC-1, said where a person signs in.
const credentialsStatement = "Ropeshift never reads, stores, relays, or proxies an agent CLI's credentials. Each CLI keeps its own sign-in."

// beforeTheFirstLane is what onboarding states of solo mode before anything runs (FR-AUTH-5).
const beforeTheFirstLane = `Before the first lane:
  Lanes run as the same operating system user as you, so nothing on this machine separates an agent
  from your files, your browser, or a session you hold. A passphrase raises the cost of an agent
  approving its own request, and does not make it impossible.
  A repository's own settings and hooks run in its lanes. Run lanes on work from outside your team
  only behind another operating system user, a container, a virtual machine, or another machine.
  docs/onboarding.md and docs/authorization.md say more.
`

// initResult is what ropeshift init did.
type initResult struct {
	Daemon     string                  `json:"daemon"`
	Passphrase string                  `json:"passphrase"`
	Worker     ledger.Worker           `json:"worker"`
	Drivers    []ledger.DriverManifest `json:"drivers"`
	Usable     bool                    `json:"usable"`
	Sample     string                  `json:"sample"`
	Written    bool                    `json:"sample_written"`
}

// initMachine sets up this machine for solo mode: the passphrase, the worker's registration, the
// doctor, and a sample contract. A step already done stays as it is, so a second run only says
// what is left. With no daemon running, it runs one for its own length, because a first start is
// what registers the worker.
func (c *invocation) initMachine(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	if c.doctor == nil || c.startDaemon == nil || c.stateDir == "" {
		return c.usageError("this build cannot set up a machine")
	}
	client := c.client()
	defer client.Close()
	var result initResult
	stop, err := c.reachDaemon(client, &result)
	if err != nil {
		return c.failure(err)
	}
	code, done := c.setUp(client, &result)
	if stop != nil {
		if err := stop(); err != nil && done {
			return c.failure(fmt.Errorf("stopping the daemon this run started: %w", err))
		}
	}
	if !done {
		return code
	}
	if c.asJSON {
		if err := json.NewEncoder(c.stdout).Encode(result); err != nil {
			return c.failure(err)
		}
	}
	if !result.Usable {
		return ExitError
	}
	return ExitOK
}

// reachDaemon finds the running daemon, or starts one for this run and returns how to stop it.
func (c *invocation) reachDaemon(client *api.Client, result *initResult) (func() error, error) {
	_, err := client.Health(c.ctx)
	if err == nil {
		result.Daemon = "running"
		c.say("Daemon: running.\n")
		return nil, nil
	}
	if !errors.Is(err, api.ErrUnreachable) {
		return nil, err
	}
	stop, err := c.startDaemon(c.ctx)
	if err != nil {
		return nil, err
	}
	result.Daemon = "started"
	c.say("Daemon: started for this run, and stopped at its end.\n")
	return stop, nil
}

// setUp runs each step in order, and says whether every one was done. A step that fails ends the
// run with its exit code, and a missing agent CLI is said and does not stop the steps after it.
func (c *invocation) setUp(client *api.Client, result *initResult) (int, bool) {
	for _, step := range []func(*api.Client, *initResult) error{c.initPassphrase, c.initWorker, c.initDoctor, c.initSample} {
		if err := step(client, result); err != nil {
			var usage usageProblem
			if errors.As(err, &usage) {
				return c.usageError("%s", usage), false
			}
			return c.failure(err), false
		}
	}
	c.say(beforeTheFirstLane)
	if result.Daemon == "started" {
		c.say("Next: start the daemon with ropeshiftd solo --source <repository>.\n")
	}
	return ExitOK, true
}

// usageProblem is a step that failed on what a person typed.
type usageProblem string

func (u usageProblem) Error() string { return string(u) }

func (c *invocation) initPassphrase(client *api.Client, result *initResult) error {
	state, err := client.Passphrase(c.ctx)
	if err != nil {
		return err
	}
	if state.Set {
		result.Passphrase = "kept"
		c.say("Passphrase: already set, and kept. ropeshift passphrase set changes it.\n")
		return nil
	}
	passphrase, err := c.askPassphrase("New passphrase: ")
	if err != nil {
		return err
	}
	again, err := c.askPassphrase("Again: ")
	if err != nil {
		return err
	}
	if passphrase != again {
		return usageProblem("the two passphrases differ")
	}
	if err := client.SetPassphrase(c.ctx, api.SetPassphrase{Passphrase: passphrase}); err != nil {
		return err
	}
	result.Passphrase = "set"
	c.say("Passphrase: set. A change asks for it on the terminal, and eval \"$(ropeshift login)\" holds a session in one shell.\n")
	return nil
}

// initWorker waits for the worker's registration, which a first start makes on its own.
func (c *invocation) initWorker(client *api.Client, result *initResult) error {
	ctx, cancel := context.WithTimeout(c.ctx, registrationWait)
	defer cancel()
	for {
		workers, err := client.Workers(ctx)
		if err != nil {
			return err
		}
		if len(workers) != 0 {
			result.Worker = workers[0]
			c.say("Worker: registered as %s.\n", workers[0].ID)
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("no worker registered within %v", registrationWait)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (c *invocation) initDoctor(_ *api.Client, result *initResult) error {
	manifest, err := c.doctor(c.ctx)
	if err != nil {
		return err
	}
	result.Drivers = manifest.Drivers
	result.Usable = anyUsable(manifest)
	c.say("Agent CLIs:\n")
	for _, driver := range manifest.Drivers {
		c.say("  %s\n", initDriverLine(driver))
	}
	c.say("%s\n", credentialsStatement)
	return nil
}

// initDriverLine says where one agent CLI stands, and what a person does next. Only a kind and a
// status reach it, never who is signed in.
func initDriverLine(driver ledger.DriverManifest) string {
	v := vendorOf(driver.Driver)
	if failedCheck(driver, v) {
		return fmt.Sprintf("%s: installed, and its version check failed: %s. Run %s --version to see why, repair or reinstall %s, and run ropeshift init again.",
			driver.Driver, driver.Detail, v.command, v.product)
	}
	if !driver.Present {
		return fmt.Sprintf("%s: not installed. Install %s, sign in through %s's own flow, and run ropeshift init again.", driver.Driver, v.product, v.company)
	}
	name := driver.Driver + " " + driver.Version
	var line string
	switch {
	case driver.SignIn.Status == lanes.SignedOut:
		return fmt.Sprintf("%s: installed, not signed in. Run %s and sign in through %s's own flow, or give the CLI an API key it reads itself, then run ropeshift init again.",
			name, v.command, v.company)
	case driver.SignIn.Kind == lanes.SignInAPIKey:
		line = fmt.Sprintf("%s: signed in with an API key, which the CLI reads itself. This worker starts at unattended.", name)
	case driver.SignIn.Kind == lanes.SignInSubscription:
		line = fmt.Sprintf("%s: signed in with a subscription. This worker starts at attended, so an orchestrator claims work only inside a run window you open. "+
			"%s restrict automated use of a subscription, and no vendor has approved this use. "+
			"Read docs/onboarding.md, and ropeshift worker risk prints what a member acknowledges before this worker runs unattended.", name, v.terms)
	default:
		line = fmt.Sprintf("%s: installed, and its sign-in could not be read, so it counts as a subscription and this worker starts at attended. Run %s auth status to see it.",
			name, v.command)
	}
	if !driver.Qualified {
		line += " This version is not in the qualification matrix, so the worker probes it before its first lane, and a lane on it runs degraded if the probe fails."
	}
	return line
}

// failedCheck says a driver's CLI is on PATH and failed its version check, so nothing runs on it. The
// doctor's detail then names the vendor's command as it ran it, and how it failed.
func failedCheck(driver ledger.DriverManifest, v vendor) bool {
	return !driver.Present && strings.HasPrefix(driver.Detail, v.command+" --version ")
}

// initSample writes the sample contract, and keeps one already there, which a person may have changed.
func (c *invocation) initSample(_ *api.Client, result *initResult) error {
	path := filepath.Join(c.stateDir, SampleContractName)
	result.Sample = path
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		c.say("Sample contract: kept %s.\n", path)
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := file.WriteString(sampleContract); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	result.Written = true
	c.say("Sample contract: wrote %s.\n  Copy it into a repository, change it to your own work, and commit it, and intake reads it.\n", path)
	return nil
}

// say prints one part of a run as it happens. With --json, the run prints one document at its end.
func (c *invocation) say(format string, args ...any) {
	if c.asJSON {
		return
	}
	fmt.Fprintf(c.stdout, format, args...)
}
