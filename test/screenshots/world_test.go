//go:build screenshots

package screenshots

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// sampleProject is the sample's project: the installation's first one. A conversation with the assistant
// in a project created later fails on this build, so the sample stays where the assistant runs.
const sampleProject = ledger.DefaultProjectName

// world is the sample as the population lays it out, with the IDs the views open.
type world struct {
	in                            *install
	project                       string
	worker                        string
	ordersAPI, ordersWeb, catalog *clone
	// orders names each work order by its title, and contracts the ID the ledger gave its contract.
	orders, contracts map[string]string
	plan              string
	// The records a view opens, which the ledger names.
	cardItem, planItem         string
	planningLane, conversation string
	repository, agent          string
	// guided is a sample the guide walk reads, whose daemon folds delivery and forecasts every second, so a plan's
	// page shows the forecast the daemon made and not a pending line. The view captures keep the defaults.
	guided bool
}

func newWorld(in *install) *world {
	return &world{in: in, project: sampleProject, orders: map[string]string{}, contracts: map[string]string{}}
}

// populate lays out the sample through the binary, in the order a team would reach each state.
func (w *world) populate() {
	in := w.in
	in.t.Helper()
	in.gitSetup()
	w.ordersAPI = in.newClone("src", "orders-api", merged(ordersAPIFiles(), planFiles()))
	w.ordersWeb = in.newClone("src", "orders-web", ordersWebFiles())
	w.catalog = in.newClone("src", "catalog-service", catalogFiles())
	state := in.paths().StateDir
	if err := os.MkdirAll(state, 0o700); err != nil {
		in.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "profile.toml"), []byte(userProfile), 0o600); err != nil {
		in.t.Fatal(err)
	}
	in.writeFixture(assistantTurns())

	// The assistant runs only on a worker whose session opened within three heartbeats, and a page says so
	// for a running conversation too, so the beat outlasts the whole run, and the lease outlasts the beat.
	flags := []string{"--intake-tick", "200ms", "--lease-tick", "200ms", "--wake-tick", "200ms", "--batch-tick", "500ms",
		"--heartbeat", "3m", "--lease-ttl", "15m"}
	if w.guided {
		flags = append(flags, "--delivery-tick", "1s", "--forecast-tick", "1s")
	}
	in.start(flags...)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	w.worker = in.workers()[0].ID

	w.repositories()
	w.converse()
	w.readPlan()
	w.decisions()
	w.approvePlan()
	w.askQuestions()
	w.runLanes()
	w.ladder()
	w.cards()
	w.laterPlan()
	w.planningSession()
	w.roster()
	w.secondProject()
}

func merged(parts ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, part := range parts {
		maps.Copy(out, part)
	}
	return out
}

// repositories registers the project's two clones.
func (w *world) repositories() {
	in := w.in
	in.must("repository", "add", w.ordersAPI.Dir, w.ordersWeb.Dir)
	var list api.RepositoryList
	in.read(&list, "repository", "list")
	for _, repository := range list.Repositories {
		if repository.Name == "orders-api" {
			w.repository = repository.ID
		}
	}
	if w.repository == "" {
		in.t.Fatalf("no orders-api among %+v", list.Repositories)
	}
}

// secondProject creates a second project with a repository and work of its own, last, so what waits in it is
// the newest item of Needs you.
func (w *world) secondProject() {
	in := w.in
	in.must("project", "create", catalogProject, "--display-name", "Catalog")
	in.must("repository", "add", w.catalog.Dir, "--project", catalogProject)
	in.must("work", "create", "--project", catalogProject, "--title", "Index products by category",
		"--criterion", "A category page reads its products from the index", "--size", "medium")
}

// The records the guide that runs several projects at once adds to the sample.
const (
	catalogProject   = "catalog"
	orderCatalogFind = "catalog-search"
	laneCatalogFind  = "lane-catalog-search"
)

// severalProjects lays out what the guide that runs several projects at once reads beyond the sample: a lane at
// work in Catalog beside the sample's own, so the Floor's All projects shows another project's lane, and a third
// project, Promotions, with a repository and nothing running, which the guide archives. Catalog's first work still
// waits for a member's review of its contract, so two projects have work waiting. Only the guide walk lays it
// out, and the view captures keep the sample as it is.
func (w *world) severalProjects() {
	in := w.in
	in.t.Helper()
	var list api.RepositoryList
	in.read(&list, "repository", "list", "--project", catalogProject)
	if len(list.Repositories) != 1 {
		in.t.Fatalf("catalog holds %+v", list.Repositories)
	}
	in.must("work", "create", "--project", catalogProject, "--id", orderCatalogFind, "--title", "Search products by name",
		"--criterion", "A search returns every product whose name holds the words", "--size", "small",
		"--repository", list.Repositories[0].ID)
	in.must("work", "review", orderCatalogFind, "--project", catalogProject)
	in.reap(laneCatalogFind)
	in.must("work", "claim", orderCatalogFind, "--worker", w.worker, "--claim-id", "claim-"+laneCatalogFind, "--project", catalogProject)
	in.must("lane", "start", orderCatalogFind, "--lane", laneCatalogFind, "--project", catalogProject)
	in.waitFor("the catalog lane at work", func() bool {
		var view ledger.LaneView
		code, raw, _ := in.run("lane", "show", laneCatalogFind, "--json", "--project", catalogProject)
		return code == 0 && json.Unmarshal([]byte(raw), &view) == nil && view.ActualState == "working"
	})
	promotions := in.newClone("src", "promotions-service", promotionsFiles())
	in.must("project", "create", "promotions", "--display-name", "Promotions")
	in.must("repository", "add", promotions.Dir, "--project", "promotions")
}

