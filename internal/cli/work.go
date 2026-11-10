package cli

import (
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/api"
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
	client := c.client()
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
	client := c.client()
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
	client := c.client()
	defer client.Close()
	row, err := client.WorkOrder(c.ctx, id)
	return c.finish(err, row, func() string { return workOrderText(row) })
}

// workOrderText prints a work order, and says what its start waits for when something other than a
// lack of ready work holds it.
func workOrderText(row workorders.WorkOrder) string {
	var out strings.Builder
	out.WriteString(orderLine(row))
	fmt.Fprintf(&out, "fence\t%d\nworker\t%s\ncontract\t%s@%d\n", row.Fence, row.LeaseWorker, row.ContractID, row.ContractVersion)
	if row.Base != "" {
		fmt.Fprintf(&out, "base\t%s\n", row.Base)
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
	client := c.client()
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
	client := c.client()
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
	client := c.client()
	defer client.Close()
	err := client.Release(c.ctx, id, claim, request)
	return c.finish(err, struct {
		Released string `json:"released"`
	}{id}, func() string { return id + "\treleased\n" })
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
	client := c.client()
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
		for _, artifact := range view.Artifacts {
			fmt.Fprintf(&out, "artifact\t%s\t%s\t%s\n", artifact.Kind, artifact.URI, artifact.Digest)
		}
		for _, check := range view.Checks {
			fmt.Fprintf(&out, "check\t%s\t%s\t%s\t%s\n", check.Name, check.Status, check.Source, check.Criterion)
		}
		return out.String()
	})
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
	client := c.client()
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
	client := c.client()
	defer client.Close()
	result, err := client.Rework(c.ctx, id, api.ReworkRequest{Reason: *reason})
	return c.finish(err, result, func() string { return fmt.Sprintf("%s\t%s\n", id, result.State) })
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
	client := c.client()
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
	client := c.client()
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
	client := c.client()
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
		raw, err := os.ReadFile(*layer)
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
	client := c.client()
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
	client := c.client()
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
	client := c.client()
	defer client.Close()
	lane, err := client.Lane(c.ctx, id)
	return c.finish(err, lane, func() string { return laneDetail(lane) })
}

// laneDetail is a lane's line, and where its branch began: the ref and the commit the claim named, which
// review diffs against and integration targets. A lane that began where its checkout was names none.
func laneDetail(lane ledger.LaneView) string {
	text := laneLine(lane)
	if lane.BaseCommit != "" {
		text += fmt.Sprintf("base\t%s\t%s\n", dashIfEmpty(lane.BaseRef), lane.BaseCommit)
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
	client := c.client()
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
	workers, err := client.Workers(c.ctx)
	if workers == nil {
		workers = []ledger.Worker{}
	}
	return c.finish(err, api.WorkerList{Workers: workers}, func() string {
		var out strings.Builder
		for _, worker := range workers {
			fmt.Fprintf(&out, "%s\t%s\tepoch %d\tprotocol %d\n", worker.ID, worker.Name, worker.Epoch, worker.Protocol)
		}
		return out.String()
	})
}

// workerToken prints a join token. This is the one time anyone sees it, because the
// ledger keeps only its digest.
func (c *invocation) workerToken(args []string) int {
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
	token, err := client.CreateJoinToken(c.ctx)
	return c.finish(err, api.JoinTokenResponse{Token: token}, func() string { return token + "\n" })
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
	client := c.client()
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
	client := c.client()
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
	client := c.client()
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
	client := c.client()
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
	client, ok, code := c.noArguments(args)
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

func (c *invocation) noArguments(args []string) (*api.Client, bool, int) {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return nil, false, ExitUsage
	}
	if len(positional) != 0 {
		return nil, false, c.usageError("unexpected argument %q", positional[0])
	}
	return c.client(), true, ExitOK
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
	client := c.client()
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

// doctorLine states one driver the way a person reads it: usable or not, and why.
func doctorLine(driver ledger.DriverManifest) string {
	if v := vendorOf(driver.Driver); failedCheck(driver, v) {
		return fmt.Sprintf("%s\tunusable\t%s. Run %s --version to see why, and repair or reinstall %s.\n", driver.Driver, driver.Detail, v.command, v.product)
	}
	if !driver.Present {
		return fmt.Sprintf("%s\tabsent\t%s\n", driver.Driver, driver.Detail)
	}
	qualified := "qualified"
	if !driver.Qualified {
		qualified = "unqualified"
	}
	kind := driver.SignIn.Kind
	if kind == "" {
		kind = "-"
	}
	return fmt.Sprintf("%s\t%s\t%s\tsign-in %s\t%s\n", driver.Driver, driver.Version, qualified, kind, driver.SignIn.Status)
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
	code := c.finish(err, manifest, func() string {
		var out strings.Builder
		for _, driver := range manifest.Drivers {
			out.WriteString(doctorLine(driver))
		}
		return out.String()
	})
	if code == ExitOK && !anyUsable(manifest) {
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
