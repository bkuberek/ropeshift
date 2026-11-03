import type { Decision, Decisions as View } from "../api/types";
import { addressOf, Link } from "../app/route";
import { Fact, Line, words } from "../parts/parts";

const decisionAt = (id: string) => addressOf("/decisions", { id });

function Lineage({ decision }: { decision: Decision }) {
  return (
    <ol className="lineage" aria-label="Lineage">
      {decision.lineage.map((id) => (
        <li key={id}>
          {id === decision.id ? (
            <span className="mono" aria-current="true">
              {id}
            </span>
          ) : (
            <Link className="mono" to={decisionAt(id)}>
              {id}
            </Link>
          )}
        </li>
      ))}
    </ol>
  );
}

function Linked({ ids }: { ids: string[] }) {
  return (
    <ul className="links">
      {ids.map((id) => (
        <li key={id}>
          <Link className="mono" to={decisionAt(id)}>
            {id}
          </Link>
        </li>
      ))}
    </ul>
  );
}

function DecisionLine({ decision }: { decision: Decision }) {
  return (
    <Line side={<span className="wait-word">{words(decision.status)}</span>} kind={<span className="mono">{decision.id}</span>} title={decision.statement} plain>
      <div className="card-body">
        {decision.rationale ? <p className="lead">{decision.rationale}</p> : null}
        {decision.lineage.length > 1 ? <Lineage decision={decision} /> : null}
        <dl className="facts">
          {decision.supersedes ? (
            <Fact name="Replaces">
              <Linked ids={[decision.supersedes]} />
            </Fact>
          ) : null}
          {decision.superseded_by ? (
            <Fact name="Replaced by">
              <Linked ids={[decision.superseded_by]} />
            </Fact>
          ) : null}
          {decision.scope ? <Fact name="Scope">{decision.scope}</Fact> : null}
          {decision.reversibility ? <Fact name="Reversibility">{words(decision.reversibility)}</Fact> : null}
          {decision.categories?.length ? <Fact name="Categories">{decision.categories.map((name) => words(name)).join(", ")}</Fact> : null}
          {decision.decided_by ? (
            <Fact name="Decided by" mono>
              {decision.decided_by}
            </Fact>
          ) : null}
          {decision.policy_version ? <Fact name="Policy version">{decision.policy_version}</Fact> : null}
          {decision.question_id ? (
            <Fact name="Question" mono>
              {decision.question_id}
            </Fact>
          ) : null}
          {decision.citations?.length ? (
            <Fact name="Cites" mono>
              {decision.citations.join(", ")}
            </Fact>
          ) : null}
          {decision.overlaps?.length ? (
            <Fact name="Overlaps">
              <Linked ids={decision.overlaps} />
            </Fact>
          ) : null}
          {decision.no_conflict?.length ? (
            <Fact name="Judged compatible with">
              <Linked ids={decision.no_conflict} />
            </Fact>
          ) : null}
        </dl>
      </div>
    </Line>
  );
}

export function Decisions({ view, selected }: { view: View; selected: string }) {
  const shown = selected ? view.decisions.filter((decision) => decision.id === selected) : view.decisions;
  if (view.decisions.length === 0) {
    return <p className="empty">No decisions yet.</p>;
  }
  return (
    <>
      {selected ? (
        <p className="back">
          <Link to="/decisions">All decisions</Link>
        </p>
      ) : null}
      {shown.length ? (
        <ol className="queue queue-work" role="list">
          {shown.map((decision) => (
            <DecisionLine key={decision.id} decision={decision} />
          ))}
        </ol>
      ) : null}
    </>
  );
}
