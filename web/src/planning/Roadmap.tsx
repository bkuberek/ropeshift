import type { GoalProgress, PlanProgress, Roadmap as View } from "../api/types";
import { addressOf, Link } from "../app/route";
import { stateCounts, workLabel } from "../floor/states";
import { Section } from "../parts/parts";

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
