package cli

import (
	"cmp"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

const (
	// defaultPlanTitle names a session that started with no goal. The planning agent asks for one.
	defaultPlanTitle = "Planning session"
	// maxPlanTitle bounds a title taken from a goal's first line.
	maxPlanTitle = 80
	// planCriterion is the one criterion a planning session's work order holds, because the definition
	// of ready wants at least one and a plan has no test to run.
	planCriterion = "The plan is written to the repository as files, and the person has gone through it in this session."
)

// planStarted is what `plan start --json` prints.
type planStarted struct {
	WorkOrder  string `json:"work_order"`
	Lane       string `json:"lane"`
	Worker     string `json:"worker"`
	Repository string `json:"repository"`
	// Attach is the command that joins the session.
	Attach string `json:"attach"`
	// Web is the session's page in the web UI, which shows the same pane. It is empty when the web UI
	// is off, or when the daemon cannot say where it listens.
	Web string `json:"web,omitempty"`
}

// planStart opens a planning session: it chains the calls a member makes by hand, so each step is an
// audited member action and the state machine has no shortcut for it, and then attaches the person's
// terminal to the lane's pane. It creates a `plan` work order with the goal, passes its contract
// review, claims it on a worker, and starts the lane.
func (c *invocation) planStart(args []string) int {
	set := c.flags()
	goal := set.String("goal", "", "what the plan is for, as text")
	goalFile := set.String("goal-file", "", "what the plan is for, in a file")
	worker := set.String("worker", "", "the worker that runs the session, by default the one worker online")
	repository := set.String("repository", "", "the repository to plan in, by default the one source the daemon reads")
	noAttach := set.Bool("no-attach", false, "start the session and leave your terminal alone")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	text, err := c.readGoal(*goal, *goalFile)
	if err != nil {
		return c.usageError("%v", err)
	}
	client := c.client()
	defer client.Close()
	chosenRepository, code := c.chooseRepository(client, *repository)
	if code != ExitOK {
		return code
	}
	chosenWorker, code := c.chooseWorker(client, *worker)
	if code != ExitOK {
		return code
	}
	started, code := c.openSession(client, text, chosenRepository, chosenWorker)
	if code != ExitOK {
		return code
	}
	attach := !*noAttach && !c.asJSON && c.terminal != nil && c.panes != nil
	if !attach {
		return c.finish(nil, started, func() string {
			text := fmt.Sprintf("Join the session with: %s\n", started.Attach)
			if started.Web != "" {
				text += fmt.Sprintf("Or open it in the web UI: %s\n", started.Web)
			}
			return text
		})
	}
	if code := c.awaitPane(client, started); code != ExitOK {
		return code
	}
	return c.attach(client, started.Lane)
}

// readGoal reads the goal from the one place the person gave it. A session with none starts, and the
// planning agent asks what the plan is for.
func (c *invocation) readGoal(text, file string) (string, error) {
	if text != "" && file != "" {
		return "", errors.New("give --goal or --goal-file, not both")
	}
	if file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		text = string(raw)
	}
	return strings.TrimSpace(text), nil
}

// planTitle is a goal's first line, which is what a person scanning a list would call it.
func planTitle(goal string) string {
	for line := range strings.SplitSeq(goal, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) > maxPlanTitle {
			line = string([]rune(line)[:maxPlanTitle])
		}
		return line
	}
	return defaultPlanTitle
}

// chooseRepository finds the repository the session plans in: the one the person names, or the only
// one the daemon reads. A name matches a source's instance or its ID, as `source list` prints it.
func (c *invocation) chooseRepository(client *api.Client, named string) (string, int) {
	list, err := client.Sources(c.ctx)
	if err != nil {
		return "", c.failure(err)
	}
	var instances []string
	for _, source := range list.Sources {
		if named != "" && (source.Instance == named || source.ID == named) {
			return source.Instance, ExitOK
		}
		instances = append(instances, source.Instance)
	}
	switch {
	case named != "":
		return "", c.usageError("no source reads a repository %q; the daemon reads: %s", named, strings.Join(instances, ", "))
	case len(instances) == 0:
		return "", c.refuse(ExitConflict, "the daemon reads no source, so there is no repository to plan in; start it with --source")
	case len(instances) > 1:
		return "", c.usageError("the daemon reads more than one repository; pass --repository with one of: %s", strings.Join(instances, ", "))
	}
	return instances[0], ExitOK
}

// chooseWorker finds the worker that runs the session: the one the person names, the only one
// registered, or the only one of several whose word is fresh. The ledger cannot say a worker is up,
// because a heartbeat writes nothing about it and an idle worker reads as last reported once its
// session is old, so freshness only tells apart workers that are already several. A guess between
// them is the person's to make.
func (c *invocation) chooseWorker(client *api.Client, named string) (string, int) {
	if named != "" {
		return named, ExitOK
	}
	workers, err := client.Workers(c.ctx)
	if err != nil {
		return "", c.failure(err)
	}
	switch len(workers) {
	case 0:
		return "", c.refuse(ExitConflict, "no worker is registered to run the session; start the daemon, or name a worker with --worker")
	case 1:
		return workers[0].ID, ExitOK
	}
	var floor api.ShopFloor
	if err := client.ReadView(c.ctx, api.ViewShopFloor, "", &floor); err != nil {
		return "", c.failure(err)
	}
	freshness := map[string]string{}
	var fresh []string
	for _, line := range floor.Workers {
		freshness[line.Worker.ID] = line.Freshness
		if line.Freshness == ledger.FreshnessObserved {
			fresh = append(fresh, line.Worker.ID)
		}
	}
	if len(fresh) == 1 {
		return fresh[0], ExitOK
	}
	listed := make([]string, 0, len(workers))
	for _, worker := range workers {
		listed = append(listed, fmt.Sprintf("%s (%s, %s)", worker.ID, worker.Name, cmp.Or(freshness[worker.ID], ledger.FreshnessLastReported)))
	}
	return "", c.usageError("more than one worker is registered, and the ledger cannot say which one is up; pass --worker with one of: %s", strings.Join(listed, ", "))
}

