import type { LadderStep } from "../api/types";

// outcomeWords says what the core recorded on a rung no orchestrator worked. Any other outcome is the words an
// orchestrator gave it.
const outcomeWords: Record<string, string> = {
  unjudged: "Found by text, judged by nobody",
  no_orchestrator: "No orchestrator worked this rung",
};

// Ladder is a question's ladder trace so far: each rung it passed, what happened there, and what was considered.
export function Ladder({ steps }: { steps?: LadderStep[] }) {
  if (!steps?.length) {
    return null;
  }
  return (
    <details className="older">
      <summary>How it got here</summary>
      <ol className="list-rows" role="list">
        {steps.map((step, at) => (
          <li key={`${at}:${step.rung}`} className="list-row">
            <p>Rung {step.rung}</p>
            {step.outcome ? <p className="muted">{outcomeWords[step.outcome] ?? step.outcome}</p> : null}
            {step.candidates?.length ? <p className="mono muted">{step.candidates.join(", ")}</p> : null}
          </li>
        ))}
      </ol>
    </details>
  );
}
