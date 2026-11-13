// Package cli is the ropeshift command tree. It reaches the ledger only through
// the API client (FR-UI-4).
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
)

// Exit codes. docs/cli.md documents each one, and a test holds the two together.
const (
	ExitOK          = 0
	ExitError       = 1
	ExitUsage       = 2
	ExitNotFound    = 3
	ExitConflict    = 4
	ExitFenced      = 5
	ExitLapsed      = 6
	ExitNotReady    = 7
	ExitForbidden   = 8
	ExitUnreachable = 9
	// ExitPrepared says the command's operation became a confirmation card: it ran nothing, and it runs
	// when a member confirms the card.
	ExitPrepared = 10
)

// Options is what a command needs from its process.
type Options struct {
	Socket string
	// Doctor asks this machine what it can run. It reaches no daemon.
	Doctor func(context.Context) (ledger.Manifest, error)
	// Session is the member's session this shell holds, if any.
	Session string
	// Delegate is the delegate credential this shell holds, if any, which a command presents in place of a
	// session.
	Delegate string
	// Project is the project a command on project records acts on when it names none, from
	// ROPESHIFT_PROJECT.
	Project string
	// RepositoryHere is the identity of the repository that holds the working directory, or empty when
	// none does. Nil never finds one.
	RepositoryHere func(context.Context) (string, error)
	// RepositoryRoot is the top of the git working tree that holds the working directory, or empty when
	// none does. Nil never finds one.
	RepositoryRoot func(context.Context) (string, error)
	// ReadPassphrase asks a person on their terminal. Nil means there is no terminal.
	ReadPassphrase func(prompt string) (string, error)
	// Terminal is the keyboard, when both ends of this process are a terminal. A live view needs one.
	Terminal io.Reader
	// StartDaemon runs the daemon for the length of one command, and returns how to stop it. A first
	// run needs one, because a daemon's first start registers its worker.
	StartDaemon func(context.Context) (func() error, error)
	// Panes is this machine's terminal server, for a command that puts the person's terminal on a
	// lane's pane. Nil means this machine has no tmux.
	Panes Panes
	// StateDir is the installation's own directory, where a first run writes its sample contract.
	StateDir string
	// InstallService writes the daemon's service definition under a name, with the daemon's own
	// flags, and starts it. An empty name is the platform's. UninstallService stops that service and
	// removes its definition. Nil means this platform has no service manager to write for.
	InstallService   func(ctx context.Context, name string, daemonArgs []string) (Service, error)
	UninstallService func(ctx context.Context, name string) (Service, error)
}

// invocation is one running command.
type invocation struct {
	doctor         func(context.Context) (ledger.Manifest, error)
	readPassphrase func(prompt string) (string, error)
	terminal       io.Reader
	panes          Panes
	startDaemon    func(context.Context) (func() error, error)
	stateDir       string
	install        func(context.Context, string, []string) (Service, error)
	uninstall      func(context.Context, string) (Service, error)
	ctx            context.Context
	stdout         io.Writer
	stderr         io.Writer
	name           string
	socket         string
	session        string
	delegate       string
	// why is the reason a delegate gives, which a card it prepares keeps.
	why    string
	asJSON bool
	// inProject says the command acts on one project's records, so it takes --project.
	inProject bool
	// project is what --project names, and envProject what ROPESHIFT_PROJECT does.
	project        string
	envProject     string
	repositoryHere func(context.Context) (string, error)
	repositoryRoot func(context.Context) (string, error)
	// command is what help says about the running command.
	command Command
	// helped says the command printed its help instead of running.
	helped bool
}

// Run runs one command and returns its exit code. args starts after the program
// name.
func Run(args []string, stdout, stderr io.Writer, opts Options) int {
	base := &invocation{
		ctx: context.Background(), stdout: stdout, stderr: stderr, socket: opts.Socket, doctor: opts.Doctor,
		session: opts.Session, delegate: opts.Delegate, readPassphrase: opts.ReadPassphrase, terminal: opts.Terminal,
		panes: opts.Panes, startDaemon: opts.StartDaemon, stateDir: opts.StateDir, install: opts.InstallService, uninstall: opts.UninstallService,
		envProject: opts.Project, repositoryHere: opts.RepositoryHere, repositoryRoot: opts.RepositoryRoot,
	}
	found, rest, ok := find(args)
	if !ok {
		return base.notFound(args)
	}
	if opts.Session != "" && opts.Delegate != "" {
		fmt.Fprintf(stderr, "%s: %s and %s are both set: a command acts as a member or as a delegate, so unset one\n", program, authority.EnvMemberToken, authority.EnvDelegateToken)
		return ExitUsage
	}
	base.name, base.command, base.inProject = found.Name, found.Command, !found.installation
	code := found.run(base, rest)
	if base.helped {
		return ExitOK
	}
	return code
}

// notFound answers a command line that names no command. With nothing, it lists every command, and
// with a group's first word, the commands under it: on stdout when help is asked for, and otherwise
// on stderr as a usage error. Anything else is unknown, and the answer says where to look.
func (c *invocation) notFound(args []string) int {
	if len(args) == 0 {
		WriteList(c.stderr, program, Groups())
		return ExitUsage
	}
	group := under(args[0])
	switch {
	case len(group) == 0:
		fmt.Fprintf(c.stderr, "%s: unknown command %q\nRun '%s help' for a list of commands.\n", program, args[0], program)
	case len(args) == 1:
		writeGroup(c.stderr, args[0], group)
	case AsksForHelp(args[1]):
		writeGroup(c.stdout, args[0], group)
		return ExitOK
	default:
		fmt.Fprintf(c.stderr, "%s: unknown command %q\nRun '%s help %s' for its commands.\n", program, args[0]+" "+args[1], program, args[0])
	}
	return ExitUsage
}