// readPlan waits for intake to read the plan and its work, and keeps the ID the ledger gave each contract, by the
// title of its work, which the decisions bound to a contract name it by.
func (w *world) readPlan() {
	in := w.in
	in.waitFor("the plan", func() bool {
		var plans api.PlanList
		in.read(&plans, "plan", "list")
		if len(plans.Plans) != 1 {
			return false
		}
		w.plan = plans.Plans[0].ID
		return true
	})
	in.waitFor("the plan's work and its approval", func() bool {
		var view api.PlanView
		in.read(&view, "plan", "show", w.plan)
		for _, row := range view.WorkOrders {
			w.contracts[row.Title] = row.ContractID
		}
		return len(view.WorkOrders) == len(contracts) && view.Approval != nil && view.Approval.Approval.Open()
	})
	for _, c := range contracts {
		if w.contracts[c.order] == "" {
			in.t.Fatalf("no contract for the work titled %q", c.order)
		}
	}
}

// approvePlan approves the plan as a member would.
func (w *world) approvePlan() {
	in := w.in
	in.must("plan", "approve", w.plan)
	var orders api.WorkOrderList
	in.read(&orders, "work", "list")
	for _, row := range orders.WorkOrders {
		w.orders[row.Title] = row.ID
	}
	for _, c := range contracts {
		if w.orders[c.order] == "" {
			in.t.Fatalf("no work order titled %q", c.order)
		}
	}
}

// decisions records what the team decided before the work began, each bound to the contract it is about by the ID
// the ledger gave it. They come before the plan's approval, while no work is ready, so binding the contracts sends
// no work back to review.
func (w *world) decisions() {
	w.in.must("decision", "record", "--id", "d-key-ttl", "--statement", "Store each idempotency key for 24 hours.",
		"--rationale", "A partner retries for up to a day after an outage.", "--scope", "contract:"+w.contracts[orderRetries],
		"--citation", "requirement:ORD-3", "--citation", "docs/architecture.md", "--category", questions.CategoryProduct, "--reversibility", "reversible")
	w.in.must("decision", "record", "--id", "d-limiter-store", "--assumed", "--statement", "Keep rate limit counters in process memory for now.",
		"--rationale", "One instance serves the API today; a shared store waits for the second.", "--scope", "contract:"+w.contracts[orderRateLimit],
		"--citation", "requirement:ORD-4", "--category", questions.CategoryArchitecture, "--reversibility", "reversible")
}

// askQuestions records the questions the orchestrator takes up the ladder. A lane asks the first again and
// parks on it.
func (w *world) askQuestions() {
	in := w.in
	in.must("question", "record", "--id", qSigningKeys, "--text", questionSigningKeys, "--category", questions.CategorySecurity,
		"--subject", w.orders[orderSigningKeys], "--subject-kind", "work_order")
	in.must("question", "record", "--id", qRetryKey, "--text", questionRetryKey, "--category", questions.CategoryProduct,
		"--subject", w.orders[orderRetries], "--subject-kind", "work_order")
	in.must("question", "record", "--id", qLogLevel, "--text", questionLogLevel, "--category", questions.CategoryOperations)
	in.must("question", "record", "--id", qPartnerTier, "--text", questionPartnerTier, "--category", questions.CategoryProduct,
		"--subject", w.orders[orderRateLimit], "--subject-kind", "work_order")
}

// startLane claims one work order and starts its lane. A claim waits while the shared work it uses is
// merged and not yet fetched.
func (w *world) startLane(order, lane string) {
	in := w.in
	in.reap(lane)
	in.waitFor("the claim of "+order, func() bool {
		code, _, stderr := in.run("work", "claim", order, "--worker", w.worker, "--claim-id", "claim-"+lane)
		if code != 0 && !strings.Contains(stderr, "waiting_for_merge") {
			in.t.Fatalf("ropeshift work claim %s: exit %d: %s", order, code, stderr)
		}
		return code == 0
	})
	in.must("lane", "start", order, "--lane", lane)
}

