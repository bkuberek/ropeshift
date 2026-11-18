package entry

import (
	"regexp"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
)

// FR-UI-7 says a member does in the web UI everything the CLI does, except what it names as CLI-only. The
// web listener's route list names a command for each row, and TestEveryWebRouteHasACommand holds each row
// to a command. This holds every command to the list the other way: each command has a row, or an entry
// below that says why not. A new command fails here until it has one, so the web UI does not fall behind.
// The same holds for operations (internal/api/server) and for the UI's calls (web/src/api/parity.test.ts).

// equivalents are the commands whose effect the web UI has through the route of another command's row. The
// value is that row's command. Each entry's comment names the CLI handler that gives the effect and the
// web route that gives the same one, as read in both.
var equivalents = map[string]string{
	// Reads. The web UI renders a read model where the CLI prints a record, and the model holds the same
	// records, so a page shows what the command prints.
	"decision list":    "view decisions",  // questions.go lists decisions; the Decisions view lists them with their filters
	"decision show":    "view decisions",  // questions.go reads one decision; Decisions opens it by its ID
	"document list":    "view documents",  // planning.go lists the documents with their digests; the Documents view does
	"effect list":      "needs",           // views.go lists the effects a crash left; Needs you lists each as an effect card
	"effect show":      "needs",           // views.go reads one effect; its card in Needs you says what it lets a member say
	"lane list":        "view shop-floor", // work.go lists lanes with their freshness; Floor lists each lane with its freshness
	"lane show":        "view shop-floor", // work.go reads one lane; Floor and the lane page show it with its freshness
	"message show":     "message list",    // messages.go messageShow reads one message and prints messageLine; the lane page lists each message of the lane's route with its state, reason, and reply
	"measures":         "view operations", // measures.go reads the report for the default window; Measures shows the operations view's report
	"project show":     "project list",    // project.go reads a project's record; Projects lists each with its state and repositories
	"question batches": "needs",           // questions.go lists the batches that reached members; Needs you shows each batch
	"question list":    "view questions",  // questions.go finds questions; the Open questions view lists them with their filters
	"question show":    "view questions",  // questions.go reads one question; Open questions opens it by its ID
	"requirement get":  "view documents",  // planning.go reads a requirement's newest version; the documents view carries every requirement
	"requirement list": "view documents",  // planning.go lists requirements; Traceability lists them from the documents view
	"worker doctor":    "view shop-floor", // work.go workerDoctor prints doctorLine for each driver of this machine; the Workers view shows the same result for each driver of each worker from the floor's worker lines
	"work list":        "view plans",      // work.go lists work orders; the Work orders view lists each by group from the plans view

	// Changes. The CLI command sends the request of a row the UI already sends, with a different value.
	"agent add":         "agent set",          // agent.go agentAdd sends a roster change with the op add, from another agent or with the definition readAgentFile reads from a TOML file; Add agent sends both on the same route, the definition read from a pasted [[agent]] table
	"agent disable":     "agent set",          // agent.go agentToggle sends a roster change with the op disable; Agents sends it from the agent's toggle
	"agent enable":      "agent set",          // agentToggle with the op enable; Agents sends it from the same toggle
	"agent guide":       "agent set",          // agent.go agentGuide sends guide_set or guide_reset as a roster change; the Routing guide tab sends both
	"agent reset":       "agent set",          // agentToggle with the op reset; Agents sends it from Reset
	"agent show":        "agent list",         // agent.go reads one agent from the resolved roster; Agents shows each agent's fields with their sources
	"approval deny":     "approval approve",   // approvals.go approvalDecide sends DecideApproval with Allow false to the decide route; the approval cards send allow false from Deny
	"chat":              "chat send",          // chat.go sends each line as a message to the conversation's messages route; the dock's composer does
	"confirm":           "approval approve",   // delegates.go decideCard, for confirm, sends DecideApproval with Allow true for a card; the confirmation card does
	"decline":           "approval approve",   // delegates.go decideCard, for decline, sends DecideApproval with Allow false for a card; the confirmation card does
	"fleet drain":       "fleet pause",        // work.go setsFleet sends SetFleetMode, the same PUT /fleet/mode as fleet pause, with the mode drain; Settings sends all three modes
	"fleet run":         "fleet pause",        // setsFleet with the mode run, on the same route
	"new monorepo":      "new workspace",      // new.go newFolder writes a shape's starting files; Start a project from a template sends the shape monorepo in the body of the POST /v1/workers/{id}/template-writes that new workspace's row names (templateWriteRoutes takes the shape from the body)
	"new repository":    "new workspace",      // newFolder with the shape repository, and its --project in the body's project; the dialog sends both on the same route
	"orchestrator stop": "orchestrator start", // orchestrator.go setsOrchestrator sends SetOrchestrator, the same PUT as start, with desired stopped; Workers sends both
	"plan approve":      "approval approve",   // planning.go planApprove reads the plan, then sends DecideApproval with Allow for its digest; the plan approval card does
	"plan start":        "work create",        // planstart.go openSession sends work create (plan), review, claim, and lane start; planning/session.ts sends the same calls
	"plan revision":     "plan show",          // planning.go planRevision prints the open revision of a plan, which plan show prints too; the plan page shows it, and the approval page shows the one an approval answers, from the plan's own read
	"policy show":       "policy list",        // questions.go policyShow finds one version in the rows policyList prints and prints its body; the list route returns every version with its settings, and Settings shows the newest and each earlier one in its History tab
	"repository show":   "repository list",    // repository.go repositoryShow prints the RepositoryView of one repository; the list route returns the same view for each, with its forge health and review agreement, and Repositories opens a row's record from it
}

