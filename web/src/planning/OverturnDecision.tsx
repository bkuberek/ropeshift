import { useRef, useState } from "react";
import type { RefObject } from "react";

import { newID } from "../admin/logic";
import { useChange } from "../admin/parts";
import { overturnDecision } from "../api/client";
import type { Decision, Overturned } from "../api/types";
import { addressOf, Link } from "../app/route";
import { plural } from "../parts/parts";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { Field } from "../ui/forms";
import { Dialog } from "../ui/overlays";
import { CitationFinder } from "./CitationFinder";
import { citedLine, inForce, listOf, noOverturn, overturnProblems, overturnRequest } from "./records";
import type { OverturnDraft } from "./records";

// What an overturn did: the decision that took the place of the one reversed, the rework it created, and the
// lanes it told. Each work order and decision is a link, so a member goes to what the overturn made.
function OverturnResult({ result, heading, onClose }: { result: Overturned; heading: RefObject<HTMLHeadingElement | null>; onClose: () => void }) {
  const rework = result.rework ?? [];
  const told = result.noticed?.length ?? 0;
  return (
    <div className="field-stack">
      <h3 className="h4" ref={heading} tabIndex={-1}>
        Overturned {result.old.id}
      </h3>
      <p>
        <Link to={addressOf("/decisions", { id: result.new.id })} className="mono" onClick={onClose}>
          {result.new.id}
        </Link>{" "}
        takes its place: {result.new.statement}
      </p>
      {rework.length > 0 ? (
        <>
          <p>The work that rested on it goes to rework:</p>
          <ul className="result-links" aria-label="Rework created">
            {rework.map((id) => (
              <li key={id}>
                <Link to={addressOf("/work-order", { id })} className="mono" onClick={onClose}>
                  {id}
                </Link>
              </li>
            ))}
          </ul>
        </>
      ) : (
        <p>No completed work rested on it, so no rework was created.</p>
      )}
      <p className="small muted">
        {told > 0 ? `${plural(told, "lane")} ${told === 1 ? "was" : "were"} told that it no longer stands.` : "No lane had read it, so none was told."} The overturned decision stays on record.
      </p>
    </div>
  );
}

// OverturnDialog reverses a decision in force, as `ropeshift decision overturn` does: what stands instead, and
// why. The overturned decision stays on record as overturned, and the decision that takes its place records
// the member as its author. Each completed work order that rested on the old one goes to rework, and each lane
// that read it is told, in one step that stands or does not.
function OverturnDialog({ decision, onClose }: { decision: Decision; onClose: () => void }) {
  const [draft, setDraft] = useState<OverturnDraft>(noOverturn);
  const [tried, setTried] = useState(false);
  const [result, setResult] = useState<Overturned | null>(null);
  const change = useChange();
  // The ID of this attempt holds until the dialog closes, so a press after a refusal finds what the first made.
  const id = useRef(newID());
  const field = useRef<HTMLTextAreaElement>(null);
  // What the overturn did takes focus when it is shown, so a screen reader reads it.
  const outcome = useRef<HTMLHeadingElement>(null);
  const problems = tried ? overturnProblems(draft) : {};
  const set = (next: Partial<OverturnDraft>) => setDraft((was) => ({ ...was, ...next }));

  const overturn = async () => {
    setTried(true);
    const stopped = overturnProblems(draft);
    if (stopped.statement || stopped.id) {
      return;
    }
    const done = await change.run(() => overturnDecision(decision.id, overturnRequest(id.current, draft)));
    if (done) {
      setResult(done);
    }
  };

  return (
    <Dialog
      open
      title={`Overturn ${decision.id}`}
      onClose={onClose}
      initialFocus={result ? outcome : field}
      footer={
        result ? (
          <>
            <span className="spacer" />
            <Button variant="primary" onClick={onClose}>
              Done
            </Button>
          </>
        ) : (
          <>
            <span className="spacer" />
            <Button onClick={onClose}>Cancel</Button>
            <Button variant="primary" loading={change.busy} onClick={() => void overturn()}>
              Overturn decision
            </Button>
          </>
        )
      }
    >
      {result ? (
        <OverturnResult result={result} heading={outcome} onClose={onClose} />
      ) : (
        <form
          className="field-stack"
          noValidate
          onSubmit={(event) => {
            event.preventDefault();
            void overturn();
          }}
        >
          <p>{decision.statement}</p>
          <p className="small muted">
            The decision becomes overturned and stays on record, and the new one takes its place. Each completed work order that rested on it goes to rework, and each lane that read it is told.
          </p>
          <Field label="New statement" help="What stands instead." error={problems.statement}>
            <textarea ref={field} className="textarea" rows={3} autoComplete="off" value={draft.statement} onChange={(event) => set({ statement: event.target.value })} />
          </Field>
          <Field label="Rationale" help="Why it is reversed.">
            <textarea className="textarea" rows={3} autoComplete="off" value={draft.rationale} onChange={(event) => set({ rationale: event.target.value })} />
          </Field>
          <Field label="Citations" help="What it rests on, one to a line: a record such as requirement:FR-RETRY-1, or a path.">
            <textarea className="textarea mono" rows={2} autoComplete="off" spellCheck={false} value={draft.citations} onChange={(event) => set({ citations: event.target.value })} />
          </Field>
          <CitationFinder cited={listOf(draft.citations)} onCite={(citation) => set({ citations: citedLine(draft.citations, citation) })} />
          <details className="disclosure">
            <summary>More</summary>
            <div className="field-stack">
              <Field label="ID of the new decision (optional)" help="Leave it empty and ropeshift names the decision." error={problems.id}>
                <input className="input mono" autoComplete="off" spellCheck={false} value={draft.id} onChange={(event) => set({ id: event.target.value })} />
              </Field>
            </div>
          </details>
          {change.failure ? <Refusal>{change.failure}</Refusal> : null}
        </form>
      )}
    </Dialog>
  );
}

// OverturnDecisionButton is the button on a decision in force, which opens the dialog. A decision that was
// replaced or overturned has none, because the ledger overturns a decision once.
export function OverturnDecisionButton({ decision }: { decision: Decision }) {
  const [open, setOpen] = useState(false);
  if (!inForce(decision)) {
    return null;
  }
  return (
    <>
      <Button small aria-label={`Overturn ${decision.id}`} onClick={() => setOpen(true)}>
        Overturn
      </Button>
      {open ? <OverturnDialog decision={decision} onClose={() => setOpen(false)} /> : null}
    </>
  );
}
