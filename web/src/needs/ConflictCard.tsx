import type { ConflictNeed } from "../api/types";
import { StepCommands } from "../admin/OutsideEdits";
import { plainText } from "../admin/untrustedText";
import { DecisionCard, DecisionSection } from "../ui/DecisionCard";
import { TableRegion } from "../ui/display";
import type { Frame } from "./frame";
import { conflictKind } from "./kinds";

const hiddenWords = "It holds a character that is not seen, so no command names it.";

// personSide says how the person changed a path: in their clone, or in commits pushed to the base.
function personSide(person: ConflictNeed["paths"][number]["person"], need: ConflictNeed): string {
  const clone = plainText(need.clone);
  switch (person) {
    case "pushed":
      return `Pushed to ${plainText(need.base)}`;
    case "uncommitted":
      return `Uncommitted in the clone ${clone}`;
    case "untracked":
      return `Untracked in the clone ${clone}`;
    case "unpushed":
      return `Committed in the clone ${clone}, not pushed`;
  }
}

// ConflictCard is a lane whose branch changed files a person changed too, in their clone or in pushes to the base
// (FR-UI-14). It names both sides of each file and the ways to settle it, each as its exact commands with Copy. Ropeshift
// settles none of it, so the card has no button that acts.
export function ConflictCard({ need, frame }: { need: ConflictNeed; frame: Frame }) {
  const branch = plainText(need.branch);
  return (
    <DecisionCard kind={conflictKind} project={frame.project} title={frame.title} provenance={frame.provenance}>
      <p className="small">
        Lane {plainText(need.lane)} of {plainText(need.title || need.work_order)}, in {plainText(need.name)}, changed files on its branch {branch} that a person changed too. Ropeshift
        settles none of it and overwrites neither side: choose how, and run that way's commands.
      </p>
      <p className="small">{plainText(need.clears)}</p>
      <DecisionSection title="Both sides">
        <TableRegion label="Files both sides changed">
          <table className="table stack-on-phone" aria-label="Files both sides changed">
            <thead>
              <tr>
                <th scope="col">Path</th>
                <th scope="col">The lane</th>
                <th scope="col">The person</th>
              </tr>
            </thead>
            <tbody>
              {need.paths.map((path, at) => (
                <tr key={`${at}:${path.path}`}>
                  <td data-label="Path" className="wrap">
                    <code>{plainText(path.path)}</code>
                    {path.hidden ? <span className="cell-note">{hiddenWords}</span> : null}
                  </td>
                  <td data-label="The lane">Changed on {branch}</td>
                  <td data-label="The person">{personSide(path.person, need)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableRegion>
        {need.cut ? <p className="small">Git listed more files than a reading keeps, so these may not be every one.</p> : null}
      </DecisionSection>
      <DecisionSection title="How to settle it">
        <p className="help">The commands are for POSIX sh, bash or zsh.</p>
        {need.options.length === 0 ? <p className="small">No command can name these files. Settle it in a terminal in the clone.</p> : null}
        {need.options.map((option) => (
          <StepCommands key={option.kind} step={option} label={plainText(option.title)} />
        ))}
      </DecisionSection>
    </DecisionCard>
  );
}