// cliOnly are the commands FR-UI-7 leaves to the command line, each with the clause that does.
var cliOnly = map[string]string{
	"init":              "FR-UI-7: init sets up this machine, with its passphrase, worker, user profile, doctor, and sample",
	"service install":   "FR-UI-7: service install and service uninstall act on the machine's service manager",
	"service uninstall": "FR-UI-7: service install and service uninstall act on the machine's service manager",
	"telemetry query":   "FR-UI-7: members do not run telemetry query, which gives an orchestrator agent the worker's raw store with --json; Usage shows each lane's context, tokens, and cost from telemetry lanes",
	"top":               "FR-UI-7: top is the terminal's own view",
	"work write":        "FR-UI-7: work write stores one event under a claim's fence, which the worker protocol relies on",
}

// webGaps are the commands the web UI should have a row for and does not yet, each with the slice of U2 or
// of P4 that adds it. A command whose row is added leaves this list, and the test fails until it does. The
// first run of this test wrote it, and a command the audit did not name went by its area.
var webGaps = map[string]string{
	// The Spend section of project Settings, the work order and lane pages, and the installation's Spend section
	// read and publish through the same routes (ADR-0037); SP4 builds them.
	"spend":                 "SP4",
	"spend show":            "SP4",
	"spend set":             "SP4",
	"spend revert":          "SP4",
	"question answer-batch": "P8e", // questions.go questionAnswerBatch answers a batch in one request; the Needs you batch sends POST /v1/batches/{id}/answers
}

var gapSlice = regexp.MustCompile(`^(U2[a-k]|P[34][a-j]|SP[1-9]|P8e)[12]?$`)

// TestEveryCommandHasAWebRoute holds the CLI to the web listener: each command is named by a row of
// WebRoutes, or is in exactly one of equivalents, cliOnly, and webGaps. An entry whose command a row now
// names, a command that does not exist, or an equivalent whose row does not exist fails, so the lists only
// shrink as the UI grows.
func TestEveryCommandHasAWebRoute(t *testing.T) {
	rows := map[string]bool{}
	for _, route := range api.WebRoutes() {
		rows[route.Command] = true
	}
	commands := cli.Commands()
	exists := map[string]bool{}
	for _, command := range commands {
		exists[command] = true
		_, isEquivalent := equivalents[command]
		_, isCLIOnly := cliOnly[command]
		_, isGap := webGaps[command]
		listed := 0
		for _, in := range []bool{isEquivalent, isCLIOnly, isGap} {
			if in {
				listed++
			}
		}
		switch {
		case rows[command] && listed > 0:
			t.Errorf("a web route names %q, so remove it from the list it is on", command)
		case !rows[command] && listed == 0:
			t.Errorf("%q has no row in WebRoutes and is in none of equivalents, cliOnly, and webGaps: serve it and build it in the UI, or list it with the reason or the slice", command)
		case listed > 1:
			t.Errorf("%q is in more than one of equivalents, cliOnly, and webGaps: it is in one", command)
		}
	}
	for command, row := range equivalents {
		switch {
		case !exists[command]:
			t.Errorf("equivalents names %q, which is no command", command)
		case !rows[row]:
			t.Errorf("equivalents gives %q the row %q, which no web route names", command, row)
		}
	}
	for command, clause := range cliOnly {
		switch {
		case !exists[command]:
			t.Errorf("cliOnly names %q, which is no command", command)
		case clause == "":
			t.Errorf("cliOnly gives %q no clause", command)
		}
	}
	for command, slice := range webGaps {
		switch {
		case !exists[command]:
			t.Errorf("webGaps names %q, which is no command", command)
		case !gapSlice.MatchString(slice):
			t.Errorf("webGaps names %q as %q, which is no slice of U2 or P4", command, slice)
		}
	}
	if len(commands) < 100 {
		t.Fatalf("%d commands, so the registry read too few", len(commands))
	}
}
