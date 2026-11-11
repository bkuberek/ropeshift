package cli

import (
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
)

// registered is one command: what help says about it, and what runs it.
type registered struct {
	Command
	run func(*invocation, []string) int
	// installation says the command acts on the installation's own records, so it takes no project
	// (ADR-0021). Every other command acts on one project's.
	installation bool
}

// section is the commands help prints under one heading.
type section struct {
	title    string
	commands []registered
}

func command(name, args, summary string, run func(*invocation, []string) int) registered {
	return registered{Command: Command{Name: name, Args: args, Summary: summary}, run: run}
}

// installation marks commands on the installation's own records: projects, members and sessions,
// workers and their orchestrators, the worker's telemetry, and this machine.
func installation(commands ...registered) []registered {
	for i := range commands {
		commands[i].installation = true
	}
	return commands
}

// registry is every command this package runs, in the groups and the order help prints them.
// Routing, help, IsCommand, HasCommand, and Commands all read it, so no command runs without a line
// in help.
func registry() []section {
	return []section{
		{"Watch", append([]registered{
			command("top", "[--once] [--width N]", "watch the fleet, by who needs a person", (*invocation).runTop),
			installation(command("health", "", "say the control plane answers, and at what revision", (*invocation).health))[0],
			command("events", "[--after N] [--limit N]", "follow the ledger's events from a revision", (*invocation).events),
		}, views()...)},
		{"Projects", installation(
			command("project list", "", "list the installation's projects, each with its state and repositories", (*invocation).projectList),
			command("project show", "<name>", "show a project's record, its database, and its repositories", (*invocation).projectShow),
		)},
		{"Work orders", []registered{
			command("work create", "--title T [--criterion C]... [flags]", "record a work order, or planning work with --kind plan", (*invocation).workCreate),
			command("work list", "[--state S] [--worker W]", "list work orders, or what a worker can run", (*invocation).workList),
			command("work show", "<id>", "show one work order", (*invocation).workShow),
			command("work review", "<id>", "record a passed review", (*invocation).workReview),
			command("work acceptance", "<id>", "show what a work order's evidence leaves to do", (*invocation).workAcceptance),
			command("work accept", "<id>", "move completed work to done", (*invocation).workAccept),
			command("work rework", "<id> --reason R", "send completed work back for another attempt", (*invocation).workRework),
			command("work claim", "<id> --worker W [--claim-id C]", "claim a work order", (*invocation).workClaim),
			command("work release", "<id> --claim-id C --fence N", "give a claim back", (*invocation).workRelease),
			command("work write", "<id> --claim-id C --fence N --payload P", "store one event under a fence", (*invocation).workWrite),
			command("work reassign", "<id> --reason R", "end a lapsed holder's ownership", (*invocation).workReassign),
			command("work cancel", "<id> --reason R", "end work for good, which stops its lane", (*invocation).workCancel),
			command("work send-back", "<id> --reason R", "return reviewed work to planning", (*invocation).workSendBack),
			command("work resolve", "<id> --reason R", "return blocked work to ready", (*invocation).workResolve),
			command("work replan", "<id> --reason R", "return blocked work to planning", (*invocation).workReplan),
		}},
		{"Plans and requirements", []registered{
			command("plan start", "[--goal TEXT | --goal-file PATH] [--worker ID] [--repository R] [--no-attach]", "open a planning session and attach your terminal to it", (*invocation).planStart),
			command("plan list", "", "list plans", (*invocation).planList),
			command("plan show", "<id>", "show a plan, its goal, contracts, and work, and the approval that waits with what it changes", (*invocation).planShow),
			command("plan approve", "<id> [--version N]", "approve the plan version that waits, which decides its draft decisions and passes the review of its work", (*invocation).planApprove),
			command("plan review", "<id> [--id I]", "start a contract review a lane can run", (*invocation).planReview),
			command("requirement list", "", "list requirements", (*invocation).requirementList),
			command("requirement get", "<key>", "show one requirement's newest version", (*invocation).requirementGet),
		}},
		{"Questions and decisions", []registered{
			command("question list", "[--category C] [--status S] [--scope S] [--text T]", "find questions", (*invocation).questionList),
			command("question show", "<id>", "show one question", (*invocation).questionShow),
			command("question record", "--text T [--category C]... [flags]", "record a question as it is", (*invocation).questionRecord),
			command("question dismiss", "<id>", "close a question with no answer", (*invocation).questionDismiss),
			command("question answer", "<id> --answer A [flags]", "answer a question, once", (*invocation).questionAnswer),
			command("question batches", "[--category C]", "show what reached members and still waits", (*invocation).questionBatches),
			command("decision list", "[--category C] [--status S] [--scope S] [--text T]", "find decisions", (*invocation).decisionList),
			command("decision show", "<id>", "show one decision", (*invocation).decisionShow),
			command("decision record", "--statement S [flags]", "record a decision", (*invocation).decisionRecord),
			command("decision overturn", "<id> --statement S [flags]", "reverse a decision, which creates rework", (*invocation).decisionOverturn),
			command("category list", "", "list the workspace's categories", (*invocation).categoryList),
			command("category add", "<name>", "add a category", (*invocation).categoryAdd),
			command("search", "<words> [--kind K]... [--limit N]", "rank candidates for a question", (*invocation).search),
		}},
		{"Policies", []registered{
			command("policy list", "[--kind K]", "list the versions of a policy, newest first", (*invocation).policyList),
			command("policy show", "<version> [--kind K]", "show one version as it was written", (*invocation).policyShow),
			command("policy publish", "--file F", "publish a new version", (*invocation).policyPublish),
		}},
		{"Documents and sources", []registered{
			command("document list", "", "list documents, each with its digest", (*invocation).documentList),
			command("document show", "<path> [--version V] [--instance I]", "show one document", (*invocation).documentShow),
			command("document versions", "<path> [--instance I]", "list one document's versions", (*invocation).documentVersions),
			command("document recorded", "<kind>:<id> [--version N]", "show a planning document's version as the ledger stored it", (*invocation).documentRecorded),
			command("source list", "", "list sources and their health", (*invocation).sourceList),
		}},
		{"Lanes and messages", []registered{
			command("lane start", "<work-order> [--lane L] [--profile F]", "run a lane on a claimed work order", (*invocation).laneStart),
			command("lane attach", "<lane>", "put your terminal on an interactive lane's pane, which detaching leaves running", (*invocation).laneAttach),
			command("lane list", "", "list lanes, each with its freshness", (*invocation).laneList),
			command("lane show", "<lane>", "show one lane, with its freshness", (*invocation).laneShow),
			command("lane reset", "<work-order>", "let a lane its breaker stopped start again", (*invocation).laneReset),
			command("lane respawn", "<work-order>", "ask for a running lane's next generation", (*invocation).laneRespawn),
			command("lane resume", "<work-order>", "give a parked lane a member's word", (*invocation).laneResume),
			command("lane archive", "<lane> [--force]", "remove a lane's worktree, which keeps its branch", (*invocation).laneArchive),
			command("message send", "<lane> --body B [--kind K] [--id I] [--reply-to R] [--generation G]", "send a lane a message, which grants nothing", (*invocation).messageSend),
			command("message show", "<id>", "say what became of a message", (*invocation).messageShow),
			command("message list", "<lane>", "list what was sent to a lane", (*invocation).messageList),
		}},
		{"Repositories", []registered{
			command("repository list", "", "list each repository, the branch its lanes start from, and how it merges", (*invocation).repositoryList),
			command("repository show", "<id>", "show a repository's record: remote, default branch, base, merge method, and start", (*invocation).repositoryShow),
			command("repository set", "<id> [--base B] [--merge-method M] [--start-from S] [--integration on|off]", "change a repository's settings, which only a member can do", (*invocation).repositorySet),
		}},
		{"Approvals, effects, and notices", []registered{
			command("approval list", "[--state S]", "list what lanes have asked to do", (*invocation).approvalList),
			command("approval show", "<id>", "print a request and the digest it binds to", (*invocation).approvalShow),
			command("approval approve", "<id> --digest D [--reason R]", "let a lane make one call, as you read it", (*invocation).approvalApprove),
			command("approval deny", "<id> --digest D [--reason R]", "refuse a request", (*invocation).approvalDeny),
			command("effect list", "", "list effects a crash left for a person to decide", (*invocation).effectList),
			command("effect show", "<id>", "show one effect, and what it lets a person say", (*invocation).effectShow),
			command("effect decide", "<id> --outcome O [--reason R]", "say an effect happened, not_happened, or may retry", (*invocation).effectDecide),
			command("notice list", "", "say what each channel was told, and how many times", (*invocation).noticeList),
		}},
		{"Telemetry and measures", append(installation(
			command("telemetry query", "[--signal S] [--name N] [--lane L] [--since D] [--limit N]", "read the worker's local telemetry, newest first", (*invocation).telemetryQuery),
			command("telemetry lanes", "", "print each lane's context, tokens, and cost", (*invocation).telemetryLanes),
		),
			command("measures", "[--window D]", "print the product measures, the north star first", (*invocation).measuresCommand),
		)},
		{"Fleet", []registered{
			command("fleet show", "", "show the fleet's mode and caps", (*invocation).fleetShow),
			command("fleet run", "", "let the fleet take claims and start lanes", setsFleet(ledger.FleetRun)),
			command("fleet pause", "", "take no new claims and start no new lanes", setsFleet(ledger.FleetPause)),
			command("fleet drain", "", "pause, and start no new generations either", setsFleet(ledger.FleetDrain)),
			command("fleet caps", "[--wip N] [--per-repository N] [--per-worker N]", "set what a claim is held to", (*invocation).fleetCaps),
		}},
		{"Workers and orchestrators", installation(
			command("worker list", "", "list registered workers", (*invocation).workerList),
			command("worker token", "", "make a join token", (*invocation).workerToken),
			command("worker doctor", "", "report which agent CLIs are usable here", (*invocation).workerDoctor),
			command("worker autonomy", "<id>", "show a worker's level and run window", (*invocation).workerAutonomy),
			command("worker level", "<id> manual|attended|unattended", "choose a worker's level", (*invocation).workerLevel),
			command("worker risk", "", "print the stated risk and its digest", (*invocation).workerRisk),
			command("worker acknowledge-risk", "<id> --digest D", "stand behind the stated risk, for one worker", (*invocation).workerAcknowledge),
			command("worker window-open", "<id> [--for D] [--id I]", "let an attended worker claim for a while", (*invocation).workerWindowOpen),
			command("worker window-close", "<id>", "end a worker's run window, which stops no lane", (*invocation).workerWindowClose),
			command("orchestrator show", "<worker>", "show a worker's orchestrator", (*invocation).orchestratorShow),
			command("orchestrator start", "<worker>", "run a worker's orchestrator, or reset a breaker that stopped it", setsOrchestrator(ledger.OrchestratorRunning)),
			command("orchestrator stop", "<worker>", "stop a worker's orchestrator, which stops no lane", setsOrchestrator(ledger.OrchestratorStopped)),
		)},
		{"Sessions", installation(
			command("login", "", "print a session for this shell, for eval", (*invocation).login),
			command("sign-out", "", "end this shell's session", (*invocation).signOut),
			command("passphrase set", "", "set the passphrase, on the terminal", (*invocation).passphraseSet),
		)},
		{"This machine", []registered{
			installation(command("init", "", "set up this machine: passphrase, worker, doctor, sample", (*invocation).initMachine))[0],
			command("backup", "<file> [--installation]", "copy the project's ledger, or the installation's records, to a new file while the daemon runs", (*invocation).backup),
			installation(command("service install", "[--name N] [-- daemon flags]", "run the daemon as this machine's service, from now on", (*invocation).serviceInstall))[0],
			installation(command("service uninstall", "[--name N]", "stop the daemon's service and remove it, which stops no lane", (*invocation).serviceUninstall))[0],
		}},
	}
}

