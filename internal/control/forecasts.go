package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/roster"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Forecasts keeps each plan's forecasts current (ADR-0026, FR-EST-8): the forecast tick forecasts the newest version
// of every plan again when what its forecast rests on changed, as when work in it ended or started, its estimates
// moved, the version was approved, or the lane cap changed, and at least hourly while its forecast has dates. The
// simulation runs outside any transaction, and its forecasts are kept, with the estimate set they were made from,
// in a transaction that takes no revision.
type Forecasts struct {
	store   ports.Store
	windows messages.Windows
	report  func(error)
}

// NewForecasts returns the forecasts of a store.
func NewForecasts(store ports.Store) *Forecasts { return &Forecasts{store: store} }

// forecastEvery is how far the ledger's time moves past a forecast with dates before it is made again with nothing
// else new, so its dates and its started work's time so far follow the ledger at least hourly.
const forecastEvery = int64(time.Hour / time.Millisecond)

// forecastRun is one plan version's forecast to make: what it is made from and the digest of that; the estimate set
// its estimates are in, the version's newest when that set says what they say, or else a new set to keep beside
// it, and none when the version has no open work; and the ID of the version's newest set when it was read.
type forecastRun struct {
	in      delivery.ForecastInput
	version int64
	digest  string
	set     delivery.EstimateSet
	newSet  bool
	read    string
}

// Refresh forecasts each plan version that is due, and returns how many it kept forecasts for. What a forecast is
// made from is read at one revision, whose recorded time it runs from, so the same revision gives the same
// forecast. Then it sends each planning lane whose session is open the estimates and forecasts of the version it
// submitted, which the tick has now forecast (FR-EST-12).
func (f *Forecasts) Refresh(ctx context.Context, scope ledger.WorkspaceID) (int, error) {
	var runs []forecastRun
	err := f.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		runs, err = dueForecasts(tx)
		return err
	})
	if err != nil {
		return 0, err
	}
	kept := 0
	for _, run := range runs {
		// A large plan's simulation takes seconds, so it holds no transaction, and a shutdown stops it before each
		// plan and between its trials.
		if err := ctx.Err(); err != nil {
			return kept, err
		}
		forecasts, err := delivery.ForecastOf(ctx, run.in)
		if err != nil {
			return kept, err
		}
		done, err := f.keep(ctx, scope, run, forecasts)
		if err != nil {
			return kept, err
		}
		if done {
			kept++
		}
	}
	return kept, f.tell(ctx, scope)
}

// keep keeps one run's forecasts, and the set they were made from when it is new. It keeps nothing when the plan
// has a newer version than the run's, whose estimates are current and which the next tick forecasts; when a
// forecast of a later revision was kept since; or when a set of the version kept since says something other than
// the run's estimates, which the next tick forecasts from.
func (f *Forecasts) keep(ctx context.Context, scope ledger.WorkspaceID, run forecastRun, forecasts []delivery.Forecast) (bool, error) {
	kept := false
	err := f.store.Forecast(ctx, scope, func(tx ports.ForecastTx) error {
		newestVersion, err := tx.Planning().LatestPlanVersion(run.in.Plan)
		switch {
		case errors.Is(err, ports.ErrNotFound):
			return nil
		case err != nil:
			return err
		case newestVersion.Version != run.version:
			return nil
		}
		latest, err := tx.Forecasts().Latest(run.in.Plan, run.version)
		if err != nil {
			return err
		}
		if last := planForecast(latest); last != nil && last.Rev > run.in.Rev {
			return nil
		}
		set, newSet := run.set, run.newSet
		// A run with no open work rests on no estimate, so no set kept since bears on it.
		if len(run.in.Work) != 0 {
			newest, err := tx.Estimates().NewestOf(run.in.Plan, run.version)
			switch {
			case errors.Is(err, ports.ErrNotFound):
			case err != nil:
				return err
			case newest.ID != run.read:
				if !holdsTheSame(newest, run.in.Work) {
					return nil
				}
				set, newSet = newest, false
			}
		}
		if newSet {
			if err := tx.EstimateSets().Add(set); err != nil {
				return err
			}
		}
		now, err := tx.Now()
		if err != nil {
			return err
		}
		for i := range forecasts {
			if forecasts[i].ID, err = newID(now); err != nil {
				return err
			}
			forecasts[i].SetID, forecasts[i].PlanVersion, forecasts[i].Inputs = set.ID, run.version, run.digest
			forecasts[i].Day, forecasts[i].CreatedAt = delivery.DayOf(now), now
		}
		kept = true
		return tx.Forecast().Keep(forecasts)
	})
	return kept, err
}

