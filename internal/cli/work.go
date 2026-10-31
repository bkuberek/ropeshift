package cli

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/workorders"
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
	set.Int64Var(&request.Priority, "priority", 0, "the priority")
	set.Var(&criteria, "criterion", "an acceptance criterion, repeatable")
	set.Var(&dependsOn, "depends-on", "a work order this one needs, repeatable")
	set.Var(&requirements, "requirement", "a requirement key, repeatable")
	positional, ok := parse(set, args)
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
	positional, ok := parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	client := c.client()
	defer client.Close()
	rows, err := client.WorkOrders(c.ctx, workorders.State(*state))
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
	positional, ok := parse(set, args)
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
	return c.finish(err, row, func() string {
		return orderLine(row) + fmt.Sprintf("fence\t%d\nworker\t%s\ncontract\t%s@%d\n", row.Fence, row.LeaseWorker, row.ContractID, row.ContractVersion)
	})
}

// workReview exits with its own code when the definition of ready refuses, because
// a script must not read a recorded review as a move to `ready`.
func (c *invocation) workReview(args []string) int {
	set := c.flags()
	positional, ok := parse(set, args)
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
	positional, ok := parse(set, args)
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
	positional, ok := parse(set, args)
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

func (c *invocation) workReassign(args []string) int {
	set := c.flags()
	var request api.ReassignRequest
	set.StringVar(&request.Reason, "reason", "", "why the holder's work no longer counts")
	positional, ok := parse(set, args)
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

// laneStart asks the worker holding a claim to run a lane (FR-WO-9). It returns as soon
// as the ledger holds the request, and `work show` follows the lane from there.
func (c *invocation) laneStart(args []string) int {
	set := c.flags()
	var request api.StartLane
	set.StringVar(&request.LaneID, "lane", "", "a client-generated ID, so a retry names the same lane")
	positional, ok := parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
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
	positional, ok := parse(set, args)
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
	positional, ok := parse(set, args)
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
	return c.finish(err, lane, func() string { return laneLine(lane) })
}

func (c *invocation) sourceList(args []string) int {
	set := c.flags()
	positional, ok := parse(set, args)
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
		}
		return out.String()
	})
}

func (c *invocation) workerList(args []string) int {
	set := c.flags()
	positional, ok := parse(set, args)
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
	positional, ok := parse(set, args)
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
