import { useId } from "react";
import type { ReactNode } from "react";

import type { GoalProgress, MilestoneProgress, PlannedWork, PlanProgress, Roadmap as View } from "../api/types";
import { addressOf, Link } from "../app/route";
import { stateCounts, workLabel } from "../floor/states";
import { Section, words } from "../parts/parts";

// StateCounts says how a plan's work stands, state by state.
export function StateCounts({ states }: { states: Record<string, number> }) {
  return (
    <ul className="tags" aria-label="Work by state">
      {stateCounts(states).map(([state, count]) => (
        <li key={state}>
          {workLabel(state)} {count}
        </li>
      ))}
    </ul>
  );
}

const milestoneWords: Record<string, string> = {
  not_started: "Not started",
  waiting: "Waiting",
  in_progress: "In progress",
  done: "Done",
};

// Part is one labelled list of a plan's map. Its heading names the list, so a screen reader finds each
// list by what it holds.
function Part({ title, className, children }: { title: string; className: string; children: ReactNode }) {
  const heading = useId();
  return (
    <div className="plan-part">
      <h3 className="plan-part-title" id={heading}>
        {title}
      </h3>
      <ol className={className} aria-labelledby={heading}>
        {children}
      </ol>
    </div>
  );
}

function Milestone({ milestone, titleOf }: { milestone: MilestoneProgress; titleOf: (id: string) => string }) {
  const title = milestone.title || milestone.id;
  const said = `${milestone.done} of ${milestone.total} done`;
  return (
    <li className={`milestone milestone-${milestone.state}`}>
      <p className="milestone-title">{title}</p>
      <ul className="tags">
        <li className="tag-strong">{milestoneWords[milestone.state] ?? words(milestone.state)}</li>
        <li>{milestone.total === 0 ? "No work yet" : said}</li>
      </ul>
      {milestone.total === 0 ? null : <progress value={milestone.done} max={milestone.total} aria-label={`${title}: ${said}`} />}
      {milestone.depends_on.length ? <p className="quiet">After {milestone.depends_on.map(titleOf).join(", ")}</p> : null}
      {milestone.exit_criteria.length ? (
        <ul className="criteria" aria-label="Exit criteria">
          {milestone.exit_criteria.map((criterion) => (
            <li key={criterion.id}>{criterion.text}</li>
          ))}
        </ul>
      ) : null}
    </li>
  );
}

// The critical path is marked in words beside its color, so it reads the same to every person.
function LayerWork({ work, critical }: { work: PlannedWork; critical: boolean }) {
  return (
    <li className={critical ? "layer-work layer-work-critical" : "layer-work"}>
      <span className="layer-work-title">{work.title || work.id}</span>
      <ul className="tags">
        <li>{workLabel(work.state)}</li>
        {critical ? <li className="tag-critical">On the critical path</li> : null}
      </ul>
    </li>
  );
}

// PlanMap lays out how a plan's work runs: its milestones in priority order, the work that can run at the
// same time, and the heaviest chain of what is left. A plan of one work order has nothing to lay out.
function PlanMap({ row }: { row: PlanProgress }) {
  if (row.milestones.length === 0 && row.work.length < 2) {
    return null;
  }
  const milestones = new Map(row.milestones.map((milestone) => [milestone.id, milestone.title || milestone.id]));
  const work = new Map(row.work.map((order) => [order.id, order]));
  const workOf = (id: string): PlannedWork => work.get(id) ?? { id, title: id, state: "" };
  const critical = new Set(row.critical_path.work_orders);
  return (
    <div className="plan-map">
      {row.milestones.length ? (
        <Part title="Milestones" className="milestones">
          {row.milestones.map((milestone) => (
            <Milestone key={milestone.id} milestone={milestone} titleOf={(id) => milestones.get(id) ?? id} />
          ))}
        </Part>
      ) : null}
      {row.layers.length ? (
        <Part title="Work that can run at the same time" className="layers">
          {row.layers.map((layer, index) => (
            <li key={index} className="layer">
              <p className="layer-title">Step {index + 1}</p>
              <ul className="layer-works">
                {layer.map((id) => (
                  <LayerWork key={id} work={workOf(id)} critical={critical.has(id)} />
                ))}
              </ul>
            </li>
          ))}
        </Part>
      ) : null}
      {row.critical_path.work_orders.length ? (
        <>
          <Part title="Critical path" className="path">
            {row.critical_path.work_orders.map((id) => (
              <li key={id}>{workOf(id).title || id}</li>
            ))}
          </Part>
          <p className="quiet">{row.critical_path.weight} left on the critical path</p>
        </>
      ) : (
        <p className="quiet">Nothing is left on the critical path.</p>
      )}
    </div>
  );
}

function PlanRow({ row }: { row: PlanProgress }) {
  const said = `${row.done} of ${row.total} done`;
  return (
    <li className="row">
      <Link className="row-title" to={addressOf("/plans", { plan: row.plan.id })}>
        {row.plan.title}
      </Link>
      {row.total === 0 ? (
        <p className="quiet">No work yet</p>
      ) : (
        <div className="row-progress">
          <progress value={row.done} max={row.total} aria-label={`${row.plan.title}: ${said}`} />
          <p className="quiet">{said}</p>
        </div>
      )}
      <StateCounts states={row.states} />
      <PlanMap row={row} />
    </li>
  );
}

function Goal({ group }: { group: GoalProgress }) {
  return (
    <Section title={group.goal?.title ?? "Under no goal"}>
      {group.goal?.statement ? <p className="lead">{group.goal.statement}</p> : null}
      {group.goal?.success_measures?.length ? (
        <ul className="measures" aria-label="Success measures">
          {group.goal.success_measures.map((measure) => (
            <li key={measure}>{measure}</li>
          ))}
        </ul>
      ) : null}
      <ol className="rows" role="list">
        {group.plans.map((row) => (
          <PlanRow key={row.plan.id} row={row} />
        ))}
      </ol>
    </Section>
  );
}

export function Roadmap({ view }: { view: View }) {
  if (view.goals.length === 0) {
    return <p className="empty">No goals or plans yet.</p>;
  }
  return (
    <>
      {view.goals.map((group) => (
        <Goal key={group.goal?.id ?? "no-goal"} group={group} />
      ))}
    </>
  );
}