// planForecast finds a plan version's own forecast among its latest, or none.
func planForecast(latest []delivery.Forecast) *delivery.Forecast {
	for i := range latest {
		if latest[i].Milestone == "" {
			return &latest[i]
		}
	}
	return nil
}

// forecastDue says a plan version is due a forecast: it has none, what its last one rests on changed, or that one
// has dates and the ledger's time has moved an hour past the time it ran from.
func forecastDue(last *delivery.Forecast, digest string, asOf int64) bool {
	return last == nil || last.Inputs != digest || (last.Status == delivery.Forecasted && asOf-last.AsOf >= forecastEvery)
}

// holdsTheSame says a set holds an estimate of each work order given that says what the work order's own does.
func holdsTheSame(set delivery.EstimateSet, work []delivery.ForecastWork) bool {
	held := map[string]delivery.Estimate{}
	for _, e := range set.Estimates {
		held[e.WorkOrder] = e
	}
	for _, w := range work {
		e, ok := held[w.WorkOrder]
		if !ok || !e.SaysTheSame(w.Estimate) {
			return false
		}
	}
	return true
}

// forecastReads are what every plan's forecast in one read is made from: the estimate reads and the roster for each
// work order's keys; the policy, the delivery records, and the pool, for its estimate and the records it draws; the
// revision, its recorded time, and database time; the lane cap and whether any worker serves the project; the
// median wait for a person; every dependency, by the work order that holds it; and every approval.
type forecastReads struct {
	tx        ports.ReadTx
	estimates estimateReads
	view      roster.Resolved
	policy    delivery.EstimatePolicy
	records   []delivery.Record
	pool      []delivery.Record
	noPool    bool
	rev       int64
	asOf      int64
	now       int64
	cap       int64
	noWorker  bool
	median    int64
	edges     map[string][]planning.Dependency
	approvals []authority.Approval
}

// forecastTarget is the work one dependency names, found as admission finds it: the work order and whether it
// ended, or, for a name nothing holds yet, the name, which is unfinished work outside the plan.
type forecastTarget struct {
	id    string
	found bool
	ended bool
}

// dueForecasts reads the forecast due of each plan's newest version, within one read. A ledger with no event has
// nothing to forecast.
func dueForecasts(tx ports.ReadTx) ([]forecastRun, error) {
	workspace, err := tx.Workspace()
	if err != nil {
		return nil, err
	}
	at, err := tx.Events(workspace.Rev-1, 1)
	if err != nil || len(at) == 0 {
		return nil, err
	}
	r := forecastReads{
		tx: tx, rev: workspace.Rev, asOf: at[0].RecordedAt, edges: map[string][]planning.Dependency{},
		estimates: estimateReads{
			orders: tx.WorkOrders(), roster: tx.Roster(), repositories: tx.Repositories(), planning: tx.Planning(),
			policies: tx.Policies(), deliveries: tx.Deliveries(), pool: tx.Pool(),
		},
	}
	if r.now, err = tx.Now(); err != nil {
		return nil, err
	}
	if r.policy, err = estimatePolicyIn(tx.Policies()); err != nil {
		return nil, err
	}
	if r.records, err = tx.Deliveries().Records(); err != nil {
		return nil, err
	}
	r.noPool = !tx.Pool().Kept()
	if r.policy.ShareHistory && !r.noPool {
		if r.pool, err = tx.Pool().Records(); err != nil {
			return nil, err
		}
	}
	view, err := ResolveIn(tx.Roster())
	if err != nil {
		return nil, err
	}
	r.view = view.Resolved
	workers, err := tx.Workers().List()
	if err != nil {
		return nil, err
	}
	r.cap, r.noWorker = laneCapOf(workspace, workers)
	r.median = delivery.PersonMedianMs(r.records, r.policy)
	edges, err := tx.Planning().AllDependencies()
	if err != nil {
		return nil, err
	}
	for _, edge := range edges {
		if edge.Kind.Blocks() {
			r.edges[edge.WorkOrder] = append(r.edges[edge.WorkOrder], edge)
		}
	}
	if r.approvals, err = tx.Approvals().All(); err != nil {
		return nil, err
	}
	plans, err := tx.Planning().Plans()
	if err != nil {
		return nil, err
	}
	var runs []forecastRun
	for _, plan := range plans {
		version, err := tx.Planning().LatestPlanVersion(plan.ID)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		run, due, err := r.run(version)
		if err != nil {
			return nil, err
		}
		if due {
			runs = append(runs, run)
		}
	}
	return runs, nil
}

