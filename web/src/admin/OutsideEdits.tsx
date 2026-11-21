import { useReducer } from "react";

import { readOutsideEdits } from "../api/admin";
import type { CloneEditsView, EditState, EditStep, EditView, OutsideEdits } from "../api/admin";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import type { Tone } from "../floor/states";
import { Button } from "../ui/controls";
import { Badge, Panel, Refusal, SkeletonRows, TableRegion } from "../ui/display";
import type { GlyphName } from "../ui/Icon";
import { useCopy } from "../ui/useCopy";
import { plainText } from "./untrustedText";

// A reading runs git in every clone, so the page asks for one at most this often, however much it hears.
const readGapMs = 30_000;

const states: Record<EditState, { label: string; tone: Tone; glyph: GlyphName }> = {
  uncommitted: { label: "Uncommitted", tone: "warning", glyph: "half" },
  untracked: { label: "Untracked", tone: "neutral", glyph: "ring" },
  unpushed: { label: "Unpushed", tone: "active", glyph: "arrow" },
};

const hiddenWords = "No command names it, because it holds a character that is not seen or a backslash. Commit it from a terminal in the clone.";

// nextStep names the steps that take a path, by the titles of the clone's steps of those kinds.
function nextStep(edit: EditView, clone: CloneEditsView): string {
  if (edit.hidden) {
    return hiddenWords;
  }
  const titles = clone.steps.filter((step) => edit.steps.includes(step.kind)).map((step) => plainText(step.title));
  return titles.length ? titles.join(", or ") : "Set a remote for the clone, then push it.";
}

// StepCommands is one step: what it does, and its exact commands in mono with Copy, which copies what the page shows.
function StepCommands({ step, clone }: { step: EditStep; clone: string }) {
  const copy = useCopy();
  const title = plainText(step.title);
  const text = plainText(step.commands.join("\n"));
  return (
    <div role="group" aria-label={`${title} in ${clone}`} className="stack">
      <div className="row">
        <strong>{title}</strong>
        <span className="spacer" />
        <Button small icon="copy" aria-label={`Copy the commands: ${title}`} onClick={() => copy(text, "the commands")}>
          Copy
        </Button>
      </div>
      <p className="small">{plainText(step.detail)}</p>
      <pre className="op">{text}</pre>
    </div>
  );
}

// CloneFiles is one clone: where it is, each path intake and lanes cannot read with its state and next step, and the
// steps that make them readable.
function CloneFiles({ clone, limit }: { clone: CloneEditsView; limit: number }) {
  const name = plainText(clone.name);
  return (
    <div className="stack">
      <h3 className="h4">
        {name}{" "}
        {clone.planning ? (
          <Badge glyph="target" tone="neutral">
            Planning
          </Badge>
        ) : null}
      </h3>
      {clone.problem ? (
        <p className="small">{plainText(clone.problem)}</p>
      ) : (
        <p className="small muted">
          <code>{plainText(clone.path)}</code>, {clone.branch ? <>on {plainText(clone.branch)}</> : "with no branch checked out"}
          {clone.remote ? <>, pushes to {plainText(clone.remote)}</> : ", with no remote"}
        </p>
      )}
      {!clone.problem && clone.edits.length === 0 ? <p className="small">Nothing in this clone is out of reach of intake and lanes.</p> : null}
      {clone.edits.length ? (
        <TableRegion label={`Files of ${name}`}>
          <table className="table stack-on-phone" aria-label={`Files in ${name}`}>
            <thead>
              <tr>
                <th scope="col">Path</th>
                <th scope="col">State</th>
                <th scope="col">Next step</th>
              </tr>
            </thead>
            <tbody>
              {clone.edits.map((edit, at) => (
                <tr key={`${at}:${edit.path}`}>
                  <td data-label="Path" className="wrap">
                    <code>{plainText(edit.path)}</code>
                  </td>
                  <td data-label="State">
                    <Badge tone={states[edit.state].tone} glyph={states[edit.state].glyph}>
                      {states[edit.state].label}
                    </Badge>
                  </td>
                  <td data-label="Next step" className="wrap">
                    {nextStep(edit, clone)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableRegion>
      ) : null}
      {clone.cut ? <p className="small">These are the first {limit} files git listed. It listed more, and the steps take only these.</p> : null}
      {clone.steps.map((step) => (
        <StepCommands key={`${step.kind}:${step.branch}`} step={step} clone={name} />
      ))}
    </div>
  );
}

// OutsideEditsPanel lists, for each registered repository's clone on the daemon's worker's machine, the files intake
// and lanes cannot read, by path, with the step that makes each readable (FR-UI-14). On Plans it lists only the planning
// repository's clone.
export function OutsideEditsPanel({ live, project, planningOnly = false }: { live: Live; project: string; planningOnly?: boolean }) {
  const [version, again] = useReducer((count: number) => count + 1, 0);
  const read = useView<OutsideEdits>(() => readOutsideEdits(), `outside-edits:${project}#${version}`, live, readGapMs);
  const clones = (read.view?.clones ?? []).filter((clone) => !planningOnly || clone.planning);
  return (
    <Panel
      title="Files ropeshift cannot read"
      actions={
        <Button small icon="retry" loading={read.busy} onClick={again}>
          Read again
        </Button>
      }
    >
      <div className="stack">
        <p className="help">
          Lanes start from the remote's copy of their base branch, and intake reads the remote's copy of the default branch, so a file that is uncommitted,
          untracked, or committed and not pushed reaches neither. Each step below is the exact commands to run on the clone's machine.
          The commands are for POSIX sh, bash or zsh.
        </p>
        {read.failure ? <Refusal>{read.failure}</Refusal> : null}
        {read.view === null && !read.failure ? <SkeletonRows rows={2} /> : null}
        {read.view && clones.length === 0 ? <p className="small muted">{planningOnly ? "The project has no planning repository." : "No repository is registered."}</p> : null}
        {read.view ? clones.map((clone) => <CloneFiles key={clone.repository} clone={clone} limit={read.view?.limit ?? 0} />) : null}
      </div>
    </Panel>
  );
}
