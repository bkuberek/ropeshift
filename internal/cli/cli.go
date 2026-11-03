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
	"maps"
	"slices"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/ledger"
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

  top [--once]                                       watch the fleet, by who needs a person
  health                                             say the control plane answers, and at what revision
  work create --title T [--criterion C]... [flags]   record a work order, or planning work with --kind plan
  work list [--state S] [--worker W]                 list work orders, or what a worker can run
  work show <id>                                     show one work order
  work review <id>                                   record a passed review
  work acceptance <id>                               show what a work order's evidence leaves to do
  work accept <id>                                   move completed work to done
  work rework <id> --reason R                        send completed work back for another attempt
  work claim <id> --worker W [--claim-id C]          claim a work order
  work release <id> --claim-id C --fence N           give a claim back
  work write <id> --claim-id C --fence N --payload P store one event under a fence
  work reassign <id> --reason R                      end a lapsed holder's ownership
  work cancel <id> --reason R                        end work for good, which stops its lane
  work send-back <id> --reason R                     return reviewed work to planning
  work resolve <id> --reason R                       return blocked work to ready
  work replan <id> --reason R                        return blocked work to planning
  plan list                                          list plans
  plan show <id>                                     show a plan, its goal, contracts, and work
  plan review <id> [--id I]                          start a contract review a lane can run
  requirement list                                   list requirements
  requirement get <key>                              show one requirement's newest version
  question list [--category C] [--status S] [--scope S] [--text T]
                                                     find questions
  question show <id>                                 show one question
  question record --text T [--category C]... [flags] record a question as it is
  question dismiss <id>                              close a question with no answer
  question answer <id> --answer A [flags]            answer a question, once
  question batches [--category C]                    show what reached members and still waits
  decision list [--category C] [--status S] [--scope S] [--text T]
                                                     find decisions
  decision show <id>                                 show one decision
  decision record --statement S [flags]              record a decision
  decision overturn <id> --statement S [flags]       reverse a decision, which creates rework
  policy list                                        list the versions of the decision policy
  policy show <version>                              show one version as it was written
  policy publish --file F                            publish a new version
  category list                                      list the workspace's categories
  category add <name>                                add a category
  search <words> [--kind K]... [--limit N]           rank candidates for a question
  document list                                      list documents, each with its digest
  document show <path> [--version V]                 show one document
  document versions <path>                           list one document's versions
  lane start <work-order> [--lane L] [--profile F]   run a lane on a claimed work order
  source list                                        list sources and their health
  lane list                                          list lanes, each with its freshness
  lane show <lane>                                   show one lane, with its freshness
  approval list [--state <state>]                    list what lanes have asked to do
  approval show <id>                                 print a request and the digest it binds to
  approval approve <id> --digest <digest>            let a lane make one call, as you read it
  approval deny <id> --digest <digest> [--reason ..] refuse a request
  effect list                                        list effects a crash left for a person to decide
  effect show <id>                                   show one, and what it lets a person say
  effect decide <id> --outcome O [--reason R]        say happened, not_happened, or retry
  notice list                                        say what each channel was told, and how many times
  telemetry query [--signal S] [--name N] [--lane L] [--since D] [--limit N]
                                                     read the worker's local telemetry, newest first
  telemetry lanes                                    print each lane's context, tokens, and cost
  measures [--window D]                              print the product measures, the north star first
  view <name> [--category C]                         print a read model, the web UI's data
  events [--after N] [--limit N]                     follow the ledger's events from a revision
  lane reset <work-order>                            let a lane its breaker stopped start again
  lane respawn <work-order>                          ask for a running lane's next generation
  lane resume <work-order>                           give a parked lane a member's word
  lane archive <lane> [--force]                      remove a lane's worktree, which keeps its branch
  message send <lane> --body B [--kind K] [--id I] [--reply-to R] [--generation G]
                                                     send a lane a message, which grants nothing
  message show <id>                                  say what became of a message
  message list <lane>                                list what was sent to a lane
  fleet show                                         show the fleet's mode and caps
  fleet run|pause|drain                              set the fleet's mode
  fleet caps [--wip N] [--per-repository N] [--per-worker N]
                                                     set what a claim is held to
  login                                              print a session for this shell, for eval
  sign-out                                           end this shell's session
  passphrase set                                     set the passphrase, on the terminal
  worker doctor                                      report which agent CLIs are usable here
  orchestrator show <worker>                         show a worker's orchestrator
  orchestrator start <worker>                        run one, or reset a breaker that stopped it
  orchestrator stop <worker>                         stop it, which stops no lane
  worker list                                        list registered workers
  worker token                                       make a join token
  worker autonomy <id>                               show a worker's level and run window
  worker level <id> manual|attended|unattended       choose a worker's level
  worker risk                                        print the stated risk and its digest
  worker acknowledge-risk <id> --digest D            stand behind that statement, for one worker
  worker window-open <id> [--for D] [--id I]         let an attended worker claim for a while
  worker window-close <id>                           end its window, which stops no lane
  init                                               set up this machine: passphrase, worker, doctor, sample
  backup <file>                                      copy the ledger to a new file while the daemon runs
  version                                            print the version