// laneCapOf is the lane cap a forecast runs under, by the product's rule that zero caps nothing: the tighter of
// the project's work-in-progress cap and the sum of the lane caps of the workers serving it, each held to the
// project's lanes per worker, where one worker with no cap leaves the sum uncapped. Every worker the installation
// registers serves the project, and with none, nothing can run its work. The cap is all a forecast holds work to:
// the per-repository cap, a worker's autonomy and run windows, capability classes, and the fleet's mode are not
// modelled, so the dates can be early when one of them holds work back.
func laneCapOf(workspace ledger.Workspace, workers []ledger.Worker) (int64, bool) {
	if len(workers) == 0 {
		return 0, true
	}
	var sum int64
	for _, worker := range workers {
		lanes := tighter(workspace.WorkerLimit, worker.LaneCap())
		if lanes == 0 {
			return workspace.WIPLimit, false
		}
		sum += lanes
	}
	return tighter(workspace.WIPLimit, sum), false
}

// run reads one plan version's forecast, and says whether it is due. Its work is the open `change` and `rework`
// work of the contracts the version covers, each estimated as an estimate is made now, with the records that
// estimate rests on.
func (r forecastReads) run(version planning.PlanVersion) (forecastRun, bool, error) {
	orders, err := planWork(r.tx.WorkOrders(), version)
	if err != nil {
		return forecastRun{}, false, err
	}
	in := delivery.ForecastInput{
		Plan: version.PlanID, PlanDigest: version.Digest, Rev: r.rev, Now: r.asOf, Cap: r.cap, NoWorker: r.noWorker,
		Policy: r.policy, PersonMedianMs: r.median, Milestones: milestonesOf(version),
	}
	open := map[string]bool{}
	var targets []forecastTarget
	for _, order := range orders {
		if !estimable(order) || open[order.ID] {
			continue
		}
		open[order.ID] = true
		work, waits, err := r.work(order)
		if err != nil {
			return forecastRun{}, false, err
		}
		in.Work, targets = append(in.Work, work), append(targets, waits...)
	}
	in.Outside = outsideOf(targets, open)
	run := forecastRun{in: in, version: version.Version, digest: forecastDigest(in, r.approved(version))}
	latest, err := r.tx.Forecasts().Latest(version.PlanID, version.Version)
	if err != nil {
		return forecastRun{}, false, err
	}
	if !forecastDue(planForecast(latest), run.digest, r.asOf) {
		return forecastRun{}, false, nil
	}
	// A version with no open work rests on no estimate, so its forecasts name no set.
	if len(in.Work) == 0 {
		return run, true, nil
	}
	newest, err := r.tx.Estimates().NewestOf(version.PlanID, version.Version)
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		return forecastRun{}, false, err
	}
	run.set, run.read = newest, newest.ID
	if holdsTheSame(newest, in.Work) {
		return run, true, nil
	}
	set := delivery.EstimateSet{
		Plan: version.PlanID, PlanVersion: version.Version, Rev: r.rev, EstimatorVersion: delivery.EstimatorVersion,
		PolicyVersion: r.policy.Version, CreatedAt: r.now,
	}
	if set.ID, err = newID(r.now); err != nil {
		return forecastRun{}, false, err
	}
	for _, w := range in.Work {
		e := w.Estimate
		if e.ID, err = newID(r.now); err != nil {
			return forecastRun{}, false, err
		}
		set.Estimates = append(set.Estimates, e)
	}
	run.set, run.newSet = set.Seal(), true
	return run, true, nil
}

// work reads one open work order as a forecast runs it: its milestone, from the contract version it pins; its
// priority, and the work its dependencies name; its estimate and the records it rests on; and what it has had so
// far, at the revision's time.
func (r forecastReads) work(order workorders.WorkOrder) (delivery.ForecastWork, []forecastTarget, error) {
	keys, err := r.estimates.keysOf(r.view, order)
	if err != nil {
		return delivery.ForecastWork{}, nil, err
	}
	targets, err := r.targetsOf(order.ID)
	if err != nil {
		return delivery.ForecastWork{}, nil, err
	}
	in := delivery.EstimateInput{WorkOrder: order.ID, Keys: keys, Records: r.records, Pool: r.pool, Policy: r.policy, Rev: r.rev, NoPool: r.noPool}
	work := delivery.ForecastWork{
		WorkOrder: order.ID, Title: order.Title, Priority: order.Priority,
		Estimate: delivery.EstimateOf(in), Records: delivery.Drawn(in),
	}
	for _, target := range targets {
		work.DependsOn = append(work.DependsOn, target.id)
	}
	if order.ContractID != "" {
		contract, err := r.tx.Planning().Contract(order.ContractID, order.ContractVersion)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return delivery.ForecastWork{}, nil, err
		}
		work.Milestone = contract.Milestone
	}
	progress, err := progressOf(r.tx, order.ID, r.asOf)
	if err != nil {
		return delivery.ForecastWork{}, nil, err
	}
	work.Started, work.SoFarMs, work.LaneSoFarMs, work.SpentUSD = progress.Started, progress.DeliveryMs, progress.LaneMs, progress.CostUSD
	return work, targets, nil
}

