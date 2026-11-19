package cli

import (
	"context"
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/planning"
)

func (c *invocation) planList(args []string) int {
	client, ok, code := c.noArguments(args)
	if !ok {
		return code
	}
	defer client.Close()
	plans, err := client.Plans(c.ctx)
	if plans == nil {
		plans = []planning.Plan{}
	}
	return c.finish(err, api.PlanList{Plans: plans}, func() string {
		var out strings.Builder
		for _, plan := range plans {
			fmt.Fprintf(&out, "%s\t%s\t%s\n", plan.ID, plan.Status, plan.Title)
		}
		return out.String()
	})
}

func (c *invocation) planShow(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one plan ID")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	view, err := client.Plan(c.ctx, positional[0])
	return c.finish(err, view, func() string {
		var out strings.Builder
		fmt.Fprintf(&out, "%s\t%s\t%s\n", view.Plan.ID, view.Plan.Status, view.Plan.Title)
		if view.Goal != nil {
			fmt.Fprintf(&out, "goal\t%s\t%s\n", view.Goal.ID, view.Goal.Title)
		}
		for _, contract := range view.Contracts {
			fmt.Fprintf(&out, "contract\t%s@%d\t%s\n", contract.ID, contract.Version, contract.Title)
		}
		for _, order := range view.WorkOrders {
			out.WriteString("work\t" + orderLine(order))
		}
		if view.Revision != nil {
			writeRevision(&out, *view.Revision)
		}
		if view.Approval != nil {
			writePlanApproval(&out, *view.Approval)
		}
		return out.String()
	})
}

// planRevision prints the open revision of a plan: the decisions a member made since its approved version, the
// finished work they reached, and the work that waits in contract review until a member settles it.
func (c *invocation) planRevision(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one plan ID")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	view, err := client.PlanRevision(c.ctx, positional[0])
	return c.finish(err, view, func() string {
		var out strings.Builder
		writeRevision(&out, view)
		return out.String()
	})
}

// planConfirm records that the approved version of a plan stands with the decisions made since, as a member who
// read them. It closes the revision and releases the work that waited for it.
func (c *invocation) planConfirm(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one plan ID")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	closed, err := client.ConfirmRevision(c.ctx, positional[0])
	return c.finish(err, closed, func() string {
		return fmt.Sprintf("confirmed version %d of plan %s with %s since\n", closed.ApprovedVersion, positional[0], plural(len(closed.DecisionIDs), "decision", "decisions"))
	})
}

// writeRevision prints a plan's revision, or says the plan has none open.
func writeRevision(out *strings.Builder, view api.PlanRevisionView) {
	if view.Revision == nil {
		fmt.Fprintf(out, "no revision is open for plan %s\n", view.Plan)
		return
	}
	fmt.Fprintf(out, "revision\t%s\t%s\tversion %d approved\t%s since\n", view.Plan, view.Title, view.Revision.ApprovedVersion, plural(len(view.Decisions), "decision", "decisions"))
	for _, decision := range view.Decisions {
		fmt.Fprintf(out, "  %s\n", revisionDecisionLine(decision))
	}
	for _, work := range view.Finished {
		line := fmt.Sprintf("  finished\t%s\t%s\tdecisions %s", work.WorkOrder, work.Title, strings.Join(work.Decisions, ", "))
		if work.Rework != "" {
			line += "\treworked as " + work.Rework
		}
		out.WriteString(line + "\n")
	}
	for _, work := range view.Waiting {
		fmt.Fprintf(out, "  waiting\t%s\t%s\n", work.WorkOrder, work.Title)
	}
}

func revisionDecisionLine(decision api.RevisionDecision) string {
	return fmt.Sprintf("decision %s (%s, %s): %s", decision.ID, decision.Reversibility, decision.Scope, decision.Statement)
}