// flags makes a flag set with what every command shares. Parsing stops at no
// position, so an ID may come before or after the flags. Help and a flag error print
// through parse, so the flag package's own usage prints nothing.
func (c *invocation) flags() *flag.FlagSet {
	set := flag.NewFlagSet("ropeshift "+c.name, flag.ContinueOnError)
	set.SetOutput(c.stderr)
	set.Usage = func() {}
	set.BoolVar(&c.asJSON, "json", false, "print as JSON")
	set.StringVar(&c.socket, "socket", c.socket, "the control plane's socket")
	if c.inProject {
		set.StringVar(&c.project, "project", c.project, "the project this acts on, by name")
		set.StringVar(&c.why, "why", c.why, "why a delegate proposes this, which a card it becomes keeps")
	}
	return set
}

// parse reads flags that may surround the positional arguments, and returns those
// arguments. -h or --help prints the command's help on stdout instead, and a flag
// error names the command's usage, and both stop the command.
func (c *invocation) parse(set *flag.FlagSet, args []string) ([]string, bool) {
	var positional []string
	for {
		if err := set.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				c.helped = true
				WriteUsage(c.stdout, program, c.command, set)
			} else {
				fmt.Fprintf(c.stderr, "usage: %s %s\nRun '%s help %s' for its flags.\n", program, c.command.Usage(), program, c.name)
			}
			return nil, false
		}
		args = set.Args()
		if len(args) == 0 {
			return positional, true
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

func (c *invocation) usageError(format string, args ...any) int {
	fmt.Fprintf(c.stderr, "ropeshift %s: "+format+"\n", append([]any{c.name}, args...)...)
	return ExitUsage
}

// client returns the API client. A command that changes something and has no session asks
// for the passphrase here, so a read never asks.
func (c *invocation) client() *api.Client {
	client := api.NewUnixClient(c.socket)
	client.UseWhy(c.why)
	if c.delegate != "" {
		client.UseDelegate(c.delegate)
		return client
	}
	client.UseSession(c.session)
	client.AskSession(func(ctx context.Context) (string, error) {
		passphrase, err := c.askPassphrase("Passphrase: ")
		if err != nil {
			return "", err
		}
		session, err := client.SignIn(ctx, api.SignIn{Passphrase: passphrase, TTLMs: authority.CommandTTLMs})
		return session.Token, err
	})
	return client
}

// finish prints a result, or explains a failure and maps it to its exit code.
func (c *invocation) finish(err error, result any, text func() string) int {
	if err != nil {
		return c.failure(err)
	}
	if c.asJSON {
		if err := json.NewEncoder(c.stdout).Encode(result); err != nil {
			fmt.Fprintf(c.stderr, "ropeshift %s: %v\n", c.name, err)
			return ExitError
		}
		return ExitOK
	}
	fmt.Fprint(c.stdout, text())
	return ExitOK
}

func (c *invocation) failure(err error) int {
	var prepared *api.Prepared
	if errors.As(err, &prepared) {
		return c.prepared(prepared.Card)
	}
	var refused *api.Error
	if errors.As(err, &refused) && refused.Detail.Code == api.CodeNoProject {
		return c.noProject(refused.Detail)
	}
	if errors.As(err, &refused) {
		c.printError(refused.Detail)
		return exitOf(refused)
	}
	code := api.CodeUnavailable
	exit := ExitError
	if errors.Is(err, api.ErrUnreachable) {
		exit = ExitUnreachable
	}
	c.printError(api.ErrorDetail{Code: code, Message: err.Error()})
	return exit
}

func (c *invocation) printError(detail api.ErrorDetail) {
	if c.asJSON {
		_ = json.NewEncoder(c.stderr).Encode(api.ErrorBody{Error: detail})
		return
	}
	fmt.Fprintf(c.stderr, "ropeshift %s: %s\n", c.name, detail.Message)
}

func exitOf(refused *api.Error) int {
	switch refused.Detail.Code {
	case api.CodeNotFound:
		return ExitNotFound
	case api.CodeForbidden, api.CodeSessionRequired, api.CodeBadPassphrase, api.CodeNotEnrolled, api.CodeOneWay, api.CodeRiskNotAcknowledged,
		api.CodeBadDelegate, api.CodeOutsideProject, api.CodeMemberOnly:
		return ExitForbidden
	case api.CodeFenced:
		return ExitFenced
	case api.CodeLapsed:
		return ExitLapsed
	case api.CodeNotAcceptable, api.CodeLost, api.CodeIneligible, api.CodeNotLapsed, api.CodeIllegalMove, api.CodeUnpushedWork, api.CodeResolved,
		api.CodeOverlap, api.CodeDecisionEnded, api.CodeNotHeld, api.CodeWrongRung, api.CodeAssumptionLimit, api.CodeWindowOpen, api.CodeStaleStatement,
		api.CodeEffectDecided, api.CodeCannotRepeat, api.CodeRepositoryHeld, api.CodeRepositoryState, api.CodeProjectExists, api.CodeProjectState, api.CodeProjectArchived,
		api.CodeAlreadyPlanned, api.CodeRosterConflict, api.CodeDelegateActive, api.CodeNoAssistantWorker, api.CodeTargetNotRunning:
		return ExitConflict
	case api.CodeInvalid, api.CodeNoProject:
		return ExitUsage
	}
	return ExitError
}