// openSession makes the member calls in order. A step that fails says which work order it left behind.
func (c *invocation) openSession(client *api.Client, goal, repository, worker string) (planStarted, int) {
	id, err := newID()
	if err != nil {
		return planStarted{}, c.failure(err)
	}
	created, err := client.CreateWorkOrder(c.ctx, api.CreateWorkOrder{
		ID: id, Kind: workorders.KindPlan, Title: planTitle(goal), Statement: goal, RepositoryID: repository,
		Criteria: []planning.Criterion{{ID: "c1", Text: planCriterion}},
	})
	if err != nil {
		return planStarted{}, c.failure(err)
	}
	c.say("Work order %s recorded: %s.\n", created.ID, created.Title)
	left := func(code int) (planStarted, int) {
		if !c.asJSON {
			fmt.Fprintf(c.stderr, "ropeshift %s: work order %s was recorded; read where it stands with 'ropeshift work show %s'\n", c.name, id, id)
		}
		return planStarted{}, code
	}
	review, err := client.Review(c.ctx, id)
	if err != nil {
		return left(c.failure(err))
	}
	if len(review.Failures) > 0 {
		for _, failure := range review.Failures {
			fmt.Fprintf(c.stdout, "not ready: %s: %s\n", failure.Check, failure.Detail)
		}
		return left(ExitNotReady)
	}
	c.say("Contract review passed.\n")
	claim, err := newID()
	if err != nil {
		return left(c.failure(err))
	}
	if _, err := client.Claim(c.ctx, id, api.ClaimRequest{ClaimID: claim, WorkerID: worker}); err != nil {
		return left(c.failure(err))
	}
	c.say("Claimed on worker %s.\n", worker)
	lane, err := newID()
	if err != nil {
		return left(c.failure(err))
	}
	if _, err := client.StartLane(c.ctx, id, api.StartLane{LaneID: lane}); err != nil {
		return left(c.failure(err))
	}
	c.say("Lane %s started.\n", lane)
	return planStarted{
		WorkOrder: id, Lane: lane, Worker: worker, Repository: repository, Attach: program + " lane attach " + lane, Web: c.lanePage(client, lane),
	}, ExitOK
}

// lanePage is a lane's page in the web UI, where its pane shows, at the address the daemon says its web
// UI bound. Only the daemon knows a port the kernel picked, so a daemon whose web UI is off, or that
// cannot say, has no page named, rather than one at an address that may be wrong.
func (c *invocation) lanePage(client *api.Client, lane string) string {
	health, err := client.Health(c.ctx)
	if err != nil || health.Web == "" {
		return ""
	}
	return (&url.URL{Scheme: "http", Host: health.Web, Path: "/lane", RawQuery: url.Values{"id": {lane}}.Encode()}).String()
}

// awaitPane waits until the new lane has a pane on this machine. The worker takes the lane, makes its
// worktree, and opens the pane, and a work order that stops on the way says so in its state, which
// ends the wait at once.
func (c *invocation) awaitPane(client *api.Client, started planStarted) int {
	c.say("Waiting for the worker to open the lane's pane.\n")
	deadline := time.Now().Add(paneWait)
	for {
		lane, err := client.Lane(c.ctx, started.Lane)
		var refused *api.Error
		switch {
		case err == nil:
			if lane.ActualState == ledger.StateEnded {
				return c.refuse(ExitConflict, "lane %s ended before its pane opened; read why with 'ropeshift work show %s'", started.Lane, started.WorkOrder)
			}
			has, err := c.panes.Has(c.ctx, started.Lane, lane.Generation)
			if err != nil {
				return c.failure(err)
			}
			if has {
				return ExitOK
			}
		case errors.As(err, &refused) && refused.Detail.Code == api.CodeNotFound:
			// The worker has not reported the lane yet.
		default:
			return c.failure(err)
		}
		order, err := client.WorkOrder(c.ctx, started.WorkOrder)
		if err != nil {
			return c.failure(err)
		}
		switch order.State {
		case workorders.Blocked, workorders.Failed, workorders.Canceled:
			return c.refuse(ExitConflict, "work order %s is %s, so its lane opens no pane; read why with 'ropeshift work show %s'", started.WorkOrder, order.State, started.WorkOrder)
		}
		if time.Now().After(deadline) {
			return c.refuse(ExitConflict, "the lane's pane did not open within %s; the session may still start, so try '%s' in a moment", paneWait, started.Attach)
		}
		select {
		case <-c.ctx.Done():
			return c.failure(c.ctx.Err())
		case <-time.After(panePoll):
		}
	}
}