// writePlanApproval prints what a member reads before approving a plan version: who asked, how it
// differs from the last approved version, every draft decision it would decide, and its forecast, or
// "forecast pending" before the forecast tick has forecast it.
func writePlanApproval(out *strings.Builder, view api.PlanApprovalView) {
	approval := view.Approval
	fmt.Fprintf(out, "approval\t%s\t%s\tversion %d\trequested by %s\n", approval.ID, approval.State, approval.PlanVersion, approval.RequestedBy)
	fmt.Fprintf(out, "  %s\n", view.Detail.Difference.Summary())
	for _, line := range []struct {
		label string
		refs  []string
	}{{"added", view.Detail.Difference.Added}, {"changed", view.Detail.Difference.Changed}, {"removed", view.Detail.Difference.Removed}} {
		if len(line.refs) != 0 {
			fmt.Fprintf(out, "  %s: %s\n", line.label, strings.Join(line.refs, ", "))
		}
	}
	for _, contract := range view.Detail.Difference.Contracts {
		fmt.Fprintf(out, "  %s: criteria %s; work orders %s\n", contract.Ref, partsWords(contract.Criteria), partsWords(contract.WorkOrders))
	}
	for _, draft := range view.Drafts {
		fmt.Fprintf(out, "  draft decision %s: %s -> %s\n", draft.ID, draft.Question, draft.Answer)
	}
	if view.Revision != nil && view.Revision.Revision != nil {
		fmt.Fprintf(out, "  decisions since version %d, which this version is read beside:\n", view.Revision.Revision.ApprovedVersion)
		for _, decision := range view.Revision.Decisions {
			fmt.Fprintf(out, "    %s\n", revisionDecisionLine(decision))
		}
	}
	if view.Detail.Adoption != nil {
		writeAdoption(out, *view.Detail.Adoption, "  ")
	}
	if view.Forecast != nil {
		writeForecasts(out, *view.Forecast, "  ")
	}
	writeRegistration(out, view.Registration)
	fmt.Fprintf(out, "  digest %s\n", approval.SubjectDigest)
}

// writeRegistration prints what approving a plan version would register of the repositories its manifest lists,
// or what the approval registered, a line for each with its path, where that leads, and its remote. A version
// that lists no manifest has none.
func writeRegistration(out *strings.Builder, registration *api.ManifestRegistration) {
	if registration == nil {
		return
	}
	if registration.Preview {
		out.WriteString("  repositories approving would register, read now on the daemon's worker's machine:\n")
	} else {
		out.WriteString("  repositories this approval registered:\n")
	}
	out.WriteString(manifestText(registration.Repositories, registration.Preview, "    "))
	if registration.Problem != "" {
		fmt.Fprintf(out, "    %s\n", plainLine(registration.Problem))
	}
}

func partsWords(d planning.PartDifference) string {
	var said []string
	for _, part := range []struct {
		mark string
		ids  []string
	}{{"+", d.Added}, {"~", d.Changed}, {"-", d.Removed}} {
		for _, id := range part.ids {
			said = append(said, part.mark+id)
		}
	}
	if len(said) == 0 {
		return "unchanged"
	}
	return strings.Join(said, " ")
}