func (w *world) show(id string) workorders.WorkOrder {
	var row workorders.WorkOrder
	w.in.read(&row, "work", "show", id)
	return row
}

// lane reads one lane, which is empty until the worker has started it.
func (w *world) lane(id string) ledger.LaneView {
	var view ledger.LaneView
	if code, raw, _ := w.in.run("lane", "show", id, "--json"); code == 0 {
		if err := json.Unmarshal([]byte(raw), &view); err != nil {
			w.in.t.Fatal(err)
		}
	}
	return view
}

// waitState waits for a piece of work to reach a state, and stops at once on one its script never leads to.
func (w *world) waitState(title string, state workorders.State) {
	id := w.orders[title]
	w.in.waitFor(title+" in "+string(state), func() bool {
		got := w.show(id).State
		if got != state && (got == workorders.Failed || got == workorders.Canceled) {
			w.in.t.Fatalf("%s is %s:\n%s%s", title, got, w.in.must("work", "show", id), w.in.daemonTail())
		}
		return got == state
	})
}

// runLanes runs the shared work first, which the work that uses it waits for, then starts a lane on six
// more pieces of work and waits for each to reach the state its script leads to.
func (w *world) runLanes() {
	in := w.in
	w.finish(orderErrorShape, laneErrorShape, reviewErrorShape, true)
	for _, title := range []string{orderRetries, orderRateLimit, orderErrorDocs, orderTimeouts, orderSigningKeys} {
		w.waitState(title, workorders.Ready)
	}
	w.finish(orderTimeouts, laneTimeouts, reviewTimeouts, true)
	w.finish(orderRetries, laneRetries, reviewRetries, false)
	w.startLane(w.orders[orderErrorDocs], laneErrorDocs)
	w.waitState(orderErrorDocs, workorders.Blocked)
	w.startLane(w.orders[orderSigningKeys], laneSigningKeys)
	w.waitState(orderSigningKeys, workorders.Parked)
	w.startLane(w.orders[orderRateLimit], laneRateLimit)
	in.waitFor("the rate limit lane at work", func() bool { return w.lane(laneRateLimit).ActualState == "working" })
	w.startLane(w.orders[orderRequestIDs], laneRequestIDs)
	in.waitFor("the request ID lane's approval request", func() bool {
		var list api.ApprovalList
		in.read(&list, "approval", "list", "--state", string(authority.ApprovalPending))
		for _, approval := range list.Approvals {
			if approval.Lane == laneRequestIDs {
				return true
			}
		}
		return false
	})
}

// finish runs a piece of work's lane and its review. When accept says so, a member accepts it and merges
// its branch, as a person does at the default merge authority.
func (w *world) finish(title, lane, reviewLane string, accept bool) {
	order := w.orders[title]
	w.startLane(order, lane)
	w.waitState(title, workorders.Acceptance)
	review := w.openReviewOf(order)
	w.startLane(review, reviewLane)
	w.in.waitFor("the review "+reviewLane, func() bool { return w.show(review).State == workorders.Done })
	if accept {
		w.in.must("work", "accept", order)
		w.waitState(title, workorders.Done)
		w.ordersAPI.merge("lane/"+lane, "Merge "+strings.ToLower(title[:1])+title[1:])
	}
}

// openReviewOf is the review of a work order that waits for a lane.
func (w *world) openReviewOf(order string) string {
	var found string
	w.in.waitFor("a review of "+order, func() bool {
		var list api.WorkOrderList
		w.in.read(&list, "work", "list")
		for _, row := range list.WorkOrders {
			if row.Kind == workorders.KindReview && row.Reviews == order && row.State == workorders.Ready {
				found = row.ID
			}
		}
		return found != ""
	})
	return found
}

// ladder starts the worker's orchestrator, which decides two questions and takes two to a person. Its script names
// the contracts by their ledger IDs, and states no conflict with the decisions its own overlap: the member's on the
// same contract, and the plan's, which bind every contract of the plan. A session reads the script as it starts,
// so the orchestrator's reads this one.
func (w *world) ladder() {
	in := w.in
	var planned api.DecisionList
	in.read(&planned, "decision", "list", "--scope", "plan:"+w.plan)
	if len(planned.Decisions) == 0 {
		in.t.Fatalf("the plan %s decided nothing at its approval", w.plan)
	}
	var plan []string
	for _, decision := range planned.Decisions {
		plan = append(plan, decision.ID)
	}
	in.scripts = lanes()
	in.scripts[orchestrator] = orchestratorScript(
		sampleRuling{contract: w.contracts[orderRetries], noConflict: append([]string{"d-key-ttl"}, plan...)},
		sampleRuling{contract: w.contracts[orderRateLimit], noConflict: append([]string{"d-limiter-store"}, plan...)},
	)
	in.writeFixture(assistantTurns())
	in.reap(orchestrator)
	in.must("orchestrator", "start", w.worker)
	in.waitFor("the questions to reach a person", func() bool {
		var needs api.NeedsYou
		in.view("needs-you", &needs)
		for _, item := range needs.Items {
			if item.Kind == api.NeedQuestions && item.Batch != nil && len(item.Batch.Items) == 2 {
				return true
			}
		}
		return false
	})
}

