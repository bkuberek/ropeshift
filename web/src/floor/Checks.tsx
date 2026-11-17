import type { Standing } from "../api/types";
import { words } from "../parts/parts";
import { StateBadge } from "../ui/display";
import { checkStates } from "./states";

// Checks lists the checks on a work order's commit: whether each passed, its name, and in one phrase who stands
// behind it and the criterion it speaks for. Beside the results on record it lists the checks the forge runs on
// the pull request the work lands through, as its worker last read them, whatever the review's state: the
// required ones are what the merge gate reads. A forge check on record that the pull request lists too shows
// once, as the pull request was last read.
export function Checks({ standing }: { standing: Standing }) {
  const pr = standing.pull_request;
  const forge = pr?.checks ?? [];
  const listed = new Set(forge.map((check) => check.name));
  const recorded = standing.checks.filter((check) => !(check.source === "forge" && listed.has(check.name)));
  if (!recorded.length && !forge.length) {
    return <p className="muted">No check reported on this work's commit.</p>;
  }
  return (
    <ul className="checks" aria-label="Checks">
      {recorded.map((check) => (
        <li key={check.id}>
          <StateBadge table={checkStates} state={check.status} />
          <span className="mono">{check.name}</span>
          <span className="subtle small">{check.criterion_id ? `${words(check.source)} for ${check.criterion_id}` : words(check.source)}</span>
        </li>
      ))}
      {forge.map((check) => (
        <li key={`pull-request:${check.name}`}>
          <StateBadge table={checkStates} state={check.state} />
          <span className="mono">{check.name}</span>
          <span className="subtle small">{check.required ? `Pull request #${pr?.number}, required` : `Pull request #${pr?.number}`}</span>
        </li>
      ))}
    </ul>
  );
}
