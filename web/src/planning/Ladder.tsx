import type { Attachment, LadderStep } from "../api/types";
import { addressOf, Link } from "../app/route";

// outcomeWords says what the core recorded on a rung no orchestrator worked. Any other outcome is the words an
// orchestrator gave it.
const outcomeWords: Record<string, string> = {
  unjudged: "Found by text, judged by nobody",
  no_orchestrator: "No orchestrator worked this rung",
};

// judgeOf names who worked a rung or recorded a decision: an orchestrator by the machine it runs on, which its worker's
// ID does not say, and anyone else by ID.
export const judgeOf = (id: string, machines?: Record<string, string>) => (machines?.[id] ? `Orchestrator on ${machines[id]}` : id);

// Ladder is a question's ladder trace so far (FR-QD-13): the lanes that asked it or wait on it, then each rung it
// passed, who worked it, what happened there, and what was considered. A rung that decided, assumed, or answered from
// the record names its decision, with why it was made and what it cites.
export function Ladder({ steps, attached, machines }: { steps?: LadderStep[]; attached?: Attachment[]; machines?: Record<string, string> }) {
  if (!steps?.length && !attached?.length) {
    return null;
  }
  return (
    <details className="older">
      <summary>How it got here</summary>
      {attached?.length ? (
        <ul className="list-rows" role="list" aria-label="Lanes on this question">
          {attached.map((lane) => (
            <li key={`${lane.lane}:${lane.generation}`} className="list-row">
              <p>
                <Link className="mono" to={addressOf("/lane", { id: lane.lane })}>
                  {lane.lane}
                </Link>{" "}
                <span className="muted">
                  generation {lane.generation}
                  {lane.blocking ? ", blocking" : ""}
                </span>
              </p>
            </li>
          ))}
        </ul>
      ) : null}
      {steps?.length ? (
        <ol className="list-rows" role="list" aria-label="Rungs it passed">
          {steps.map((step, at) => (
            <li key={`${at}:${step.rung}`} className="list-row">
              <p>
                Rung {step.rung}
                {step.by ? <span className="muted"> · {judgeOf(step.by, machines)}</span> : null}
              </p>
              {step.outcome ? <p className="muted">{outcomeWords[step.outcome] ?? step.outcome}</p> : null}
              {step.rationale ? <p>{step.rationale}</p> : null}
              {step.candidates?.length ? (
                <p className="muted">
                  {step.decision ? "Cites" : "Considered"} <span className="mono">{step.candidates.join(", ")}</span>
                </p>
              ) : null}
              {step.decision ? (
                <p>
                  <Link className="mono" to={addressOf("/decisions", { id: step.decision })}>
                    {step.decision}
                  </Link>
                </p>
              ) : null}
            </li>
          ))}
        </ol>
      ) : null}
    </details>
  );
}
