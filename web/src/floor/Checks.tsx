import type { Standing } from "../api/types";
import { words } from "../parts/parts";
import { StateBadge } from "../ui/display";
import { checkStates } from "./states";

// Checks lists the check results on record for a work order's commit: whether each passed, its name, who stands
// behind it, and the criterion it speaks for.
export function Checks({ standing }: { standing: Standing }) {
  if (!standing.checks.length) {
    return <p className="muted">No check reported on this work's commit.</p>;
  }
  return (
    <ul className="checks" aria-label="Checks">
      {standing.checks.map((check) => (
        <li key={check.id}>
          <StateBadge table={checkStates} state={check.status} />
          <span className="mono">{check.name}</span>
          <span className="subtle small">{words(check.source)}</span>
          <span className="subtle small">{check.criterion_id ? `for ${check.criterion_id}` : ""}</span>
        </li>
      ))}
    </ul>
  );
}
