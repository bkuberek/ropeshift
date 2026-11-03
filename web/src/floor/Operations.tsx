import type { Operations as View, SourceBinding } from "../api/types";
import { Link } from "../app/route";
import { Fact, Section, words } from "../parts/parts";
import { everyCount, laneLabel, laneStates, workLabel, workStates } from "./states";

const cap = (limit: number) => (limit === 0 ? "No limit" : String(limit));

function Counts({ caption, rows, label }: { caption: string; rows: [string, number][]; label: (state: string) => string }) {
  return (
    <table className="counts">
      <caption>{caption}</caption>
      <tbody>
        {rows.map(([state, count]) => (
          <tr key={state} className={count === 0 ? "count-none" : undefined}>
            <th scope="row">{label(state)}</th>
            <td>{count}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function Source({ source }: { source: SourceBinding }) {
  return (
    <li className="row">
      <p className="row-title">{source.adapter}</p>
      <ul className="tags">
        <li className="tag-strong">{words(source.health)}</li>
        <li>{words(source.port)}</li>
        <li className="mono">{source.instance}</li>
      </ul>
      {source.detail ? <p className="mono quiet">{source.detail}</p> : null}
      {source.problems?.length ? (
        <ul className="criteria" aria-label="What it could not read">
          {source.problems.map((problem) => (
            <li key={problem.path}>
              <span className="mono">{problem.path}</span> {problem.detail}
            </li>
          ))}
        </ul>
      ) : null}
    </li>
  );
}

export function Operations({ view }: { view: View }) {
  const { fleet } = view;
  return (
    <>
      <Section title="Fleet">
        <dl className="facts">
          <Fact name="Mode">{words(fleet.fleet_mode)}</Fact>
          <Fact name="Waiting for a person">
            <Link to="/">{view.waiting_for_a_person}</Link>
          </Fact>
          <Fact name="Workers">
            {view.workers_fresh} of {view.workers} observed
          </Fact>
          <Fact name="Work in progress">{cap(fleet.wip_limit)}</Fact>
          <Fact name="Lanes per repository">{cap(fleet.lanes_per_repository)}</Fact>
          <Fact name="Lanes per worker">{cap(fleet.lanes_per_worker)}</Fact>
        </dl>
      </Section>
      <div className="tables">
        <Counts caption="Work orders by state" rows={everyCount(workStates, view.work_orders)} label={workLabel} />
        <Counts caption="Lanes by state" rows={everyCount(laneStates, view.lanes)} label={laneLabel} />
      </div>
      <Section title="Sources">
        {view.sources.length ? (
          <ul className="rows">
            {view.sources.map((source) => (
              <Source key={source.id} source={source} />
            ))}
          </ul>
        ) : (
          <p className="quiet">No source is bound.</p>
        )}
      </Section>
      <Section title="Product measures">
        <p className="quiet">Product measures are not recorded yet, so this page shows the fleet's health and nothing more.</p>
      </Section>
    </>
  );
}
