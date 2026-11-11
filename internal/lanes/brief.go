package lanes

import (
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Nudge is the one message a lane that ended a turn with no outcome receives. An idle
// lane with no outcome is an abnormality.
const Nudge = "You ended your turn with no outcome. Call the " + ToolLaneReport +
	" tool now, once, with the outcome of this lane."

// PackFile is where a pack sits in a worktree, in a directory git does not see.
const PackFile = ".ropeshift/context/pack.md"

// InventoryFile sits beside the pack of a lane that adopts a project: the inventory of each repository it
// works in and reads, which the worker took with no model before the lane started (ADR-0023).
const InventoryFile = ".ropeshift/context/inventory.json"

// ReviewDiffFile and ReviewFilesFile sit beside the pack in a review lane's worktree: what the reviewed
// lane changed, as a patch, and the paths it touches, one to a line.
const (
	ReviewDiffFile  = ".ropeshift/context/review.diff"
	ReviewFilesFile = ".ropeshift/context/review-files.txt"
)

// BriefSpec is what a first prompt is built from.
type BriefSpec struct {
	WorkOrder string
	Title     string
	Statement string
	Criteria  []string
	// Pack says this generation holds a context pack.
	Pack bool
	// Handoff is what the last generation left, for a generation that follows one.
	Handoff *Handoff
	// Pending is what a stopped generation was doing, because a stopped turn is not continued.
	Pending string
	// Facts are what no session of this lane took in, or took in and did not answer.
	Facts []ledger.MessageFact
	// Kind says what this work produces, which is what a planning or review lane is told to do.
	Kind workorders.Kind
	// Review is what a contract review lane reads.
	Review *ledger.DesiredReview
	// Attempt is the work a review lane judges.
	Attempt *ledger.DesiredAttempt
	// Rework is why the work came back for this attempt, when it did.
	Rework *ledger.DesiredRework
	// Continues is the in-flight branch this work finishes, when it finishes one (ADR-0023).
	Continues *Continuation
	// Reads are the other repositories of the project the lane reads, read-only (ADR-0022).
	Reads []ReadTree
}

// ReadTree is another repository of the project checked out for a lane to read, at the commit its claim
// recorded, with no branch. One the worker could not give the lane has no path, and Detail says why.
type ReadTree struct {
	Name   string
	Path   string
	Ref    string
	Commit string
	Detail string
}

// writeReads names where each other repository of the project stands for the lane to read, and that it is
// read-only: a lane changes only its own worktree.
func writeReads(out *strings.Builder, reads []ReadTree) {
	out.WriteString("\nThe other repositories of this project are checked out for you to read, read-only, each at the commit recorded " +
		"when this lane started and on no branch. Read them to work against the code as it is. Do not edit, commit, push, or change " +
		"branches in them: your work goes only in your own worktree, and they are removed when this lane ends.\n")
	for _, read := range reads {
		if read.Path == "" {
			fmt.Fprintf(out, "- %s: not available, because %s\n", read.Name, read.Detail)
			continue
		}
		fmt.Fprintf(out, "- %s: %s, at commit %s (%s)\n", read.Name, read.Path, read.Commit, read.Ref)
	}
}

// Continuation is an in-flight branch a lane finishes: its name, the tip the lane's own branch began at,
// and the commit where the branch left its base, which the work is measured from.
type Continuation struct {
	Branch string
	Tip    string
	Base   string
}

// writeContinuation tells a lane whose branch began on someone's in-flight branch what it holds, and that
// the branch itself stays as its author left it.
func writeContinuation(out *strings.Builder, c Continuation) {
	fmt.Fprintf(out, "\nThis work finishes branch %s, which someone started. Your branch began at its tip, commit %s, so it already holds "+
		"that work; read it with git log %s..HEAD and git diff %s before you change anything. The change is measured from commit %s, "+
		"where %s left its base, and review reads all of it. Commit on your own branch. Never push to %s, move it, or switch to it: "+
		"it stays as its author left it.\n", c.Branch, c.Tip, c.Base, c.Base, c.Base, c.Branch, c.Branch)
}

// planningNotice tells a planning lane what the ledger takes from it: files, at a commit, and only
// once a person accepts it. It shows every format the lane writes, because a session holds no document
// of them (FR-SESSION-5).
func planningNotice() string {
	var out strings.Builder
	out.WriteString("This is planning work. Its output is repository files, and nothing else you do reaches the ledger. " +
		"Each file is Markdown with TOML front matter between +++ lines, and its kind says what it is. A file's id is stable, " +
		"so keep it when you change the file. Never write a version number: the ledger numbers each committed change to a " +
		"document itself. A document is cited as kind:id, as in spec:checkout-api.\n\n" +
		"These are the formats, one example of each. They describe one project, so the files in them name each other as yours must.\n")
	for _, example := range planning.FormatExamples {
		out.WriteString("\n" + example.Intro + "\n\n```markdown\n" + example.Text + "```\n")
	}
	out.WriteString("\n" +
		"- Every document, milestone, and contract names its plan with plan, and the plan lists every document it holds in " +
		"documents. Write the plan file too: a file that names a plan nobody wrote is a problem, and intake records nothing from it. " +
		"A contract with no plan gets a plan of its own.\n" +
		"- When a plan has milestones, every contract of it names one with milestone; a contract in no milestone is a problem and " +
		"records nothing. A plan with no milestones may leave milestone out.\n" +
		"- A contract's milestone must be a milestone of its plan, and each contract in uses must be a contract of the same plan " +
		"that is marked shared with shared = true. A milestone's depends_on must name milestones of its plan. A reference that is not " +
		"a milestone of its plan, or a contract that is not marked shared, is a problem, and intake records nothing from that file.\n" +
		"- Dependencies never form a cycle: milestones that depend on each other in a loop, contracts that use each other, or work " +
		"orders whose depends_on lead back to themselves. Intake refuses every file of a plan that has a cycle and names the cycle, " +
		"so the plan records nothing until it is gone.\n" +
		"- Intake makes every work order of a milestone wait for every work order of the milestones it depends on, and every work " +
		"order of a contract wait for the work orders of each shared contract it uses, so shared contracts are built first. Use " +
		"depends_on between work orders only for a finer order.\n" +
		"- A contract's documents cite documents you commit with it. Intake pins each citation to the version in your commit, " +
		"and a citation of a document nobody wrote records nothing.\n" +
		"- When the plan has a requirements file, each criterion lists at least one requirement id from it. A criterion with no " +
		"requirement, or one the plan does not have, is a problem, and the contract's work stays out of ready until a new version fixes it.\n" +
		"- A contract needs at least one criterion and at least one work order, and each work order's id is unique. Use repo = \".\" " +
		"for this repository, and size small, medium, or large.\n" +
		"- Write a draft decision only for an answer the person gave you in conversation. For anything they have not answered, ask " +
		"with the " + ToolAsk + " tool.\n" +
		"- A TOML string needs its quotes, and a file that does not parse reaches nobody.\n\n" +
		"Commit the files on this branch, because intake reads a commit and never your working tree. Report the outcome after the commit. " +
		"The person then approves the plan in one step: approving it records your draft decisions as their decisions, passes the " +
		"contract review of the work your contracts cover, and accepts your work, and intake then reads that commit. You cannot approve " +
		"the plan yourself. A change you make after that is a new version of the plan, which the person approves too, so finish the " +
		"plan before you report.\n")
	return out.String()
}

// reviewNotice tells a review lane what a review is for. It finds, and it decides nothing.
const reviewNotice = "This is a contract review. Read the plan and each contract below as the lane that will do the work would, and " +
	"find everything that lane would have to stop and ask: what the contract leaves open, what two contracts disagree on, what a " +
	"criterion cannot show. Ask each one with the " + ToolAsk + " tool, as a person would read it. Set blocking to true when work cannot " +
	"start without the answer. Ask everything you find in this one pass, and answer none of it yourself: nothing reaches a person until " +
	"you report, so that they read it together. Do not change any file.\n"

func writeReview(out *strings.Builder, review ledger.DesiredReview) {
	out.WriteString("\n" + reviewNotice)
	fmt.Fprintf(out, "\nPlan: %s\n", review.Plan)
	for _, contract := range review.Contracts {
		fmt.Fprintf(out, "\nContract: %s\n", contract.Title)
		if statement := strings.TrimSpace(contract.Statement); statement != "" {
			out.WriteString(statement + "\n")
		}
		writeList(out, "Its criteria", contract.Criteria)
	}
}

// writeAttempt tells a review lane what it judges, that it may change nothing, and how it reports. The
// lane judges against the criteria the brief states, and against the requirements and documents the
// contract cites, which its pack holds. What the reviewed lane reported of itself is in the pack too, and
// is said to be information, because a lane's account of its own work is not evidence of it.
func writeAttempt(out *strings.Builder, attempt ledger.DesiredAttempt) {
	out.WriteString("\nThis is a review. You are judging work order " + attempt.WorkOrder)
	if title := strings.TrimSpace(attempt.Title); title != "" {
		out.WriteString(", " + title)
	}
	fmt.Fprintf(out, ", which a lane finished at commit %s on branch %s.", attempt.Commit, attempt.Branch)
	if attempt.BaseCommit != "" {
		fmt.Fprintf(out, " That lane began at commit %s (%s).", attempt.BaseCommit, attempt.BaseRef)
	}
	out.WriteString(" You did not write it. Your worktree is a checkout of that commit with no branch, and you may not edit a file " +
		"with a tool, commit, push, or move a branch: nothing you write there reaches the work, and the worktree is discarded when you finish.\n\n")
	if attempt.BaseCommit != "" {
		out.WriteString("What the lane changed is in " + ReviewDiffFile + ", and the files it touches are listed in " + ReviewFilesFile +
			". Read the diff, then the code around it, and run what the repository offers to check it. ")
	} else {
		out.WriteString("The lane recorded no base, so there is no diff to read: read the branch with git log and git show, then the code, " +
			"and run what the repository offers to check it. ")
	}
	out.WriteString("Judge each acceptance criterion above against what the change does and against the requirements and documents the " +
		"contract cites, which your context pack holds. The pack also holds the checks the lane reported for itself. They are information and " +
		"not evidence: they are what the lane says of its own work.\n\n" +
		"Report through the " + ToolLaneReport + " tool once, with a verdict for every acceptance criterion above in its verdicts list. " +
		"A verdict names the criterion's ID in criterion, and says pass when the change meets the criterion, fail when it does not, and " +
		"cannot_judge when what you can read and run does not let you tell. Give reasons for each verdict, and citations: the files and " +
		"lines you rest on, the requirement IDs, and the documents as kind:id. The reasons of a fail are what the next attempt is told, " +
		"so say what is wrong and what would meet the criterion. Leave no criterion out and judge none the contract does not have, " +
		"because a report that does is refused. Report completed when you judged every criterion, whatever you found, and blocked when " +
		"you cannot judge them at all.\n")
}

// writeRework tells a lane that takes work up again why it came back. A finding is a model's text, so each
// line of it is indented under its own item: it cannot start a section of the brief.
func writeRework(out *strings.Builder, rework ledger.DesiredRework) {
	if len(rework.Findings) == 0 {
		out.WriteString("\nWhy this came back. Your last attempt was sent back for another try. The reason on record:\n\n")
		out.WriteString(indented(strings.TrimSpace(rework.Reason), reworkIndent) + "\n")
		return
	}
	fmt.Fprintf(out, "\nWhat the review found. Review %s judged your last attempt against each criterion, and it failed these:\n\n", rework.Review)
	for _, finding := range rework.Findings {
		out.WriteString("- " + finding.Criterion)
		if text := strings.TrimSpace(finding.Text); text != "" {
			out.WriteString(": " + text)
		}
		out.WriteString("\n" + indented("Why it failed: "+strings.TrimSpace(finding.Reasons), reworkIndent) + "\n")
		if len(finding.Citations) != 0 {
			out.WriteString(indented("It rests on: "+strings.Join(finding.Citations, ", "), reworkIndent) + "\n")
		}
	}
	if reason := strings.TrimSpace(rework.Reason); reason != "" {
		out.WriteString("\nIt was sent back as: " + reason + ".\n")
	}
	out.WriteString("\nAddress each finding in this attempt.\n")
}

// reworkIndent sets a finding's lines in from the item that holds them.
const reworkIndent = "        "

// indented puts every line of text in from the margin, so none of it begins a line of the brief.
func indented(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = prefix + strings.TrimRight(line, " \t\r")
	}
	return strings.Join(lines, "\n")
}