// planApprove approves the version of a plan that waits, as a member who read what it holds. It names
// the digest of the version it showed, so a version that changed since is refused and not approved.
func (c *invocation) planApprove(args []string) int {
	set := c.flags()
	version := set.Int64("version", 0, "the plan version to approve, which must be the one that waits")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one plan ID")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	view, err := client.Plan(c.ctx, positional[0])
	if err != nil {
		return c.failure(err)
	}
	waiting := view.Approval
	if waiting == nil {
		return c.refusal(api.CodeNotFound, "plan %s has no version to approve", positional[0])
	}
	asked := waiting.Approval
	switch {
	case *version != 0 && *version != asked.PlanVersion:
		return c.refusal(api.CodeIllegalMove, "plan %s waits on version %d, and not on version %d", positional[0], asked.PlanVersion, *version)
	case asked.State == authority.ApprovalApproved:
		return c.finish(nil, asked, func() string {
			return fmt.Sprintf("%s version %d is already approved\n", waiting.Detail.Title, asked.PlanVersion)
		})
	case !asked.Open():
		return c.refusal(api.CodeIllegalMove, "the request for version %d of plan %s is %s", asked.PlanVersion, positional[0], asked.State)
	}
	// The member reads the version's forecast, or that it is pending, and each repository the manifest lists
	// before the approval registers them, as plan show prints them. A script that asked for JSON gets one
	// document, so nothing is printed ahead of it.
	if !c.asJSON {
		var preview strings.Builder
		if waiting.Forecast != nil {
			writeForecasts(&preview, *waiting.Forecast, "  ")
		}
		writeRegistration(&preview, waiting.Registration)
		fmt.Fprint(c.stdout, preview.String())
	}
	decided, err := client.DecideApproval(c.ctx, asked.ID, api.DecideApproval{Digest: asked.SubjectDigest, Allow: true})
	return c.finish(err, decided, func() string {
		return approvedText(waiting.Detail.Title, asked.PlanVersion, waiting.Detail.Difference.Summary(), decided)
	})
}

// approvedText says which version of which plan was approved and what changed, and lists what the approval
// registered of the repositories the plan's manifest adds, from the clones beside the planning repository's, and
// what it could not.
func approvedText(title string, version int64, summary string, decided api.ApprovalDecided) string {
	var out strings.Builder
	fmt.Fprintf(&out, "approved %s version %d\n", plainLine(title), version)
	fmt.Fprintf(&out, "  %s\n", plainLine(summary))
	out.WriteString(manifestText(decided.Repositories, false, ""))
	if decided.RepositoriesProblem != "" {
		fmt.Fprintf(&out, "%s\n", plainLine(decided.RepositoriesProblem))
	}
	return out.String()
}

// refusal prints a refusal the CLI found before it called anything, in the shape the API's own take.
func (c *invocation) refusal(code, format string, args ...any) int {
	return c.failure(&api.Error{Detail: api.ErrorDetail{Code: code, Message: fmt.Sprintf(format, args...)}})
}

// planReview starts a contract review as a lane, which is ready for a member to claim. Work orders
// the review reads stay where they are until a member accepts it.
func (c *invocation) planReview(args []string) int {
	set := c.flags()
	id := set.String("id", "", "the review's ID, for a retry")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one plan ID")
	}
	review := *id
	if review == "" {
		generated, err := newID()
		if err != nil {
			return c.failure(err)
		}
		review = generated
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	row, err := client.ReviewPlan(c.ctx, positional[0], api.ReviewPlan{ID: review})
	return c.finish(err, row, func() string { return orderLine(row) })
}

func (c *invocation) requirementList(args []string) int {
	client, ok, code := c.noArguments(args)
	if !ok {
		return code
	}
	defer client.Close()
	requirements, err := client.Requirements(c.ctx)
	if requirements == nil {
		requirements = []planning.Requirement{}
	}
	return c.finish(err, api.RequirementList{Requirements: requirements}, func() string {
		var out strings.Builder
		for _, requirement := range requirements {
			out.WriteString(requirementLine(requirement))
		}
		return out.String()
	})
}

func (c *invocation) requirementGet(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one requirement key")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	requirement, err := client.Requirement(c.ctx, positional[0])
	return c.finish(err, requirement, func() string { return requirementLine(requirement) })
}

// requirementLine says what one requirement version asks, and, for one the code already meets, the
// evidence that shows it.
func requirementLine(requirement planning.Requirement) string {
	line := fmt.Sprintf("%s\tv%d\t%s", requirement.Key, requirement.Version, requirement.Text)
	if requirement.Met() {
		line += "\tmet by existing code: " + strings.Join(requirement.Evidence, ", ")
	}
	return line + "\n"
}

