package contract

import (
	"fmt"
	"reflect"
	"strings"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// ForecastChecks hold kept forecasts (ADR-0026, FR-EST-8): the latest of each plan version and milestone, and the
// last of each day, kept with no revision.
var ForecastChecks = []Check{
	{"FC1", "a plan version keeps the latest forecast of the plan and of each milestone, and the last of each day, with no revision", checkForecastsKept},
	{"FC2", "the forecast tick forecasts each plan's newest version from its own estimate set when what it rests on changed, a worker came, work ended, or the version was approved, and keeps nothing otherwise", checkForecastTick},
}

// dayOne is a UTC midnight, in epoch milliseconds, that the kept forecasts here date from.
const dayOne = int64(1_700_006_400_000)

// keepForecasts keeps one run's forecasts in a transaction of the forecast tick.
func (r *Rig) keepForecasts(s ports.Store, forecasts []delivery.Forecast) error {
	return s.Forecast(r.Ctx, r.Scope, func(tx ports.FoldTx) error { return tx.Forecast().Keep(forecasts) })
}

// forecastsOf reads a plan version's latest forecasts, and the history of the plan's own and of one milestone.
func (r *Rig) forecastsOf(s ports.Store, plan string, version int64, milestone string) (latest, own, of []delivery.Forecast, err error) {
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		if latest, err = tx.Forecasts().Latest(plan, version); err != nil {
			return err
		}
		if own, err = tx.Forecasts().History(plan, version, ""); err != nil {
			return err
		}
		of, err = tx.Forecasts().History(plan, version, milestone)
		return err
	})
	return latest, own, of, err
}

func checkForecastsKept(r *Rig) error {
	s := r.Store()
	plan, set := r.ID("plan"), r.ID("set")
	run := func(at, rev, end int64) []delivery.Forecast {
		forecast := func(milestone string) delivery.Forecast {
			return delivery.Forecast{
				ID: r.ID("fc"), SetID: set, Plan: plan, PlanVersion: 2, Milestone: milestone, Status: delivery.Forecasted, Rev: rev,
				Seed: delivery.ForecastSeed("sha256:plan", rev), Trials: 2000, Cap: 3, AsOf: at - 1000, PersonMedianMs: 60_000, PolicyVersion: 1,
				EndP50: end, EndP85: end + 3_600_000, CostP50USD: 2.5, CostP85USD: 4, EndP85CapPlusOne: end + 1_800_000, EndP85PersonMedian: end + 600_000,
				Confidence: delivery.Low, LowConfidenceWorkOrders: []string{"wo-1"}, LeftOut: []string{}, Outside: []string{}, LowerBound: delivery.LowerP85,
				Explanation: fmt.Sprintf("Low confidence, from 2000 trials, at revision %d.", rev), Inputs: "sha256:inputs", Day: delivery.DayOf(at), CreatedAt: at,
			}
		}
		return []delivery.Forecast{forecast(""), forecast("m1")}
	}
	morning, evening, next := run(dayOne+3_600_000, 10, dayOne+86_400_000), run(dayOne+72_000_000, 12, dayOne+90_000_000), run(dayOne+90_000_000, 15, dayOne+95_000_000)
	before, err := r.workspaceRev(s)
	if err != nil {
		return err
	}
	for _, kept := range [][]delivery.Forecast{morning, evening, next} {
		if err := r.keepForecasts(s, kept); err != nil {
			return err
		}
	}
	if after, err := r.workspaceRev(s); err != nil || after != before {
		return Violatedf("keeping forecasts moved the revision from %d to %d: %v", before, after, err)
	}
	if _, err := CheckRevisionFirst(r.trace.Entries()); err != nil {
		return err
	}
	latest, own, of, err := r.forecastsOf(s, plan, 2, "m1")
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(latest, next) {
		return Violatedf("the latest forecasts read %+v, kept %+v", latest, next)
	}
	if !reflect.DeepEqual(own, []delivery.Forecast{evening[0], next[0]}) || !reflect.DeepEqual(of, []delivery.Forecast{evening[1], next[1]}) {
		return Violatedf("the plan's days read %+v, and the milestone's %+v", own, of)
	}
	for _, other := range []struct {
		plan    string
		version int64
	}{{plan, 1}, {r.ID("plan"), 2}} {
		if latest, own, _, err := r.forecastsOf(s, other.plan, other.version, "m1"); err != nil || len(latest) != 0 || len(own) != 0 {
			return Violatedf("plan %s version %d, which kept none, reads %+v and %+v: %v", other.plan, other.version, latest, own, err)
		}
	}
	return nil
}

// registerWorker registers a worker with the installation, which then serves its projects.
func (r *Rig) registerWorker(s ports.Store) error {
	plane := control.NewWorkers(s, r.Scope, control.WorkerOptions{HeartbeatMs: statusHeartbeatMs, LeaseTTLMs: TTLMs})
	token, err := plane.CreateJoinToken(r.Ctx, ledger.Actor{Kind: ledger.ActorMember, ID: "member"})
	if err != nil {
		return err
	}
	_, err = plane.Register(r.Ctx, ledger.Registration{JoinToken: token, WorkerID: r.ID("worker"), Name: "w"})
	return err
}

// forecastRuns runs the forecast tick once and says how many plan versions it forecast.
func (r *Rig) forecastRuns(s ports.Store, want int, after string) error {
	if kept, err := control.NewForecasts(s).Refresh(r.Ctx, r.Scope); err != nil || kept != want {
		return Violatedf("%s, the forecast tick forecast %d plan versions, not %d: %v", after, kept, want, err)
	}
	return nil
}

