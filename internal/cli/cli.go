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

	"ropeshift.dev/ropeshift/internal/api"
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
)

const usage = `usage: ropeshift <command>

  work create --title T [--criterion C]... [flags]   record a work order
  work list [--state S]                              list work orders
  work show <id>                                     show one work order
  work review <id>                                   record a passed review
  work claim <id> --worker W [--claim-id C]          claim a work order
  work release <id> --claim-id C --fence N           give a claim back
  work reassign <id> --reason R                      end a lapsed holder's ownership
  lane start <work-order> [--lane L]                 run a lane on a claimed work order
  source list                                        list sources and their health
  lane list                                          list lanes, each with its freshness
  lane show <lane>                                   show one lane, with its freshness
  lane reset <work-order>                            let a lane its breaker stopped start again
  lane archive <lane> [--force]                      remove a lane's worktree, which keeps its branch
  worker list                                        list registered workers
  worker token                                       make a join token
  version                                            print the version

Every command takes --json and --socket. docs/cli.md lists the exit codes.
`

// IsCommand reports a first word this package handles.
func IsCommand(word string) bool {
	return word == "work" || word == "lane" || word == "source" || word == "worker"
}

// Options is what a command needs from its process.
type Options struct {
	Socket string
}

// invocation is one running command.
type invocation struct {
	ctx    context.Context
	stdout io.Writer
	stderr io.Writer
	name   string
	socket string
	asJSON bool
}

// Run runs one command and returns its exit code. args starts after the program
// name.
func Run(args []string, stdout, stderr io.Writer, opts Options) int {
	base := &invocation{ctx: context.Background(), stdout: stdout, stderr: stderr, socket: opts.Socket}
	if len(args) < 2 {
		fmt.Fprint(stderr, usage)
		return ExitUsage
	}
	commands := map[string]func(*invocation, []string) int{
		"work create":   (*invocation).workCreate,
		"work list":     (*invocation).workList,
		"work show":     (*invocation).workShow,
		"work review":   (*invocation).workReview,
		"work claim":    (*invocation).workClaim,
		"work release":  (*invocation).workRelease,
		"work reassign": (*invocation).workReassign,
		"lane start":    (*invocation).laneStart,
		"lane reset":    (*invocation).laneReset,
		"lane archive":  (*invocation).laneArchive,
		"lane list":     (*invocation).laneList,
		"lane show":     (*invocation).laneShow,
		"source list":   (*invocation).sourceList,
		"worker list":   (*invocation).workerList,
		"worker token":  (*invocation).workerToken,
	}
	base.name = args[0] + " " + args[1]
	command, ok := commands[base.name]
	if !ok {
		fmt.Fprintf(stderr, "ropeshift: unknown command %q\n\n%s", base.name, usage)
		return ExitUsage
	}
	return command(base, args[2:])
}

// flags makes a flag set with what every command shares. Parsing stops at no
// position, so an ID may come before or after the flags.
func (c *invocation) flags() *flag.FlagSet {
	set := flag.NewFlagSet("ropeshift "+c.name, flag.ContinueOnError)
	set.SetOutput(c.stderr)
	set.BoolVar(&c.asJSON, "json", false, "print as JSON")
	set.StringVar(&c.socket, "socket", c.socket, "the control plane's socket")
	return set
}

// parse reads flags that may surround the positional arguments, and returns those
// arguments.
func parse(set *flag.FlagSet, args []string) ([]string, bool) {
	var positional []string
	for {
		if err := set.Parse(args); err != nil {
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

func (c *invocation) client() *api.Client {
	return api.NewUnixClient(c.socket)
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
	var refused *api.Error
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
	case api.CodeForbidden:
		return ExitForbidden
	case api.CodeFenced:
		return ExitFenced
	case api.CodeLapsed:
		return ExitLapsed
	case api.CodeLost, api.CodeIneligible, api.CodeNotLapsed, api.CodeIllegalMove, api.CodeUnpushedWork:
		return ExitConflict
	case api.CodeInvalid:
		return ExitUsage
	}
	return ExitError
}