func (c *invocation) workResolve(args []string) int {
	return c.unblock(args, (*api.ProjectClient).Resolve, "ready")
}

func (c *invocation) workReplan(args []string) int {
	return c.unblock(args, (*api.ProjectClient).Replan, "planning")
}

func (c *invocation) unblock(args []string, move func(*api.ProjectClient, context.Context, string, api.Unblock) error, to string) int {
	set := c.flags()
	var request api.Unblock
	set.StringVar(&request.Reason, "reason", "", "what changed")
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
	err := move(client, c.ctx, id, request)
	return c.finish(err, struct {
		Moved string `json:"moved"`
		State string `json:"state"`
	}{id, to}, func() string { return id + "\t" + to + "\n" })
}

// workRetry returns failed work to ready, with why. A member's retry of work with no attempts left adds
// one.
func (c *invocation) workRetry(args []string) int {
	set := c.flags()
	var request api.Retry
	set.StringVar(&request.Reason, "reason", "", "why another attempt can succeed")
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
	retried, err := client.Retry(c.ctx, id, request)
	return c.finish(err, retried, func() string { return retried.WorkOrder + "\t" + string(retried.State) + "\n" })
}

func (c *invocation) workSendBack(args []string) int {
	set := c.flags()
	var request api.SendBack
	set.StringVar(&request.Reason, "reason", "", "why the contract must change")
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
	err := client.SendBack(c.ctx, id, request)
	return c.finish(err, struct {
		SentBack string `json:"sent_back"`
	}{id}, func() string { return id + "\tplanning\n" })
}

func (c *invocation) documentList(args []string) int {
	client, ok, code := c.noArguments(args)
	if !ok {
		return code
	}
	defer client.Close()
	documents, err := client.Documents(c.ctx)
	if documents == nil {
		documents = []api.Document{}
	}
	return c.finish(err, api.DocumentList{Documents: documents}, func() string {
		var out strings.Builder
		for _, document := range documents {
			fmt.Fprintf(&out, "%s\t%s\t%s\n", document.Path, document.Digest, document.Title)
		}
		return out.String()
	})
}

// documentRef reads the arguments one document command shares.
func (c *invocation) documentRef(args []string) (api.DocumentRef, bool) {
	set := c.flags()
	var ref api.DocumentRef
	set.StringVar(&ref.Instance, "instance", "", "the repository that holds it, by its first commit")
	set.StringVar(&ref.Version, "version", "", "a version from `document versions`")
	positional, ok := c.parse(set, args)
	if !ok {
		return ref, false
	}
	if len(positional) != 1 {
		c.usageError("takes one document path")
		return ref, false
	}
	ref.Path = positional[0]
	return ref, true
}

func (c *invocation) documentShow(args []string) int {
	ref, ok := c.documentRef(args)
	if !ok {
		return ExitUsage
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	document, err := client.Document(c.ctx, ref)
	return c.finish(err, document, func() string { return document.Body })
}

func (c *invocation) documentVersions(args []string) int {
	ref, ok := c.documentRef(args)
	if !ok {
		return ExitUsage
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	versions, err := client.DocumentVersions(c.ctx, ref)
	return c.finish(err, api.DocumentVersions{Versions: versions}, func() string {
		var out strings.Builder
		for _, version := range versions {
			fmt.Fprintf(&out, "%s\t%s\n", version.Version, version.Digest)
		}
		return out.String()
	})
}

// documentRecorded prints one version of a planning document as the ledger stored it, which stays what a
// contract cited after the file moves on.
func (c *invocation) documentRecorded(args []string) int {
	set := c.flags()
	version := set.Int64("version", 0, "a version number, which defaults to the newest")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		c.usageError("takes one document reference, <kind>:<id>")
		return ExitUsage
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	read, err := client.RecordedDocument(c.ctx, positional[0], *version)
	return c.finish(err, read, func() string { return read.Body })
}
