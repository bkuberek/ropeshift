import type { PlanRevisionView } from "../api/types";
import { Id } from "../ui/display";

// RevisionSince lists the decisions a member made since the version a member approved, with the finished work the
// reversible ones reached and the work that waits in contract review while the revision is open. A plan version is
// approved beside it, and a plan that is not yet approved again says why its unstarted work waits.
export function RevisionSince({ revision }: { revision: PlanRevisionView }) {
  const base = revision.revision?.approved_version ?? 0;
  const open = revision.revision?.closed_at === undefined;
  return (
    <>
      {open && revision.waiting.length ? (
        <p>
          {revision.waiting.length} work {revision.waiting.length === 1 ? "order waits" : "orders wait"} in contract review until a member confirms version {base} or approves a later one.
        </p>
      ) : null}
      <ol className="drafts" aria-label={`Decisions since version ${base}`}>
        {revision.decisions.map((decision) => (
          <li key={decision.id} className="draft">
            <p className="question-text">{decision.statement}</p>
            <p className="muted">
              {decision.reversibility === "one_way" ? "One-way" : "Reversible"}
              {decision.scope ? `, binds ${decision.scope}` : ""}
              {" · "}
              <Id value={decision.id} />
            </p>
          </li>
        ))}
      </ol>
      {revision.finished.length ? (
        <ul className="links" aria-label="Finished work the decisions reached">
          {revision.finished.map((work) => (
            <li key={work.work_order}>
              {work.title || work.work_order} <span className="muted">(decisions {work.decisions.join(", ")})</span>
            </li>
          ))}
        </ul>
      ) : null}
    </>
  );
}