func checkForecastTick(r *Rig) error {
	s := r.Store()
	files := newApprovalSource()
	files.source.Write("m1.md", []byte(milestoneOf("m1", approvalPlan, 1)))
	files.source.Write("budget.md", []byte(placedContract{ID: "budget", Plan: approvalPlan, Milestone: "m1"}.text()))
	if report, err := r.intake(s, files.source); err != nil || len(report.Problems) != 0 {
		return Violatedf("intake read %+v: %v", report, err)
	}
	plan, err := r.planID(s, "Retry budget")
	if err != nil {
		return err
	}
	versions, err := r.planVersionsOf(s, plan)
	if err != nil || len(versions) != 1 {
		return Violatedf("versions %+v: %v", versions, err)
	}
	budget, held, err := r.contractTitled(s, "budget")
	if err != nil || !held {
		return Violatedf("the contract: %v", err)
	}
	planned, err := r.ordersOfContract(s, budget.ID)
	if err != nil || len(planned) != 1 {
		return Violatedf("the plan's work %v: %v", planned, err)
	}
	_, intakeSet, err := r.currentOf(s, planned[0])
	if err != nil {
		return err
	}

	// With no worker, nothing can run the plan's work.
	before, err := r.workspaceRev(s)
	if err != nil {
		return err
	}
	if err := r.forecastRuns(s, 1, "after intake"); err != nil {
		return err
	}
	if after, err := r.workspaceRev(s); err != nil || after != before {
		return Violatedf("the forecast tick moved the revision from %d to %d: %v", before, after, err)
	}
	latest, _, _, err := r.forecastsOf(s, plan, 1, "m1")
	if err != nil {
		return err
	}
	if len(latest) != 2 || latest[0].Milestone != "" || latest[1].Milestone != "m1" || latest[0].Status != delivery.ForecastNoCapacity ||
		latest[0].SetID != intakeSet.ID || latest[1].SetID != intakeSet.ID || latest[0].PlanVersion != 1 || latest[0].Rev != before ||
		latest[0].Inputs == "" || latest[0].ID == "" || latest[0].Day != delivery.DayOf(latest[0].CreatedAt) || latest[0].Explanation == "" {
		return Violatedf("with no worker, the plan's forecasts are %+v", latest)
	}
	if err := r.forecastRuns(s, 0, "with nothing new"); err != nil {
		return err
	}

	// A worker serves the project, and no work like the plan's has finished.
	if err := r.registerWorker(s); err != nil {
		return err
	}
	if err := r.forecastRuns(s, 1, "once a worker serves the project"); err != nil {
		return err
	}
	if latest, _, _, err = r.forecastsOf(s, plan, 1, "m1"); err != nil || latest[0].Status != delivery.ForecastNoHistory || latest[1].Status != delivery.ForecastNoHistory {
		return Violatedf("with no history, the plan's forecasts are %+v: %v", latest, err)
	}

	// Work like the plan's finishes, and once the fold keeps its record, the forecast has dates, from a set of the
	// version the tick keeps itself.
	finished, err := r.Awaiting(s, []string{criterionOne}, LaneFinished(r.ID("commit"), LaneCheck(criterionOne, true)))
	if err != nil {
		return err
	}
	if _, err := r.NewOps(s).Accept(r.Ctx, r.Scope, finished, r.Actor); err != nil {
		return err
	}
	if _, err := control.NewDeliveryFold(s).Fold(r.Ctx, r.Scope); err != nil {
		return err
	}
	if err := r.forecastRuns(s, 1, "after work finished"); err != nil {
		return err
	}
	rev, err := r.workspaceRev(s)
	if err != nil {
		return err
	}
	latest, own, _, err := r.forecastsOf(s, plan, 1, "m1")
	if err != nil {
		return err
	}
	made, set, err := r.currentOf(s, planned[0])
	if err != nil {
		return err
	}
	f := latest[0]
	if f.Status != delivery.Forecasted || f.Confidence != delivery.Low || len(f.LowConfidenceWorkOrders) != 1 || f.LowConfidenceWorkOrders[0] != planned[0] ||
		f.Seed != delivery.ForecastSeed(versions[0].Digest, rev) || f.Rev != rev || f.EndP50 < f.AsOf || f.EndP85 < f.EndP50 || f.Trials != 2000 {
		return Violatedf("after work finished, the plan's forecast is %+v", f)
	}
	if set.ID != f.SetID || set.ID == intakeSet.ID || set.Plan != plan || set.PlanVersion != 1 || made.Status != delivery.Estimated || latest[1].SetID != f.SetID {
		return Violatedf("the forecast names set %s, and the plan's work is estimated %+v in %+v", f.SetID, made, set)
	}
	if len(own) != 1 || own[0].ID != f.ID {
		return Violatedf("a day keeps %+v of the plan's forecasts", own)
	}
	if err := r.forecastRuns(s, 0, "a second time with nothing new"); err != nil {
		return err
	}

	// Approving the version forecasts it again.
	pending, err := r.theOpenPlanApprovalFor(s, "Retry budget")
	if err != nil {
		return err
	}
	if _, err := r.approve(s, pending, true, r.Actor); err != nil {
		return err
	}
	if err := r.forecastRuns(s, 1, "after the version was approved"); err != nil {
		return err
	}

	// Once the plan's work starts, the forecast keeps what remains of it.
	if _, err := r.Running(s, planned[0], "a"); err != nil {
		return err
	}
	if err := r.forecastRuns(s, 1, "after the plan's work started"); err != nil {
		return err
	}
	if latest, _, _, err = r.forecastsOf(s, plan, 1, "m1"); err != nil || !strings.Contains(latest[0].Explanation, "leaves started work as it is") {
		return Violatedf("with its work started, the plan's forecast is %+v: %v", latest, err)
	}
	return nil
}