// targetsOf finds the work each of a work order's dependencies names, as admission does: a dependency a file or a
// tracker wrote names its target by the ID that source gave it, which the external links turn into the work order.
func (r forecastReads) targetsOf(order string) ([]forecastTarget, error) {
	var out []forecastTarget
	for _, edge := range r.edges[order] {
		found, held, err := dependencyOrder(r.tx.Planning(), r.tx.WorkOrders(), edge)
		if err != nil {
			return nil, err
		}
		if !held {
			out = append(out, forecastTarget{id: edge.DependsOn})
			continue
		}
		out = append(out, forecastTarget{id: found.ID, found: true, ended: workorders.Terminal(found.State)})
	}
	return out, nil
}

// outsideOf lists the unfinished work outside a forecast's work that its work depends on: each work order a
// dependency found that is not in the forecast and has not ended, and each name nothing holds yet. Work that ended
// counts for nothing.
func outsideOf(targets []forecastTarget, open map[string]bool) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, target := range targets {
		if target.ended || (target.found && open[target.id]) || seen[target.id] {
			continue
		}
		seen[target.id] = true
		out = append(out, target.id)
	}
	slices.Sort(out)
	return out
}

// approved says a member approved a plan version at its digest.
func (r forecastReads) approved(version planning.PlanVersion) bool {
	return slices.ContainsFunc(r.approvals, func(a authority.Approval) bool {
		return a.Kind == authority.KindPlan && a.Plan == version.PlanID && a.PlanVersion == version.Version &&
			a.State == authority.ApprovalApproved && a.SubjectDigest == version.Digest
	})
}

// forecastDigest is the digest a forecast is compared by: what it is made from, and whether its version is
// approved, so an approval forecasts the version again.
func forecastDigest(in delivery.ForecastInput, approved bool) string {
	digest := delivery.ForecastDigest(in)
	if !approved {
		return digest
	}
	sum := sha256.Sum256([]byte(digest + "\x00approved"))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// milestonesOf lists the milestones a plan version covers, by the ID a contract names each with.
func milestonesOf(version planning.PlanVersion) []string {
	var out []string
	for _, entry := range version.Records {
		if planning.EntryKind(entry.Ref) == string(planning.FileMilestone) {
			_, id, _ := strings.Cut(entry.Ref, ":")
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// progressOf reads what an unfinished work order has had so far at a time: its timeline to then, its review work
// orders' timelines, and what their lanes reported. Its durations rest on its states alone, so it reads no waits
// and no capacity.
func progressOf(tx ports.ReadTx, id string, at int64) (delivery.Progress, error) {
	timeline, events, err := timelineAt(tx, id, at)
	if err != nil {
		return delivery.Progress{}, err
	}
	facts := delivery.Facts{}
	if facts.Usage, err = usageOf(tx, events); err != nil {
		return delivery.Progress{}, err
	}
	reviews, err := tx.WorkOrders().ReviewsOf(id)
	if err != nil {
		return delivery.Progress{}, err
	}
	for _, review := range reviews {
		reviewed, reviewEvents, err := timelineAt(tx, review.ID, at)
		if err != nil {
			return delivery.Progress{}, err
		}
		usage, err := usageOf(tx, reviewEvents)
		if err != nil {
			return delivery.Progress{}, err
		}
		facts.Reviews, facts.Usage = append(facts.Reviews, reviewed), addUsage(facts.Usage, usage)
	}
	return delivery.ProgressOf(timeline, facts), nil
}

// timelineAt derives one work order's timeline from its own events, to a time, and returns the events too.
func timelineAt(tx ports.ReadTx, id string, at int64) (delivery.Timeline, []ledger.Event, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: workorders.SubjectKind, ID: id})
	if err != nil {
		return delivery.Timeline{}, nil, err
	}
	timeline, err := delivery.Derive(delivery.Input{WorkOrder: id, Events: events, Now: at})
	return timeline, events, err
}
