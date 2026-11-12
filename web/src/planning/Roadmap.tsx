import { useId } from "react";

import { readView } from "../api/client";
import type { GoalProgress, MilestoneProgress, PlannedWork, PlanProgress, Plans, Roadmap as View } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { addressOf, Link } from "../app/route";
import { milestoneStates, stateCounts, workLabel } from "../floor/states";
import { Bar, Page, plural, StampLine, useClock, ViewTime } from "../parts/parts";
import { Banner, EmptyState, Id, SkeletonRows, StateBadge, WorkBadge } from "../ui/display";
import { layersByMilestone, noMilestone, waitingVersions } from "./roadmap";
import { StartSession } from "./StartSession";

// StateCounts says how a plan's work stands, state by state.
export function StateCounts({ states }: { states: Record<string, number> }) {
  return (
    <ul className="chips" aria-label="Work by state">
      {stateCounts(states).map(([state, count]) => (
        <li key={state}>
          {workLabel(state)} {count}
        </li>
      ))}
    </ul>
  );
}

// One milestone's work by dependency layer: a column for each layer of the plan that holds some of its work,
// a compact card for each work order, and the critical path outlined, and named in words beside the outline.
function Layers({ plan, milestone, title }: { plan: PlanProgress; milestone: string; title: string }) {
  const layers = layersByMilestone(plan, milestone);
  const heading = useId();
  if (layers.length === 0) {
    return null;
  }
  const critical = layers.flatMap((layer) => layer.work).filter((work) => work.critical).length;
  return (
    <section className="panel" aria-labelledby={heading}>
      <div className="panel-head">
        <h3 id={heading}>{title} · work by dependency layer</h3>
        {critical ? (
          <span className="small muted row-tight">
            <span className="wo-card critical legend" aria-hidden="true">
              &nbsp;
            </span>
            critical path, {plural(critical, "work order")}
          </span>
        ) : null}
      </div>
      <div className="panel-body">
        <ol className="layers" aria-label={`Work of ${title} by dependency layer`}>
          {layers.map((layer) => (
            <li key={layer.layer} className="layer">
              <p className="overline">Layer {layer.layer}</p>
              <ul className="layer-cards">
                {layer.work.map((work) => (
                  <li key={work.id}>
                    <Link className={work.critical ? "wo-card critical" : "wo-card"} to={addressOf("/work-order", { id: work.id })}>
                      <Id value={work.id} />
                      <span className="wo-title">{work.title || work.id}</span>
                      <span className="row-tight">
                        <WorkBadge state={work.state} />
                        {work.critical ? <span className="small">On the critical path</span> : null}
                      </span>
                    </Link>
                  </li>
                ))}
              </ul>
            </li>
          ))}
        </ol>
      </div>
    </section>
  );
}

function progressWords(milestone: MilestoneProgress, work: PlannedWork[]): string {
  if (milestone.total === 0) {
    return milestone.existing ? "Delivered by existing code" : "No work yet";
  }
  const counts: Record<string, number> = {};
  for (const order of work) {
    counts[order.state] = (counts[order.state] ?? 0) + 1;
  }
  const rest = stateCounts(counts)
    .filter(([state]) => state !== "done")
    .map(([state, count]) => `${count} ${workLabel(state).toLowerCase()}`);
  return [`${milestone.done} of ${milestone.total} done`, ...rest].join(" · ");
}

function MilestoneRow({ milestone, work, titleOf }: { milestone: MilestoneProgress; work: PlannedWork[]; titleOf: (id: string) => string }) {
  const title = milestone.title || milestone.id;
  const said = progressWords(milestone, work);
  return (
    <li className="milestone-row">
      <div>
        <p className="item-title">{title}</p>
        <p className="small muted">
          Priority {milestone.priority}
          {milestone.depends_on.length ? ` · After ${milestone.depends_on.map(titleOf).join(", ")}` : ""}
        </p>
        {milestone.exit_criteria.length ? (
          <ul className="criteria small" aria-label="Exit criteria">
            {milestone.exit_criteria.map((criterion) => (
              <li key={criterion.id}>{criterion.text}</li>
            ))}
          </ul>
        ) : null}
      </div>
      <div className="stack-8">
        {milestone.total === 0 ? null : <Bar done={milestone.done} total={milestone.total} label={`${title}: ${milestone.done} of ${milestone.total} done`} />}
        <span className="small muted">{said}</span>
      </div>
      <StateBadge table={milestoneStates} state={milestone.state} />
    </li>
  );
}

// versionOf says which version of a plan is in force and which one waits.
function versionOf(version: { approved: number; pending: number }): string {
  return `${version.approved ? `Version ${version.approved} approved` : "No version approved yet"}${version.pending ? ` · version ${version.pending} waits` : ""}`;
}