Every command takes --json and --socket. docs/cli.md lists the exit codes.
`

// IsCommand reports a first word this package handles.
func IsCommand(word string) bool {
	switch word {
	case "work", "lane", "source", "worker", "fleet", "plan", "requirement", "document",
		"question", "decision", "policy", "category", "search", "login", "sign-out", "passphrase", "message", "approval", "orchestrator",
		"effect", "view", "events", "health", "top", "notice", "telemetry", "measures", "init", "backup":
		return true
	}
	return false
}

// Options is what a command needs from its process.
type Options struct {
	Socket string
	// Doctor asks this machine what it can run. It reaches no daemon.
	Doctor func(context.Context) (ledger.Manifest, error)
	// Session is the member's session this shell holds, if any.
	Session string
	// ReadPassphrase asks a person on their terminal. Nil means there is no terminal.
	ReadPassphrase func(prompt string) (string, error)
	// Terminal is the keyboard, when both ends of this process are a terminal. A live view needs one.
	Terminal io.Reader
	// StartDaemon runs the daemon for the length of one command, and returns how to stop it. A first
	// run needs one, because a daemon's first start registers its worker.
	StartDaemon func(context.Context) (func() error, error)
	// StateDir is the installation's own directory, where a first run writes its sample contract.
	StateDir string
}

// invocation is one running command.
type invocation struct {
	doctor         func(context.Context) (ledger.Manifest, error)
	readPassphrase func(prompt string) (string, error)
	terminal       io.Reader
	startDaemon    func(context.Context) (func() error, error)
	stateDir       string
	ctx            context.Context
	stdout         io.Writer
	stderr         io.Writer
	name           string
	socket         string
	session        string
	asJSON         bool
}

// Run runs one command and returns its exit code. args starts after the program
// name.
func Run(args []string, stdout, stderr io.Writer, opts Options) int {
	base := &invocation{
		ctx: context.Background(), stdout: stdout, stderr: stderr, socket: opts.Socket, doctor: opts.Doctor,
		session: opts.Session, readPassphrase: opts.ReadPassphrase, terminal: opts.Terminal,
		startDaemon: opts.StartDaemon, stateDir: opts.StateDir,
	}
	if len(args) > 0 {
		if command, ok := singleWords[args[0]]; ok {
			base.name = args[0]
			return command(base, args[1:])
		}
	}
	if len(args) < 2 {
		fmt.Fprint(stderr, usage)
		return ExitUsage
	}
	base.name = args[0] + " " + args[1]
	command, ok := commands()[base.name]
	if !ok {
		fmt.Fprintf(stderr, "ropeshift: unknown command %q\n\n%s", base.name, usage)
		return ExitUsage
	}
	return command(base, args[2:])
}

// singleWords are the commands that take no second word.
var singleWords = map[string]func(*invocation, []string) int{
	"login":    (*invocation).login,
	"sign-out": (*invocation).signOut,
	"events":   (*invocation).events,
	"search":   (*invocation).search,
	"health":   (*invocation).health,
	"top":      (*invocation).runTop,
	"measures": (*invocation).measuresCommand,
	"init":     (*invocation).initMachine,
	"backup":   (*invocation).backup,
}

// HasCommand reports a command, as a person would type it.
func HasCommand(words string) bool {
	if _, ok := singleWords[words]; ok {
		return true
	}
	_, ok := commands()[words]
	return ok
}

// Commands lists every command, as a person would type it.
func Commands() []string {
	names := slices.Collect(maps.Keys(singleWords))
	return slices.Sorted(slices.Values(append(names, slices.Collect(maps.Keys(commands()))...)))
}

// commands maps two words to a command.
func commands() map[string]func(*invocation, []string) int {
	table := map[string]func(*invocation, []string) int{
		"work create":             (*invocation).workCreate,
		"work list":               (*invocation).workList,
		"work show":               (*invocation).workShow,
		"work review":             (*invocation).workReview,
		"work acceptance":         (*invocation).workAcceptance,
		"work accept":             (*invocation).workAccept,
		"work rework":             (*invocation).workRework,
		"work claim":              (*invocation).workClaim,
		"work release":            (*invocation).workRelease,
		"work reassign":           (*invocation).workReassign,
		"work write":              (*invocation).workWrite,
		"work cancel":             (*invocation).workCancel,
		"work send-back":          (*invocation).workSendBack,
		"work resolve":            (*invocation).workResolve,
		"work replan":             (*invocation).workReplan,
		"plan list":               (*invocation).planList,
		"plan show":               (*invocation).planShow,
		"plan review":             (*invocation).planReview,
		"requirement list":        (*invocation).requirementList,
		"requirement get":         (*invocation).requirementGet,
		"document list":           (*invocation).documentList,
		"document show":           (*invocation).documentShow,
		"document versions":       (*invocation).documentVersions,
		"question list":           (*invocation).questionList,
		"question show":           (*invocation).questionShow,
		"question record":         (*invocation).questionRecord,
		"question dismiss":        (*invocation).questionDismiss,
		"question answer":         (*invocation).questionAnswer,
		"question batches":        (*invocation).questionBatches,
		"decision list":           (*invocation).decisionList,
		"decision show":           (*invocation).decisionShow,
		"decision record":         (*invocation).decisionRecord,
		"decision overturn":       (*invocation).decisionOverturn,
		"policy list":             (*invocation).policyList,
		"policy show":             (*invocation).policyShow,
		"policy publish":          (*invocation).policyPublish,
		"category list":           (*invocation).categoryList,
		"category add":            (*invocation).categoryAdd,
		"approval list":           (*invocation).approvalList,
		"approval show":           (*invocation).approvalShow,
		"approval approve":        (*invocation).approvalApprove,
		"approval deny":           (*invocation).approvalDeny,
		"lane start":              (*invocation).laneStart,
		"lane reset":              (*invocation).laneReset,
		"lane respawn":            (*invocation).laneRespawn,
		"lane resume":             (*invocation).laneResume,
		"lane archive":            (*invocation).laneArchive,
		"lane list":               (*invocation).laneList,
		"lane show":               (*invocation).laneShow,
		"message send":            (*invocation).messageSend,
		"message show":            (*invocation).messageShow,
		"message list":            (*invocation).messageList,
		"source list":             (*invocation).sourceList,
		"fleet show":              (*invocation).fleetShow,
		"fleet run":               setsFleet(ledger.FleetRun),
		"fleet pause":             setsFleet(ledger.FleetPause),
		"fleet drain":             setsFleet(ledger.FleetDrain),
		"fleet caps":              (*invocation).fleetCaps,
		"passphrase set":          (*invocation).passphraseSet,
		"worker doctor":           (*invocation).workerDoctor,
		"worker list":             (*invocation).workerList,
		"worker token":            (*invocation).workerToken,
		"worker autonomy":         (*invocation).workerAutonomy,
		"worker level":            (*invocation).workerLevel,
		"worker risk":             (*invocation).workerRisk,
		"worker acknowledge-risk": (*invocation).workerAcknowledge,
		"worker window-open":      (*invocation).workerWindowOpen,
		"worker window-close":     (*invocation).workerWindowClose,
		"orchestrator show":       (*invocation).orchestratorShow,
		"orchestrator start":      setsOrchestrator(ledger.OrchestratorRunning),
		"orchestrator stop":       setsOrchestrator(ledger.OrchestratorStopped),
		"notice list":             (*invocation).noticeList,
		"telemetry query":         (*invocation).telemetryQuery,
		"telemetry lanes":         (*invocation).telemetryLanes,
		"effect list":             (*invocation).effectList,
		"effect show":             (*invocation).effectShow,
		"effect decide":           (*invocation).effectDecide,
	}
	for _, name := range api.Views {
		table["view "+name] = viewing(name)
	}
	return table
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

// client returns the API client. A command that changes something and has no session asks
// for the passphrase here, so a read never asks.
func (c *invocation) client() *api.Client {
	client := api.NewUnixClient(c.socket)
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
	case api.CodeForbidden, api.CodeSessionRequired, api.CodeBadPassphrase, api.CodeNotEnrolled, api.CodeOneWay, api.CodeRiskNotAcknowledged:
		return ExitForbidden
	case api.CodeFenced:
		return ExitFenced
	case api.CodeLapsed:
		return ExitLapsed
	case api.CodeNotAcceptable, api.CodeLost, api.CodeIneligible, api.CodeNotLapsed, api.CodeIllegalMove, api.CodeUnpushedWork, api.CodeResolved,
		api.CodeOverlap, api.CodeDecisionEnded, api.CodeNotHeld, api.CodeWrongRung, api.CodeAssumptionLimit, api.CodeWindowOpen, api.CodeStaleStatement,
		api.CodeEffectDecided, api.CodeCannotRepeat:
		return ExitConflict
	case api.CodeInvalid:
		return ExitUsage
	}
	return ExitError
}