// views are the read models of the web UI, one command each, which the list of every command
// prints as one line.
func views() []registered {
	all := make([]registered, 0, len(api.Views))
	for _, name := range api.Views {
		all = append(all, registered{
			Command: Command{
				Name: "view " + name, Args: "[--category C]", Summary: "print one read model of the web UI, as JSON",
				Family: "view <name> [--category C]",
			},
			run: viewing(name),
		})
	}
	return all
}

// find returns the command a command line names, and the arguments after its name.
func find(args []string) (registered, []string, bool) {
	byName := map[string]registered{}
	for _, s := range registry() {
		for _, c := range s.commands {
			byName[c.Name] = c
		}
	}
	if len(args) > 0 {
		if c, ok := byName[args[0]]; ok {
			return c, args[1:], true
		}
	}
	if len(args) > 1 {
		if c, ok := byName[args[0]+" "+args[1]]; ok {
			return c, args[2:], true
		}
	}
	return registered{}, nil, false
}

// under returns the commands whose first word is word and that have a second.
func under(word string) []Command {
	var commands []Command
	for _, s := range registry() {
		for _, c := range s.commands {
			if strings.HasPrefix(c.Name, word+" ") {
				commands = append(commands, c.Command)
			}
		}
	}
	return commands
}

// Groups returns what help says about every command this package runs, in the groups and the order
// help prints them.
func Groups() []Group {
	sections := registry()
	groups := make([]Group, 0, len(sections))
	for _, s := range sections {
		group := Group{Title: s.title}
		for _, c := range s.commands {
			group.Commands = append(group.Commands, c.Command)
		}
		groups = append(groups, group)
	}
	return groups
}

// IsCommand reports a first word this package handles.
func IsCommand(word string) bool {
	return HasCommand(word) || len(under(word)) > 0
}

// HasCommand reports a command, as a person would type it.
func HasCommand(words string) bool {
	return slices.Contains(Commands(), words)
}

// Commands lists every command, as a person would type it.
func Commands() []string {
	var names []string
	for _, s := range registry() {
		for _, c := range s.commands {
			names = append(names, c.Name)
		}
	}
	slices.Sort(names)
	return names
}