// PlanBlock is one plan under its goal: its milestones in priority order, a line of notes on the critical path,
// and each milestone's work by dependency layer. A plan of one work order and no milestone keeps to its line.
function PlanBlock({ row, version, titled }: { row: PlanProgress; version: { approved: number; pending: number }; titled: boolean }) {
  const titles = new Map(row.milestones.map((milestone) => [milestone.id, milestone.title || milestone.id]));
  const said = `${row.done} of ${row.total} done`;
  const loose = row.work.filter((work) => !work.milestone || !titles.has(work.milestone));
  const plain = row.milestones.length === 0;
  return (
    <>
      {titled || plain ? (
        <div className="panel-body stack">
          <div className="plan-line">
            <h3 className="plan-title">
              <Link to={addressOf("/plans", { plan: row.plan.id })}>{row.plan.title}</Link>
            </h3>
            <span className="small muted">{versionOf(version)}</span>
            {row.total === 0 ? <span className="small muted">No work yet</span> : <span className="small muted">{said}</span>}
          </div>
          {row.total === 0 ? null : <Bar done={row.done} total={row.total} label={`${row.plan.title}: ${said}`} />}
          <StateCounts states={row.states} />
        </div>
      ) : null}
      {row.milestones.length ? (
        <ol className="milestones" aria-label="Milestones">
          {row.milestones.map((milestone) => (
            <MilestoneRow key={milestone.id} milestone={milestone} work={row.work.filter((work) => work.milestone === milestone.id)} titleOf={(id) => titles.get(id) ?? id} />
          ))}
        </ol>
      ) : null}
      <div className="panel-body stack">
        {row.milestones.map((milestone) => (
          <Layers key={milestone.id} plan={row} milestone={milestone.id} title={milestone.title || milestone.id} />
        ))}
        {loose.length >= 2 || (loose.length > 0 && !plain) ? <Layers plan={row} milestone={noMilestone} title={plain ? row.plan.title : "Work in no milestone"} /> : null}
        {row.critical_path.work_orders.length ? (
          <div>
            <p className="muted">
              {plural(row.critical_path.work_orders.length, "work order")}, {plural(row.critical_path.weight, "size point")} left on the critical path
            </p>
            <p className="muted small">A size point is what a work order's size counts for: small 1, medium 2, large 3.</p>
          </div>
        ) : row.work.length > 1 ? (
          <p className="muted">Nothing is left on the critical path.</p>
        ) : null}
      </div>
    </>
  );
}

function Goal({ group, versions }: { group: GoalProgress; versions: Map<string, { approved: number; pending: number }> }) {
  const heading = useId();
  const one = group.plans.length === 1 ? group.plans[0] : undefined;
  const version = (row: PlanProgress) => versions.get(row.plan.id) ?? { approved: 0, pending: 0 };
  return (
    <section className="panel" aria-labelledby={heading}>
      <div className="panel-head">
        <h2 id={heading}>{group.goal?.title ?? "Under no goal"}</h2>
        {one && one.milestones.length ? (
          <span className="subtle">
            Plan <Link to={addressOf("/plans", { plan: one.plan.id })}>{one.plan.title}</Link> · {versionOf(version(one))}
          </span>
        ) : null}
      </div>
      {group.goal?.statement || group.goal?.success_measures?.length ? (
        <div className="panel-body stack">
          {group.goal.statement ? <p className="lead">{group.goal.statement}</p> : null}
          {group.goal.success_measures?.length ? (
            <ul className="measures" aria-label="Success measures">
              {group.goal.success_measures.map((measure) => (
                <li key={measure}>{measure}</li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}
      {group.plans.map((row) => (
        <PlanBlock key={row.plan.id} row={row} version={version(row)} titled={group.plans.length > 1} />
      ))}
    </section>
  );
}

// Roadmap is the project's goals, each with its plans and their versions, the milestones of each plan in
// priority order with how far each has got, and each milestone's work by dependency layer.
export function Roadmap({ view, plans, project }: { view: View; plans: Plans | null; project: string }) {
  const versions = new Map((plans?.plans ?? []).map((row) => [row.plan.id, { approved: row.approved_version ?? 0, pending: row.pending_version ?? 0 }]));
  const waiting = waitingVersions(plans?.plans ?? []);
  return (
    <>
      {waiting.map((row) => (
        <Banner
          key={row.id}
          tone="warning"
          icon="plan"
          action={
            <Link className="btn btn-sm" to={addressOf("/plan", { id: row.id, version: String(row.version) })}>
              Review version {row.version}
            </Link>
          }
        >
          <strong>
            {row.title}, version {row.version}, waits for your approval.
          </strong>{" "}
          {row.approved ? `Version ${row.approved} stays in force until you decide.` : "Nothing is approved yet."}
        </Banner>
      ))}
      {view.goals.length === 0 ? (
        <div className="panel">
          <EmptyState
            icon="roadmap"
            title="No goals or plans yet"
            action={
              <div className="row-tight">
                <StartSession project={project} variant="primary" />
                <Link className="btn" to="/import" installation>
                  Import an existing project
                </Link>
              </div>
            }
          >
            A plan gives the roadmap its goals and milestones. Start a planning session to write one, or import a project that already exists.
          </EmptyState>
        </div>
      ) : (
        view.goals.map((group) => <Goal key={group.goal?.id ?? "no-goal"} group={group} versions={versions} />)
      )}
    </>
  );
}

// RoadmapPage reads the roadmap and the plans view, which names the versions that wait and the ones in force.
export function RoadmapPage({ live, project }: { live: Live; project: string }) {
  const roadmap = useView<View>(() => readView<View>("roadmap"), "roadmap", live);
  const plans = useView<Plans>(() => readView<Plans>("plans"), "roadmap:plans", live);
  const now = useClock();
  const failure = roadmap.failure || plans.failure;
  return (
    <Page title="Roadmap" note="Goals, plans, and milestones in priority order, with the critical path." actions={<StartSession project={project} />}>
      {failure ? <Banner tone="danger">{roadmap.view ? `What this view shows is not current. ${failure}` : failure}</Banner> : null}
      {roadmap.view ? (
        <ViewTime.Provider value={{ asOf: roadmap.view.as_of, elapsed: Math.max(0, now - roadmap.readAt) }}>
          <div className="view-body stack" aria-busy={roadmap.busy}>
            <Roadmap view={roadmap.view} plans={plans.view} project={project} />
          </div>
          <StampLine stamp={roadmap.view} />
        </ViewTime.Provider>
      ) : null}
      {!roadmap.view && !failure ? (
        <div className="panel">
          <SkeletonRows rows={5} />
        </div>
      ) : null}
    </Page>
  );
}