// cards has a delegate propose two changes, which wait in Needs you as confirmation cards.
func (w *world) cards() {
	in := w.in
	var created api.DelegateCreated
	in.read(&created, "delegate", "create", "--project", w.project, "--label", "Release assistant", "--expires", "7d")
	delegate := []string{authority.EnvDelegateToken + "=" + created.Token}
	in.runAs(delegate, "work", "accept", w.orders[orderRetries], "--why", "Its review passed both criteria, and nothing else waits on it.")
	in.runAs(delegate, "fleet", "caps", "--wip", "6", "--why", "Six lanes fit this machine's memory with room for the database.")
	in.waitFor("the delegate's cards", func() bool {
		var needs api.NeedsYou
		in.view("needs-you", &needs)
		cards := 0
		for _, item := range needs.Items {
			if item.Kind == api.NeedConfirm {
				cards++
				if w.cardItem == "" {
					w.cardItem = "confirm:" + item.ID
				}
			}
		}
		return cards == 2
	})
}

// laterPlan commits the plan's second version, which waits for a member's approval with its changes.
func (w *world) laterPlan() {
	in := w.in
	w.ordersAPI.commit("Plan the internal status page", laterPlanFiles())
	in.waitFor("the second version's approval", func() bool {
		var needs api.NeedsYou
		in.view("needs-you", &needs)
		for _, item := range needs.Items {
			if item.Kind == api.NeedPlanApproval {
				w.planItem = item.Kind + ":" + item.ID
				return true
			}
		}
		return false
	})
}

// planningSession opens a planning session on a pane, which the planning view attaches to. The
// conversation's session read the assistant's turns as it started, so the turns change for this one.
func (w *world) planningSession() {
	in := w.in
	in.writeFixture(planningTurns())
	var started struct {
		Lane string `json:"lane"`
	}
	in.read(&started, "plan", "start", "--no-attach", "--goal", "Let partners see their own usage and rate limits", "--repository", w.repository)
	if started.Lane == "" {
		in.t.Fatal("the planning session named no lane")
	}
	w.planningLane = started.Lane
	in.reap(started.Lane)
	in.waitFor("the planning session's pane", func() bool { return w.lane(started.Lane).ActualState == "working" })
}

// converse starts a conversation with the assistant, which answers from the fake CLI's script.
func (w *world) converse() {
	in := w.in
	var created api.ConversationOpened
	in.waitFor("a worker that runs the assistant", func() bool {
		code, raw, stderr := in.run("chat", "new", "--name", "Reliability status", "--json")
		if code != 0 && !strings.Contains(stderr, "no checkout") {
			in.t.Fatalf("ropeshift chat new: exit %d: %s", code, stderr)
		}
		return code == 0 && json.Unmarshal([]byte(raw), &created) == nil
	})
	w.conversation = created.Conversation.ID
	if created.Conversation.RunLane != "" {
		in.reap(created.Conversation.RunLane)
	}
	in.must("chat", "send", w.conversation, "--body", "What still holds the reliability plan?")
	w.answered("Resilient calls")
	in.must("chat", "send", w.conversation, "--body", "Can accepting the retry work wait?")
	w.answered("can wait")
}

// answered waits for the conversation to hold an answer with these words.
func (w *world) answered(words string) {
	shown := ""
	if !w.in.waitUntil(func() bool {
		shown = w.in.must("chat", "show", w.conversation)
		return strings.Contains(shown, words)
	}) {
		w.in.t.Fatalf("no answer holds %q:\n%s%s", words, shown, w.in.daemonTail())
	}
}

// roster changes the agent roster at both layers, so each has a history: the installation raises the
// ordinary implementer's effort, and the project gives its heaviest implementer the most effort and adds an
// API reviewer copied from the built-in reviewer.
func (w *world) roster() {
	in := w.in
	in.must("agent", "set", "implementation", "effort=high", "--installation")
	in.must("agent", "set", "heavy-implementation", "effort=max", "--project", w.project)
	in.must("agent", "add", "api-reviewer", "--from", "review", "--project", w.project)
	in.must("agent", "set", "api-reviewer",
		"purpose=Reviews a change to the public API against the error spec and the API reference.", "--project", w.project)
	w.agent = "heavy-implementation"
}