// Brief is a lane's first prompt: the contract, where its context pack is, and how the lane ends.
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
	if spec.Continues != nil {
		writeContinuation(&out, *spec.Continues)
	}
	if len(spec.Reads) != 0 {
		writeReads(&out, spec.Reads)
	}
	if spec.Rework != nil {
		writeRework(&out, *spec.Rework)
	}
	if spec.Kind == workorders.KindPlan {
		out.WriteString("\n" + planningNotice())
	}
	if spec.Review != nil {
		writeReview(&out, *spec.Review)
	}
	if spec.Attempt != nil {
		writeAttempt(&out, *spec.Attempt)
	}
	if spec.Pack {
		out.WriteString("\nYour system prompt holds this lane's context pack, which is also at " + PackFile + ". It is where you start " +
			"and not a limit: the " + ToolContextGet + ", " + ToolDecisionSearch + ", " + ToolRequirementGet + ", and " + ToolQuestionSearch +
			" tools read the rest of what this workspace holds.\n")
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

// MidTurnInstruction is the same instruction for a turn that is running, which a session reads
// between tool calls. Its first line is a message's, because that is the one line a hook reads.
func MidTurnInstruction(id string) string {
	return messages.Render(messages.Message{
		ID: id, Kind: messages.KindInstruction, From: messages.Sender{Kind: messages.SenderWorker}, Body: HandoffInstruction(),
	}, 0, messages.DefaultWindow)
}

// Restarted is what a resumed generation is told, because a resume alone starts no turn.
const Restarted = "Your session ended unexpectedly and was restarted. Continue this work order from where it stopped, " +
	"and call lane_report when it is done or cannot go on."

// AfterCompaction is what a session is told after a compaction ran at rest, because a compaction
// alone starts no turn, and its next call is the only evidence that it worked.
const AfterCompaction = "Your context was compacted. Go on with this work order from where you stopped, and call " +
	ToolLaneReport + " when it is done or cannot go on."

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
