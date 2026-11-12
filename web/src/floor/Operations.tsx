import type { ForgeLine, Operations as View, SourceBinding } from "../api/types";
import { Link } from "../app/route";
import { Fact, Id, Section, words } from "../parts/parts";
import { Measures, ReviewAgreement } from "./Measures";
import { everyCount, laneLabel, laneStates, workLabel, workStates } from "./states";

const cap = (limit: number) => (limit === 0 ? "No limit" : String(limit));

function Counts({ caption, rows, label }: { caption: string; rows: [string, number][]; label: (state: string) => string }) {
  return (
    <table className="table counts">
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
    <li className="list-row">
      <p className="list-row-title">{source.adapter}</p>
      <ul className="chips">
        <li className="chip-strong">{words(source.health)}</li>
        <li>{words(source.port)}</li>
        <li>
          <Id value={source.instance} />
        </li>
      </ul>
      {source.detail ? <p className="mono muted">{source.detail}</p> : null}
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

// Forge is one worker's gh sign-in for one repository: work that needs the forge waits while it cannot run.
function Forge({ line }: { line: ForgeLine }) {
  return (
    <li className="list-row">
      <p className="list-row-title">{line.repository_name || line.repository}</p>
      <ul className="chips">
        <li className="chip-strong">{words(line.state)}</li>
        <li>{line.worker_name || line.worker}</li>
      </ul>
      {line.detail ? <p className="mono muted">{line.detail}</p> : null}
    </li>
  );
}

export function Operations({ view }: { view: View }) {
  const { fleet } = view;
  return (
    <>
      <Section title="Fleet">
        <dl className="kv">
          <Fact name="Mode">{words(fleet.fleet_mode)}</Fact>
          <Fact name="Waiting for a person">
            <Link to="/needs-you">{view.waiting_for_a_person}</Link>
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
      <p className="muted tables-note">
        Lanes count by each lane's last state, fresh or not, and <Link to="/floor">the floor</Link> says which is which.
      </p>
      <Section title="Sources">
        {view.sources.length ? (
          <ul className="list-rows">
            {view.sources.map((source) => (
              <Source key={source.id} source={source} />
            ))}
          </ul>
        ) : (
          <p className="muted">No source is bound.</p>
        )}
      </Section>
      {view.forges?.length ? (
        <Section title="Forge sign-in">
          <ul className="list-rows">
            {view.forges.map((line) => (
              <Forge key={line.worker + "/" + line.repository} line={line} />
            ))}
          </ul>
        </Section>
      ) : null}
      <Measures report={view.measures} />
      <ReviewAgreement report={view.measures} />
    </>
  );
}
