package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/templates"
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
	// Profile is the user profile's path, and ProfileWritten says this run wrote it. WorktreeRoot is the root
	// this run wrote into it, which a run with no --worktree-root, or one that kept a profile, does not.
	Profile        string `json:"profile"`
	ProfileWritten bool   `json:"profile_written"`
	WorktreeRoot   string `json:"worktree_root,omitempty"`
	// Worktree is the doctor's worktree section.
	Worktree *lanes.WorktreeReport `json:"worktree,omitempty"`
}

// profileRoot is what --worktree-root asks of the user profile: Root is the root to write, which is the path
// given with the built-in root's tail appended when the path names neither token, and Given says the flag was
// used.
type profileRoot struct {
	Root     string
	Given    bool
	Appended bool
}

// worktreeRootOf reads --worktree-root. A path that is not absolute is a usage problem, because a relative root is
// read from each repository and a profile written by init names one place. A path that names neither {project}
// nor {repo} gets the built-in root's tail, so lanes of different projects and repositories never share a folder.
func worktreeRootOf(given string) (profileRoot, error) {
	if !path.IsAbs(given) {
		return profileRoot{}, usageProblem(fmt.Sprintf("--worktree-root %q: give an absolute path, because a relative root is read from each repository", given))
	}
	root := profileRoot{Root: given, Given: true}
	if !strings.Contains(given, lanes.ProjectToken) && !strings.Contains(given, lanes.RepositoryToken) {
		root.Root = strings.TrimRight(given, "/") + "/" + lanes.ProjectToken + "/" + lanes.RepositoryToken
		root.Appended = true
	}
	return root, nil
}

// initMachine sets up this machine for solo mode: the passphrase, the worker's registration, the user
// profile, the doctor, and a sample contract. A step already done stays as it is, so a second run only
// says what is left. With no daemon running, it runs one for its own length, because a first start is
// what registers the worker.
func (c *invocation) initMachine(args []string) int {
	set := c.flags()
	var given string
	set.StringVar(&given, "worktree-root", "", "the absolute path to set as the worktree root of the user profile, when init writes it")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	var request profileRoot
	set.Visit(func(f *flag.Flag) {
		if f.Name == "worktree-root" {
			request.Given = true
		}
	})
	if request.Given {
		var err error
		if request, err = worktreeRootOf(given); err != nil {
			return c.usageError("%s", err)
		}
	}
	if c.doctor == nil || c.startDaemon == nil || c.stateDir == "" {
		return c.usageError("this build cannot set up a machine")
	}
	if code := c.requireGit(); code != ExitOK {
		return code
	}
	client := c.client()
	defer client.Close()
	var result initResult
	stop, err := c.reachDaemon(client, &result)
	if err != nil {
		return c.failure(err)
	}
	code, done := c.setUp(client, &result, request)
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
func (c *invocation) setUp(client *api.Client, result *initResult, request profileRoot) (int, bool) {
	profile := func(_ *api.Client, result *initResult) error { return c.initProfile(result, request) }
	for _, step := range []func(*api.Client, *initResult) error{c.initPassphrase, c.initWorker, profile, c.initDoctor, c.initSample} {
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
		c.say("Next: run the daemon with ropeshift service install (brew services start ropeshift on a Homebrew install), or in the foreground with ropeshiftd solo --source <repository>.\n")
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
	if _, err := client.SetPassphrase(c.ctx, api.SetPassphrase{Passphrase: passphrase}); err != nil {
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
	section, err := c.worktreeSection()
	if err != nil {
		return err
	}
	if section != nil {
		result.Worktree = section
		c.say("%s", worktreeText(*section))
	}
	return nil
}

// initDriverLine says where one agent CLI stands, and what a person does next. Only a kind and a
// status reach it, never who is signed in.
func initDriverLine(driver ledger.DriverManifest) string {
	v := ledger.VendorOf(driver.Driver)
	if driver.FailedCheck() {
		return fmt.Sprintf("%s: installed, and its version check failed: %s. Run %s --version to see why, repair or reinstall %s, and run ropeshift init again.",
			driver.Driver, driver.Detail, v.Command, v.Product)
	}
	if !driver.Present {
		return fmt.Sprintf("%s: not installed. Install %s, sign in through %s's own flow, and run ropeshift init again.", driver.Driver, v.Product, v.Company)
	}
	name := driver.Driver + " " + driver.Version
	var line string
	switch {
	case driver.SignIn.Status == lanes.SignedOut:
		return fmt.Sprintf("%s: installed, not signed in. Run %s and sign in through %s's own flow, or give the CLI an API key it reads itself, then run ropeshift init again.",
			name, v.Command, v.Company)
	case driver.SignIn.Kind == lanes.SignInAPIKey:
		line = fmt.Sprintf("%s: signed in with an API key, which the CLI reads itself. This worker starts at unattended.", name)
	case driver.SignIn.Kind == lanes.SignInSubscription:
		line = fmt.Sprintf("%s: signed in with a subscription. This worker starts at attended, so an orchestrator claims work only inside a run window you open. "+
			"%s restrict automated use of a subscription, and no vendor has approved this use. "+
			"Read docs/onboarding.md, and ropeshift worker risk prints what a member acknowledges before this worker runs unattended.", name, v.Terms)
	default:
		line = fmt.Sprintf("%s: installed, and its sign-in could not be read, so it counts as a subscription and this worker starts at attended. Run %s auth status to see it.",
			name, v.Command)
	}
	if !driver.Qualified {
		line += " This version is not in the qualification matrix, so the worker probes it before its first lane, and a lane on it runs degraded if the probe fails."
	}
	return line
}

// initProfile writes the user profile in the state directory from its template, with the root the flag gave or
// with the root commented out, and never replaces one that exists, whatever it holds: the file is opened only to
// be created, so a run that races another writes nothing over it. A profile that exists is kept, and when the flag
// was given, the root is the person's to set in it.
func (c *invocation) initProfile(result *initResult, request profileRoot) error {
	file := filepath.Join(c.stateDir, projectfiles.WritesTo(userProfile))
	result.Profile = file
	text, err := templates.Render(userProfile, templates.Data{WorktreeRoot: request.Root})
	if err != nil {
		return err
	}
	out, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		c.say("Profile: kept %s.\n", file)
		if request.Given {
			c.say("  --worktree-root changes no profile that exists: set [worktree] root in it by hand.\n")
		}
		return nil
	}
	if err != nil {
		return err
	}
	_, err = out.WriteString(text)
	if closed := out.Close(); err == nil {
		err = closed
	}
	if err != nil {
		_ = os.Remove(file)
		return fmt.Errorf("write %s: %w", file, err)
	}
	result.ProfileWritten = true
	if !request.Given {
		c.say("Profile: wrote %s.\n  Its worktree root is commented out, so the built-in root holds. Set [worktree] root in it to change that.\n", file)
		return nil
	}
	result.WorktreeRoot = request.Root
	c.say("Profile: wrote %s with [worktree] root = %s.\n", file, request.Root)
	if request.Appended {
		c.say("  The path names neither {project} nor {repo}, so /{project}/{repo} is appended, and lanes of different projects and repositories never share a folder. Edit the file for one folder.\n")
	}
	return nil
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
