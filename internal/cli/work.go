package cli

import (
	"cmp"
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

// repeated collects a flag given more than once.
type repeated []string

func (r *repeated) String() string { return strings.Join(*r, ",") }

func (r *repeated) Set(value string) error {
	*r = append(*r, value)
	return nil
}

// newID generates a client ID, so a retry of the same command line with --id is
// idempotent.
func newID() (string, error) {
	var entropy [ledger.ULIDEntropy]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return ledger.EncodeULID(time.Now().UnixMilli(), entropy), nil
}

func (c *invocation) workCreate(args []string) int {
	set := c.flags()
	var criteria, dependsOn, requirements repeated
	var request api.CreateWorkOrder
	set.StringVar(&request.ID, "id", "", "a client-generated ID")
	set.StringVar(&request.Title, "title", "", "the title")
	set.StringVar(&request.Statement, "statement", "", "what this work must deliver")
	set.StringVar(&request.Size, "size", "", "small, medium, or large")
	set.StringVar(&request.Agent, "agent", "", "the roster agent the work's lane runs as, in place of the default mapping's")
	set.StringVar((*string)(&request.Kind), "kind", "", "change, the default, or plan")
	set.StringVar(&request.RepositoryID, "repository", "", "the repository its lane runs in, as source list names it")
	set.Int64Var(&request.Priority, "priority", 0, "the priority")
	set.Var(&criteria, "criterion", "an acceptance criterion, repeatable")
	set.Var(&dependsOn, "depends-on", "a work order this one needs, repeatable")
	set.Var(&requirements, "requirement", "a requirement key, repeatable")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	if request.Title == "" {
		return c.usageError("--title is required")
	}
	if _, err := planning.CheckSize(request.Size); err != nil {
		return c.usageError("%v", err)
	}
	if request.ID == "" {
		id, err := newID()
		if err != nil {
			return c.failure(err)
		}
		request.ID = id
	}
	for i, text := range criteria {
		request.Criteria = append(request.Criteria, planning.Criterion{ID: fmt.Sprintf("c%d", i+1), Text: text})
	}
	request.DependsOn, request.Requirements = dependsOn, requirements
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	created, err := client.CreateWorkOrder(c.ctx, request)
	return c.finish(err, created, func() string { return orderLine(created) })
}

func orderLine(w workorders.WorkOrder) string {
	return fmt.Sprintf("%s\t%s\t%d\t%s\n", w.ID, w.State, w.Priority, w.Title)
}

func (c *invocation) workList(args []string) int {
	set := c.flags()
	state := set.String("state", "", "only this state")
	worker := set.String("worker", "", "only ready work this worker can run")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	var rows []workorders.WorkOrder
	var err error
	if *worker != "" {
		rows, err = client.Offered(c.ctx, *worker)
	} else {
		rows, err = client.WorkOrders(c.ctx, workorders.State(*state))
	}
	if rows == nil {
		rows = []workorders.WorkOrder{}
	}
	return c.finish(err, api.WorkOrderList{WorkOrders: rows}, func() string {
		var out strings.Builder
		for _, row := range rows {
			out.WriteString(orderLine(row))
		}
		return out.String()
	})
}

// oneID reads the one positional argument a command takes.
func (c *invocation) oneID(positional []string) (string, bool) {
	if len(positional) != 1 {
		c.usageError("takes one work order ID")
		return "", false
	}
	return positional[0], true
}

func (c *invocation) workShow(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	row, err := client.WorkOrder(c.ctx, id)
	if err != nil {
		return c.finish(err, row, nil)
	}
	view, err := client.Acceptance(c.ctx, id)
	if err != nil {
		return c.finish(err, row, nil)
	}
	estimate, err := client.WorkEstimate(c.ctx, id)
	if err != nil {
		return c.finish(err, row, nil)
	}
	reason, said, err := endedWith(c.ctx, client, row)
	if err != nil {
		return c.finish(err, row, nil)
	}
	detail := api.WorkDetail{
		WorkOrder: row, JudgedBy: view.Reviews, Verdicts: view.Verdicts, Notes: view.Notes, Why: view.Why, PullRequest: view.PullRequest, Integrations: view.Integrations,
		Estimate: estimate.Estimate, Baseline: estimate.Baseline, Reason: reason, Detail: said,
	}
	return c.finish(nil, detail, func() string { return workDetailText(detail) })
}

// endedWith reads what the outcome of the lane that ended a work order said of why, from the completed view,
// which is where the ledger keeps it: the event stream carries no record's content. Work that is still going
// has none.
func endedWith(ctx context.Context, client *api.ProjectClient, row workorders.WorkOrder) (reason, detail string, err error) {
	if !workorders.Terminal(row.State) && !row.State.Completed() {
		return "", "", nil
	}
	var view api.Completed
	if err := client.ReadView(ctx, api.ViewCompleted, "", &view); err != nil {
		return "", "", err
	}
	for _, item := range view.Items {
		if item.WorkOrder.ID == row.ID {
			return item.Reason, item.Detail, nil
		}
	}
	return "", "", nil
}

// outcomeLine says why the lane that ended a work order did, in what the lane and the worker wrote of it, made
// safe for a terminal and put on one line.
func outcomeLine(reason, detail string) string {
	line := "outcome\t" + oneLine(api.Printable(reason))
	if detail != "" {
		line += "\t" + oneLine(api.Printable(detail))
	}
	return line + "\n"
}

// workDetailText prints a work order, then its reviews and the verdict each gave for each criterion.
func workDetailText(detail api.WorkDetail) string {
	var out strings.Builder
	out.WriteString(workOrderText(detail.WorkOrder))
	if detail.Reason != "" {
		out.WriteString(outcomeLine(detail.Reason, detail.Detail))
	}
	if pr := detail.PullRequest; pr != nil {
		fmt.Fprintf(&out, "pull request\t#%d\t%s\thead %s\t%s\n", pr.Number, pr.State, shortCommit(pr.HeadCommit), pr.URL)
	}
	out.WriteString(reviewLines(detail.JudgedBy))
	out.WriteString(verdictLines(detail.Verdicts))
	out.WriteString(noteLines(detail.Notes))
	for _, record := range detail.Integrations {
		approver := string(record.ApprovedByKind)
		if record.ApprovedByID != "" {
			approver += " " + record.ApprovedByID
		}
		merged := "not merged"
		if record.Merged() {
			merged = "merged as " + dashIfEmpty(shortCommit(record.MergedCommit))
		}
		plan := ""
		if record.Plan != "" {
			plan = fmt.Sprintf("\tplan %s version %d", record.Plan, record.PlanVersion)
		}
		fmt.Fprintf(&out, "approved\t%s\tby %s\tat %s\t%s%s\n", shortCommit(record.ApprovedCommit), approver, record.MergeAuthority, merged, plan)
	}
	out.WriteString(estimateLines(detail.Estimate, detail.Baseline))
	switch {
	case detail.Why == "":
	case detail.State == workorders.Integrating:
		fmt.Fprintf(&out, "waiting\tto merge: %s\n", oneLine(detail.Why))
	default:
		fmt.Fprintf(&out, "waiting\tfor a member: %s\n", oneLine(detail.Why))
	}
	return out.String()
}

// estimateLines prints a work order's current estimate and, under it, the estimate its baseline holds, each as
// estimateText says it. Work with neither prints nothing.
func estimateLines(estimate *delivery.Estimate, baseline *api.WorkBaseline) string {
	out := estimateText(estimate)
	if baseline != nil {
		out += strings.Replace(estimateText(&baseline.Estimate), "estimate\t", "baseline\t", 1)
	}
	return out
}

// shortCommit is enough of a commit to read and find it again.
func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// oneLine puts text on one line, because a line of output is one record.
func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

func reviewLines(reviews []workorders.ReviewLine) string {
	var out strings.Builder
	for _, review := range reviews {
		fmt.Fprintf(&out, "review\t%s\t%s\tattempt %d\n", review.ID, review.State, review.Attempt)
	}
	return out.String()
}

// verdictLines prints what each review verdict says, one to a line: the criterion, the verdict, the reasons
// on one line, and the citations.
func verdictLines(verdicts []workorders.Verdict) string {
	var out strings.Builder
	for _, v := range verdicts {
		fmt.Fprintf(&out, "verdict\t%s\t%s\t%s\t%s\n", v.Criterion, v.Verdict, oneLine(v.Reasons), dashIfEmpty(strings.Join(v.Citations, ", ")))
	}
	return out.String()
}

// noteLines prints the security notes a review left, after its verdicts, under a line that counts them: each
// note on one line with its point, its reasons, and its citations. A note is a model's text, so it is made safe
// for a terminal. A review that left none prints nothing.
func noteLines(notes []workorders.Note) string {
	if len(notes) == 0 {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "security notes\t%d\n", len(notes))
	for _, n := range notes {
		fmt.Fprintf(&out, "note\t%s\t%s\t%s\n", n.Point, oneLine(api.Printable(n.Reasons)), dashIfEmpty(oneLine(api.Printable(strings.Join(n.Citations, ", ")))))
	}
	return out.String()
}

// workOrderText prints a work order, and says what its start waits for when something other than a
// lack of ready work holds it.
func workOrderText(row workorders.WorkOrder) string {
	var out strings.Builder
	out.WriteString(orderLine(row))
	fmt.Fprintf(&out, "fence\t%d\nworker\t%s\ncontract\t%s@%d\n", row.Fence, row.LeaseWorker, row.ContractID, row.ContractVersion)
	if row.Base != "" {
		fmt.Fprintf(&out, "base\t%s\n", plainLine(row.Base))
	}
	if hold := row.StartHeld; hold != nil {
		fmt.Fprintf(&out, "waiting\t%s\t%s\t%s\n", hold.Reason, dashIfEmpty(hold.Dependency), dashIfEmpty(hold.Detail))
	}
	return out.String()
}

// workReview exits with its own code when the definition of ready refuses, because
// a script must not read a recorded review as a move to `ready`.
func (c *invocation) workReview(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	result, err := client.Review(c.ctx, id)
	code := c.finish(err, result, func() string {
		var out strings.Builder
		fmt.Fprintf(&out, "%s\t%s\n", id, result.State)
		for _, failure := range result.Failures {
			fmt.Fprintf(&out, "not ready: %s: %s\n", failure.Check, failure.Detail)
		}
		return out.String()
	})
	if code == ExitOK && len(result.Failures) > 0 {
		fmt.Fprintf(c.stderr, "ropeshift %s: the review is recorded, and %s is not ready\n", c.name, id)
		return ExitNotReady
	}
	return code
}

func (c *invocation) workClaim(args []string) int {
	set := c.flags()
	var request api.ClaimRequest
	var ttl time.Duration
	set.StringVar(&request.WorkerID, "worker", "", "the worker that will run this")
	set.StringVar(&request.ClaimID, "claim-id", "", "a client-generated ID, so a retry returns the grant")
	set.DurationVar(&ttl, "ttl", 0, "the lease, or the policy's when absent")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	if request.WorkerID == "" {
		return c.usageError("--worker is required")
	}
	if request.ClaimID == "" {
		generated, err := newID()
		if err != nil {
			return c.failure(err)
		}
		request.ClaimID = generated
	}
	request.TTLMs = ttl.Milliseconds()
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	grant, err := client.Claim(c.ctx, id, request)
	return c.finish(err, grant, func() string {
		return fmt.Sprintf("%s\tclaim %s\tfence %d\n", id, grant.ClaimID, grant.Fence)
	})
}

func (c *invocation) workRelease(args []string) int {
	set := c.flags()
	var claim string
	var request api.ReleaseRequest
	set.StringVar(&claim, "claim-id", "", "the claim to give back")
	set.Int64Var(&request.Fence, "fence", 0, "the claim's fence")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	if claim == "" || request.Fence == 0 {
		return c.usageError("--claim-id and --fence are required")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	err := client.Release(c.ctx, id, claim, request)
	return c.finish(err, struct {
		Released string `json:"released"`
	}{id}, func() string { return id + "\treleased\n" })
}

// changeColumns says what a branch changed, as columns after its digest, and nothing where nothing was
// measured.
func changeColumns(change *ledger.ChangeSize) string {
	if change == nil {
		return ""
	}
	files := "files"
	if change.Files == 1 {
		files = "file"
	}
	return fmt.Sprintf("\t%d %s\t+%d\t-%d", change.Files, files, change.Added, change.Removed)
}

// workAcceptance prints what a work order's evidence leaves to do, then what stays linked to it.
func (c *invocation) workAcceptance(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	view, err := client.Acceptance(c.ctx, id)
	return c.finish(err, view, func() string {
		var out strings.Builder
		fmt.Fprintf(&out, "%s\t%s\tneeds %s\n", id, view.State, view.Assessment.Needs)
		for _, row := range view.Assessment.Criteria {
			fmt.Fprintf(&out, "criterion\t%s\t%s\n", row.Criterion, row.Standing)
		}
		for _, name := range view.Assessment.Failed {
			fmt.Fprintf(&out, "failed\t%s\n", name)
		}
		out.WriteString(reviewLines(view.Reviews))
		out.WriteString(verdictLines(view.Verdicts))
		out.WriteString(noteLines(view.Notes))
		out.WriteString(pullRequestLine(view.PullRequest))
		for _, artifact := range view.Artifacts {
			fmt.Fprintf(&out, "artifact\t%s\t%s\t%s%s\n", artifact.Kind, artifact.URI, artifact.Digest, changeColumns(artifact.Change))
		}
		out.WriteString(checkLines(view.Checks, view.PullRequest))
		return out.String()
	})
}

// pullRequestLine names the pull request a work order lands through, as its worker last read it from the forge:
// its number, its state, the branch it merges into its base, its head commit, the commit a merge made once there
// is one, and its address. What the forge said is made safe for a terminal. Work with no pull request prints
// nothing.
func pullRequestLine(pr *ledger.PullRequest) string {
	if pr == nil {
		return ""
	}
	merged := ""
	if pr.MergeCommit != "" {
		merged = "\tmerged as " + shortCommit(pr.MergeCommit)
	}
	return fmt.Sprintf("pull request\t#%d\t%s\t%s into %s\thead %s%s\t%s\n", pr.Number, pr.State, api.Printable(pr.Branch),
		api.Printable(pr.Base), shortCommit(pr.HeadCommit), merged, api.Printable(pr.URL))
}

// checkLines lists the checks on a work order's commit as the web tab does: the results on record, each with who
// stands behind it and the criterion it speaks for, then the checks the forge runs on the pull request, marked
// required where the base branch requires them, which are what the merge gate reads. A forge check on record that
// the pull request lists too shows once, as the pull request was last read.
func checkLines(recorded []workorders.Evidence, pr *ledger.PullRequest) string {
	var out strings.Builder
	listed := map[string]bool{}
	if pr != nil {
		for _, check := range pr.Checks {
			listed[check.Name] = true
		}
	}
	for _, check := range recorded {
		if check.Source == workorders.SourceForge && listed[check.Name] {
			continue
		}
		fmt.Fprintf(&out, "check\t%s\t%s\t%s\t%s\n", check.Name, check.Status, check.Source, check.Criterion)
	}
	if pr == nil {
		return out.String()
	}
	for _, check := range pr.Checks {
		from := fmt.Sprintf("pull request #%d", pr.Number)
		if check.Required {
			from += ", required"
		}
		fmt.Fprintf(&out, "check\t%s\t%s\t%s\n", api.Printable(check.Name), check.State, from)
	}
	return out.String()
}

func (c *invocation) workAccept(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	result, err := client.Accept(c.ctx, id)
	return c.finish(err, result, func() string { return fmt.Sprintf("%s\t%s\n", id, result.State) })
}

func (c *invocation) workRework(args []string) int {
	set := c.flags()
	reason := set.String("reason", "", "why the work goes back")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	if *reason == "" {
		return c.usageError("--reason is required")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	result, err := client.Rework(c.ctx, id, api.ReworkRequest{Reason: *reason})
	return c.finish(err, result, func() string { return fmt.Sprintf("%s\t%s\n", id, result.State) })
}

// workDecisionRework makes the rework of one finished work order for the decisions its plan's revision lists, which
// a reversible decision does not make by itself.
func (c *invocation) workDecisionRework(args []string) int {
	set := c.flags()
	var decisions repeated
	set.Var(&decisions, "decision", "a decision the plan's revision lists for the work, repeatable")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	if len(decisions) == 0 {
		return c.usageError("--decision is required")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	result, err := client.DecisionRework(c.ctx, id, api.DecisionRework{Decisions: decisions})
	return c.finish(err, result, func() string { return fmt.Sprintf("%s\t%s\n", id, result.Rework) })
}

func (c *invocation) workReassign(args []string) int {
	set := c.flags()
	var request api.ReassignRequest
	set.StringVar(&request.Reason, "reason", "", "why the holder's work no longer counts")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	if request.Reason == "" {
		return c.usageError("--reason is required")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	err := client.Reassign(c.ctx, id, request)
	return c.finish(err, struct {
		Reassigned string `json:"reassigned"`
	}{id}, func() string { return id + "\treassigned\n" })
}

// workWrite stores one event under a claim's fence, which is how anything that holds a claim keeps a
// note the ledger will refuse once that claim is stale. The client generates the ID, so a retry stores
// one event.
func (c *invocation) workWrite(args []string) int {
	set := c.flags()
	var request api.WriteRequest
	var payload string
	set.StringVar(&request.ClaimID, "claim-id", "", "the claim this write comes under")
	set.Int64Var(&request.Fence, "fence", 0, "the claim's fence")
	set.StringVar(&request.WriteID, "write-id", "", "a client-generated ID, so a retry stores one event")
	set.StringVar(&request.Lane, "lane", "", "the lane writing, if one is")
	set.Int64Var(&request.Generation, "generation", 0, "that lane's generation")
	set.StringVar(&payload, "payload", "", "what to store")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	if request.ClaimID == "" || request.Fence == 0 {
		return c.usageError("--claim-id and --fence are required")
	}
	if request.WriteID == "" {
		generated, err := newID()
		if err != nil {
			return c.failure(err)
		}
		request.WriteID = generated
	}
	request.Payload = []byte(payload)
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	written, err := client.Write(c.ctx, id, request)
	return c.finish(err, written, func() string { return fmt.Sprintf("%s\t%s\trevision %d\n", id, written.ID, written.Rev) })
}

// workCancel ends work for good. It answers when the ledger holds that, and a lane with a process
// stops when its worker hears, so `lane show` follows the lane from there.
func (c *invocation) workCancel(args []string) int {
	set := c.flags()
	var request api.CancelWork
	set.StringVar(&request.Reason, "reason", "", "why this work is over")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	if request.Reason == "" {
		return c.usageError("--reason is required")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	result, err := client.CancelWork(c.ctx, id, request)
	return c.finish(err, result, func() string { return fmt.Sprintf("%s\t%s\n", id, result.State) })
}

// laneStart asks the worker holding a claim to run a lane (FR-WO-9). It returns as soon
// as the ledger holds the request, and `work show` follows the lane from there.
func (c *invocation) laneStart(args []string) int {
	set := c.flags()
	var request api.StartLane
	set.StringVar(&request.LaneID, "lane", "", "a client-generated ID, so a retry names the same lane")
	layer := set.String("profile", "", "a TOML file, this lane's own profile layer")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	if *layer != "" {
		raw, err := c.readLocal(*layer)
		if err != nil {
			return c.usageError("%v", err)
		}
		request.Overrides = string(raw)
	}
	if request.LaneID == "" {
		generated, err := newID()
		if err != nil {
			return c.failure(err)
		}
		request.LaneID = generated
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	dispatched, err := client.StartLane(c.ctx, id, request)
	return c.finish(err, dispatched, func() string {
		return fmt.Sprintf("%s\tlane %s\tfence %d\n", id, dispatched.LaneID, dispatched.Fence)
	})
}

// laneLine states a lane with its freshness, because a value read as `last_reported` and
// shown as `observed` is the mistake FR-UI-5 exists to stop.
func laneLine(lane ledger.LaneView) string {
	conditions := strings.Join(lane.Conditions, ",")
	if conditions == "" {
		conditions = "-"
	}
	return fmt.Sprintf("%s\t%s\tliveness %s\tmodel %s\tledger %s\t%s\t%s\n", lane.ID, lane.ActualState,
		lane.Liveness, lane.ModelReach, lane.LedgerReach, conditions, lane.Freshness)
}

func (c *invocation) laneList(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	lanes, err := client.Lanes(c.ctx)
	if lanes == nil {
		lanes = []ledger.LaneView{}
	}
	return c.finish(err, api.LaneList{Lanes: lanes}, func() string {
		var out strings.Builder
		for _, lane := range lanes {
			out.WriteString(laneLine(lane))
		}
		return out.String()
	})
}

func (c *invocation) laneShow(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	lane, err := client.Lane(c.ctx, id)
	return c.finish(err, lane, func() string { return laneDetail(lane) })
}

// laneDetail is a lane's line, the agent CLI version its worker is checking when a check holds it, what each
// condition that has a sentence means, and where its branch began: the ref and the commit the claim named,
// which review diffs against and integration targets. A lane that began where its checkout was names none.
func laneDetail(lane ledger.LaneView) string {
	text := laneLine(lane)
	if lane.Checking != nil {
		text += fmt.Sprintf("status\t%s\n", oneLine(lane.Checking.Words))
	}
	for _, condition := range lane.Conditions {
		if note := lanes.Condition(condition).Note(); note != "" {
			text += fmt.Sprintf("%s\t%s\n", condition, note)
		}
	}
	if lane.BaseCommit != "" {
		text += fmt.Sprintf("base\t%s\t%s\n", dashIfEmpty(lane.BaseRef), lane.BaseCommit)
	}
	if lane.ContinuesCommit != "" {
		text += fmt.Sprintf("continues\t%s\t%s\n", lane.ContinuesRef, lane.ContinuesCommit)
	}
	return text
}

func (c *invocation) sourceList(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	list, err := client.Sources(c.ctx)
	return c.finish(err, list, func() string {
		var out strings.Builder
		for _, source := range list.Sources {
			fmt.Fprintf(&out, "%s\t%s\t%s\n", source.ID, source.Health, source.Detail)
			for _, problem := range source.Problems {
				fmt.Fprintf(&out, "\t%s\t%s\n", problemPlace(problem), problem.Detail)
			}
		}
		return out.String()
	})
}

// problemPlace names the file a problem is in, and for a file a plan's accepted commit held, the
// version intake read and the plan.
func problemPlace(problem planning.SourceProblem) string {
	if problem.WorkOrder == "" {
		return problem.Path
	}
	place := problem.Path
	if problem.Source != "" {
		place = problem.Source
	}
	return place + ", accepted in " + problem.WorkOrder
}

func (c *invocation) workerList(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	client := c.client()
	defer client.Close()
	list, err := client.WorkerList(c.ctx)
	if list.Workers == nil {
		list.Workers = []ledger.Worker{}
	}
	return c.finish(err, list, func() string {
		var out strings.Builder
		for _, worker := range list.Workers {
			fmt.Fprintf(&out, "%s\t%s\tepoch %d\tprotocol %d\t%s", api.Printable(worker.ID), api.Printable(worker.Name), worker.Epoch, worker.Protocol, api.ResourceWords(worker.Report))
			if worker.Report != nil {
				fmt.Fprintf(&out, "  measured %s ago", api.AgeWords(list.AsOf-worker.ReportedAt))
			}
			out.WriteString("\n")
		}
		return out.String()
	})
}

// workerToken makes a join token and prints it. On a group server it prints the join string the token is inside, which
// is what a worker is handed: one line holding the server's URL, the fingerprint of its authority, and the token. This
// is the one time anyone sees it, because the ledger keeps only the token's digest. The token lives a day unless
// --expires-in names another lifetime, up to thirty days. When it ends and which token it is go to the error stream, so
// what is piped on is the string alone, and --json carries all of it. The record of the token names the member who made
// it.
func (c *invocation) workerToken(args []string) int {
	set := c.flags()
	lifetime := set.Duration("expires-in", 0, "how long the token lives, such as 48h, up to 720h (30 days); a day by default")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	client := c.client()
	defer client.Close()
	made, err := client.NewJoinTokenFor(c.ctx, *lifetime)
	if err == nil && !c.asJSON && made.ExpiresAt != 0 {
		fmt.Fprintf(c.stderr, "ropeshift %s: the token expires at %s; revoke it before then with '%s worker token-revoke %s' if no worker used it\n",
			c.name, time.UnixMilli(made.ExpiresAt).UTC().Format(time.RFC3339), program, made.ID)
	}
	return c.finish(err, made, func() string { return cmp.Or(made.JoinString, made.Token) + "\n" })
}

// workerTokenRevoke ends a join token no worker used, by the ID `worker token` says, so it is refused from then on.
// Revoking a token again is no error, and a token a worker used is refused, because that worker holds a credential.
func (c *invocation) workerTokenRevoke(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client := c.client()
	defer client.Close()
	err := client.RevokeJoinToken(c.ctx, id)
	return c.finish(err, api.JoinTokenRevoked{ID: id}, func() string { return "revoked join token " + api.Printable(id) + "\n" })
}

func (c *invocation) laneReset(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	reset, err := client.ResetLane(c.ctx, id)
	return c.finish(err, reset, func() string { return "reset the lane of " + reset.WorkOrder + "\n" })
}

// laneRespawn asks for a running lane's next generation.
func (c *invocation) laneRespawn(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	asked, err := client.RespawnLane(c.ctx, id)
	return c.finish(err, asked, func() string { return "asked for a new generation of the lane of " + asked.WorkOrder + "\n" })
}

// laneResume gives a work order's parked lane a member's word.
func (c *invocation) laneResume(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	resumed, err := client.ResumeLane(c.ctx, id)
	return c.finish(err, resumed, func() string {
		if len(resumed.Fired) == 0 {
			return "the lane of " + resumed.WorkOrder + " has your word, and it woke nothing yet\n"
		}
		return "woke the lane of " + resumed.WorkOrder + "\n"
	})
}

// laneArchive reads which work order a lane belongs to, because a request goes through it.
func (c *invocation) laneArchive(args []string) int {
	set := c.flags()
	var force bool
	set.BoolVar(&force, "force", false, "remove it even if no remote holds its work")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	lane, err := client.Lane(c.ctx, id)
	if err != nil {
		return c.failure(err)
	}
	in := api.ArchiveLane{LaneID: id, Force: force}
	err = client.ArchiveLane(c.ctx, lane.WorkOrder, in)
	return c.finish(err, in, func() string { return fmt.Sprintf("lane %s: the worker removes its worktree on its next pass\n", id) })
}

// health says the control plane read its ledger, which an answer on the socket alone would not.
func (c *invocation) health(args []string) int {
	client, ok, code := c.noArgumentsAtInstallation(args)
	if !ok {
		return code
	}
	defer client.Close()
	health, err := client.Health(c.ctx)
	return c.finish(err, health, func() string { return fmt.Sprintf("%s\trevision %d\n", health.Status, health.Revision) })
}

func fleetLine(fleet api.Fleet) string {
	return fmt.Sprintf("fleet %s, work in progress %d, per repository %d, per worker %d\n",
		fleet.Mode, fleet.WIPLimit, fleet.RepositoryLimit, fleet.WorkerLimit)
}

// noArguments reads a command on a project that takes no argument, and returns that project's client.
func (c *invocation) noArguments(args []string) (*api.ProjectClient, bool, int) {
	if !c.parseNothing(args) {
		return nil, false, ExitUsage
	}
	client, exit := c.projectClient()
	return client, client != nil, exit
}

// noArgumentsAtInstallation reads a command on the installation that takes no argument.
func (c *invocation) noArgumentsAtInstallation(args []string) (*api.Client, bool, int) {
	if !c.parseNothing(args) {
		return nil, false, ExitUsage
	}
	return c.client(), true, ExitOK
}

// parseNothing reads a command line that holds only flags, and says so when it holds more.
func (c *invocation) parseNothing(args []string) bool {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return false
	}
	if len(positional) != 0 {
		c.usageError("unexpected argument %q", positional[0])
		return false
	}
	return true
}

func (c *invocation) fleetShow(args []string) int {
	client, ok, code := c.noArguments(args)
	if !ok {
		return code
	}
	defer client.Close()
	fleet, err := client.Fleet(c.ctx)
	return c.finish(err, fleet, func() string { return fleetLine(fleet) })
}

// setsFleet is the command of one mode.
func setsFleet(mode ledger.FleetMode) func(*invocation, []string) int {
	return func(c *invocation, args []string) int {
		client, ok, code := c.noArguments(args)
		if !ok {
			return code
		}
		defer client.Close()
		fleet, err := client.SetFleetMode(c.ctx, api.SetFleetMode{Mode: mode})
		return c.finish(err, fleet, func() string { return fleetLine(fleet) })
	}
}

// fleetCaps starts from what is set, so a flag left out keeps its value.
func (c *invocation) fleetCaps(args []string) int {
	set := c.flags()
	wip := set.Int64("wip", -1, "work in progress, 0 for no limit")
	repository := set.Int64("per-repository", -1, "lanes per repository, 0 for no limit")
	worker := set.Int64("per-worker", -1, "lanes per worker, 0 for no limit")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	current, err := client.Fleet(c.ctx)
	if err != nil {
		return c.failure(err)
	}
	caps := current.Caps
	for _, flag := range []struct {
		value  int64
		target *int64
	}{{*wip, &caps.WIPLimit}, {*repository, &caps.RepositoryLimit}, {*worker, &caps.WorkerLimit}} {
		if flag.value >= 0 {
			*flag.target = flag.value
		}
	}
	fleet, err := client.SetFleetCaps(c.ctx, caps)
	return c.finish(err, fleet, func() string { return fleetLine(fleet) })
}

// doctorLine states one driver the way a person reads it: usable or not, and why. The words of a driver
// that is not usable come from ledger.DriverManifest.Result, which the web UI shows too.
func doctorLine(driver ledger.DriverManifest) string {
	result := driver.Result()
	reason := result.Reason
	if result.Fix != "" {
		reason += ". " + result.Fix
	}
	switch {
	case result.Word == ledger.WordSignedOut:
		return fmt.Sprintf("%s\t%s\t%s\t%s\t%s\n", driver.Driver, result.Word, reason, driver.Version, qualification(driver))
	case result.Word != "":
		return fmt.Sprintf("%s\t%s\t%s\n", driver.Driver, result.Word, reason)
	}
	kind := driver.SignIn.Kind
	if kind == "" {
		kind = "-"
	}
	return fmt.Sprintf("%s\t%s\t%s\tsign-in %s\t%s\n", driver.Driver, driver.Version, qualification(driver), kind, driver.SignIn.Status)
}

func qualification(driver ledger.DriverManifest) string {
	if driver.Qualified {
		return "qualified"
	}
	return "unqualified"
}

// doctorReport is what `worker doctor --json` prints: the worker's manifest, which names each agent CLI, and the
// git and worktree sections, which a build with no check leaves out. InitDaemon says the environment holds the mark
// of the daemon `ropeshift init` starts.
type doctorReport struct {
	ledger.Manifest
	InitDaemon bool                  `json:"init_daemon,omitempty"`
	Git        *lanes.GitReport      `json:"git,omitempty"`
	Worktree   *lanes.WorktreeReport `json:"worktree,omitempty"`
}

// initDaemonLine says the environment holds the mark of the daemon `ropeshift init` starts, and what that does to a
// daemon started from it.
const initDaemonLine = "init daemon\tmarked\tROPESHIFT_INIT_DAEMON is set in this environment, so a daemon started from it is the one ropeshift init starts: " +
	"it qualifies no agent CLI and takes no work. Unset it before you start the daemon.\n"

// worktreeSection asks the machine where lane worktrees go. A build with no check has no section.
func (c *invocation) worktreeSection() (*lanes.WorktreeReport, error) {
	if c.worktree == nil {
		return nil, nil
	}
	report, err := c.worktree(c.ctx)
	if err != nil {
		return nil, fmt.Errorf("the worktree root: %w", err)
	}
	return &report, nil
}

// workerDoctor reports this machine. Only what could leave it is printed, which is a kind
// and a status, and never who is signed in.
func (c *invocation) workerDoctor(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	if c.doctor == nil {
		return c.usageError("this build has no doctor")
	}
	manifest, err := c.doctor(c.ctx)
	var section *lanes.WorktreeReport
	var git *lanes.GitReport
	if err == nil {
		git = c.gitSection()
		section, err = c.worktreeSection()
	}
	code := c.finish(err, doctorReport{Manifest: manifest, InitDaemon: c.initMarked, Git: git, Worktree: section}, func() string {
		var out strings.Builder
		for _, driver := range manifest.Drivers {
			out.WriteString(doctorLine(driver))
		}
		if c.initMarked {
			out.WriteString(initDaemonLine)
		}
		if git != nil {
			out.WriteString(gitText(*git))
		}
		if section != nil {
			out.WriteString(worktreeText(*section))
		}
		return out.String()
	})
	if code == ExitOK && (!anyUsable(manifest) || git != nil && !git.Usable()) {
		return ExitError
	}
	return code
}

func anyUsable(manifest ledger.Manifest) bool {
	for _, driver := range manifest.Drivers {
		if runs, _ := manifest.Runs(map[string]string{ledger.NeedDriver: driver.Driver}); runs {
			return true
		}
	}
	return false
}
