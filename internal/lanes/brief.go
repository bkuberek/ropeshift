package lanes

import (
	"fmt"
	"strings"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/messages"
)

// Nudge is the one message a lane that ended a turn with no outcome receives. An idle
// lane with no outcome is an abnormality.
const Nudge = "You ended your turn with no outcome. Call the " + ToolLaneReport +
	" tool now, once, with the outcome of this lane."

// BriefSpec is what a first prompt is built from.
type BriefSpec struct {
	WorkOrder string
	Title     string
	Statement string
	Criteria  []string
	// Handoff is what the last generation left, for a generation that follows one.
	Handoff *Handoff
	// Pending is what a stopped generation was doing, because a stopped turn is not continued.
	Pending string
	// Facts are what no session of this lane took in, or took in and did not answer.
	Facts []ledger.MessageFact
}

// Brief is a lane's first prompt until context packs replace it: the contract, and
// how the lane ends.
func Brief(spec BriefSpec) string {
	var out strings.Builder
	out.WriteString("You are working in a ropeshift lane on work order " + spec.WorkOrder + ": " + spec.Title + ".\n")
	if statement := strings.TrimSpace(spec.Statement); statement != "" {
		out.WriteString("\n" + statement + "\n")
	}
	if len(spec.Criteria) > 0 {
		out.WriteString("\nAcceptance criteria:\n")
		for _, criterion := range spec.Criteria {
			out.WriteString("- " + criterion + "\n")
		}
	}
	if spec.Handoff != nil {
		writeHandoff(&out, *spec.Handoff)
	}
	if spec.Pending != "" {
		out.WriteString("\n" + pendingNotice + "\n\n" + spec.Pending + "\n")
	}
	writeFacts(&out, spec.Facts)
	out.WriteString("\nWhen the work is done, or cannot go on, call the " + ToolLaneReport +
		" tool once with its outcome, then end your turn.\n")
	return out.String()
}

// The two lists a first prompt can hold.
const (
	factsNotice      = "While no session could hear it, the ledger holds this for this lane:"
	unansweredNotice = "These messages were taken into an earlier turn, and each is possibly unanswered. " +
		"Check whether each still needs a reply, and use the " + ToolReply + " tool if it does:"
)

// writeFacts states what reaches a new generation as a ledger fact, and what it should check. A
// message is never retargeted to a new generation, so this is the one way any of it gets here. The
// text is a sender's, so it says what it is not.
func writeFacts(out *strings.Builder, facts []ledger.MessageFact) {
	var stated, unanswered []ledger.MessageFact
	for _, fact := range facts {
		if fact.Unanswered {
			unanswered = append(unanswered, fact)
		} else {
			stated = append(stated, fact)
		}
	}
	writeFactList(out, factsNotice, stated)
	writeFactList(out, unansweredNotice, unanswered)
	if len(facts) != 0 {
		out.WriteString("\n" + messages.NotConsent + "\n")
	}
}

func writeFactList(out *strings.Builder, notice string, facts []ledger.MessageFact) {
	if len(facts) == 0 {
		return
	}
	out.WriteString("\n" + notice + "\n")
	for _, fact := range facts {
		fmt.Fprintf(out, "\n- %s %s", fact.Kind, fact.ID)
		if fact.ReplyTo != "" {
			out.WriteString(", about " + fact.ReplyTo)
		}
		out.WriteString(": " + fact.Body + "\n")
	}
}

// writeHandoff tells a new generation what the last one left. It states a missing narrative
// and does not paper over it, so the lane reads the work before it goes on.
func writeHandoff(out *strings.Builder, doc Handoff) {
	fmt.Fprintf(out, "\nYou take over from generation %d, which handed off (%s).\n", doc.Generation, doc.Reason)
	if doc.Narrative == nil {
		writeMissing(out, doc.Deterministic)
	} else {
		writeList(out, "Done", doc.Narrative.Done)
		writeList(out, "In progress", doc.Narrative.InProgress)
		writeList(out, "Next steps", doc.Narrative.NextSteps)
		writeList(out, "Risks", doc.Narrative.Risks)
	}
	writeList(out, "Where the work stands", standing(doc.Deterministic))
}

func writeMissing(out *strings.Builder, facts HandoffFacts) {
	out.WriteString("\nIt left no narrative, so nothing says what it was doing or what comes next. Read the work before you go on: ")
	if facts.SnapshotRef != "" {
		out.WriteString("a snapshot of it is at " + facts.SnapshotRef + ".\n")
		return
	}
	fmt.Fprintf(out, "it is on branch %s at %s, with no snapshot.\n", facts.Branch, facts.Head)
}

func writeList(out *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	out.WriteString("\n" + title + ":\n")
	for _, item := range items {
		out.WriteString("- " + item + "\n")
	}
}

func standing(facts HandoffFacts) []string {
	var lines []string
	add := func(on bool, line string) {
		if on {
			lines = append(lines, line)
		}
	}
	add(facts.Branch != "", fmt.Sprintf("Branch %s at %s, with %d commits not pushed", facts.Branch, facts.Head, facts.UnpushedCommits))
	add(facts.SnapshotRef != "", "Snapshot: "+facts.SnapshotRef)
	add(len(facts.DirtyPaths) != 0, "Changed files: "+strings.Join(facts.DirtyPaths, ", "))
	add(len(facts.OpenQuestions) != 0, "Open questions: "+strings.Join(facts.OpenQuestions, ", "))
	add(len(facts.OpenAssumptions) != 0, "Open assumptions: "+strings.Join(facts.OpenAssumptions, ", "))
	var checks []string
	for _, check := range facts.Checks {
		checks = append(checks, check.Name+" "+check.Status)
	}
	add(len(checks) != 0, "Checks: "+strings.Join(checks, ", "))
	return lines
}

// HandoffInstruction asks an agent for the part only it knows, and names what to leave out,
// because the worker writes the rest.
func HandoffInstruction() string {
	return "Hand off now. This session is about to end, and a new generation takes this work order over from what " +
		"you write here (" + HandoffSchema + ").\n\n" +
		"Call the " + ToolLaneHandoff + " tool once, with only what you alone know. The worker records the branch, the " +
		"commits, the changed files, the open questions, and the checks itself.\n\n" +
		"- done: what you finished\n" +
		"- in_progress: what you were part way through\n" +
		"- next_steps: what the next generation should do first\n" +
		"- risks: what could go wrong, and what you are unsure of\n" +
		"- carries_next_step: true only if this holds everything the next step needs\n\n" +
		"Then end your turn, and start nothing new.\n"
}

// Restarted is what a resumed generation is told, because a resume alone starts no turn.
const Restarted = "Your session ended unexpectedly and was restarted. Continue this work order from where it stopped, " +
	"and call lane_report when it is done or cannot go on."

// RestartPrompt is that, with what the last generation left unread.
func RestartPrompt(facts ...ledger.MessageFact) string {
	var out strings.Builder
	out.WriteString(Restarted)
	if len(facts) != 0 {
		out.WriteString("\n")
		writeFacts(&out, facts)
	}
	return out.String()
}
