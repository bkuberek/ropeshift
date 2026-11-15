import { useEffect, useRef, useState } from "react";

import { newID } from "../admin/logic";
import { useChange } from "../admin/parts";
import { recordDecision } from "../api/client";
import type { Decision } from "../api/types";
import { Fact, words } from "../parts/parts";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { Dialog, useToast } from "../ui/overlays";
import { CategoryChoices } from "./CategoryChoices";
import { decisionProblems, decisionRequest, listOf, noDecision } from "./records";
import type { DecisionDraft } from "./records";
import { useCategories } from "./useCategories";
import { useDialogParam } from "./useDialogParam";

// inForce says a decision still stands, so a new one may replace it.
const inForce = (decision: Decision) => decision.status === "decided" || decision.status === "assumed";

const shortened = (text: string, most = 72) => (text.length > most ? `${text.slice(0, most - 1)}…` : text);

// RecordDecisionDialog records a decision, or an assumption, as `ropeshift decision record` does. Recording is
// one-way: the record stays on the ledger as the member's own, and only an overturn changes it, so the dialog
// goes through a review before it sends anything, and the review says what the record is.
function RecordDecisionDialog({ decisions, onClose }: { decisions: Decision[]; onClose: () => void }) {
  const names = useCategories();
  const [draft, setDraft] = useState<DecisionDraft>(noDecision);
  const [step, setStep] = useState<"write" | "review">("write");
  const [tried, setTried] = useState(false);
  const change = useChange();
  const toast = useToast();
  // The ID of this attempt holds until the dialog closes, so a press after a refusal finds what the first made.
  const id = useRef(newID());
  const field = useRef<HTMLTextAreaElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const problems = tried ? decisionProblems(draft) : {};
  const set = (next: Partial<DecisionDraft>) => setDraft((was) => ({ ...was, ...next }));
  const kind = draft.assumed ? "assumption" : "decision";

  // The review takes focus at its heading, so a screen reader reads what is about to be recorded, and going back
  // returns it to the statement, because the button that held it changes its words.
  const reviewed = useRef(false);
  useEffect(() => {
    if (step === "review") {
      reviewed.current = true;
      heading.current?.focus();
    } else if (reviewed.current) {
      field.current?.focus();
    }
  }, [step]);

  const review = () => {
    setTried(true);
    if (Object.keys(decisionProblems(draft)).length === 0) {
      change.setFailure("");
      setStep("review");
    }
  };

  const record = async () => {
    const recorded = await change.run(() => recordDecision(decisionRequest(id.current, draft)));
    if (recorded) {
      const overlaps = recorded.overlaps ?? [];
      toast("success", `Recorded the ${kind}.${overlaps.length ? ` Its scope overlaps ${overlaps.join(", ")}.` : ""}`);
      onClose();
    }
  };

  const replaces = decisions.find((row) => row.id === draft.supersedes);
  return (
    <Dialog
      open
      wide
      title="Record decision"
      onClose={onClose}
      initialFocus={field}
      footer={
        step === "write" ? (
          <>
            <span className="spacer" />
            <Button onClick={onClose}>Cancel</Button>
            <Button variant="primary" onClick={review}>
              Review
            </Button>
          </>
        ) : (
          <>
            <span className="spacer" />
            <Button disabled={change.busy} onClick={() => setStep("write")}>
              Back
            </Button>
            <Button variant="primary" icon="check" loading={change.busy} onClick={() => void record()}>
              {draft.assumed ? "Record assumption" : "Record decision"}
            </Button>
          </>
        )
      }
    >
      {step === "write" ? (
        <form
          className="field-stack"
          noValidate
          onSubmit={(event) => {
            event.preventDefault();
            review();
          }}
        >
          <p className="small muted">A decision is recorded as yours, with the policy in force. It stays on the ledger, and it changes only when it is overturned.</p>
          <Field label="Statement" help="What was decided, in a sentence." error={problems.statement}>
            <textarea ref={field} className="textarea" rows={3} autoComplete="off" value={draft.statement} onChange={(event) => set({ statement: event.target.value })} />
          </Field>
          <Field label="Rationale" help="Why it was decided.">
            <textarea className="textarea" rows={3} autoComplete="off" value={draft.rationale} onChange={(event) => set({ rationale: event.target.value })} />
          </Field>
          <div className="field">
            <label className="check-row">
              <input type="checkbox" checked={draft.assumed} onChange={(event) => set({ assumed: event.target.checked })} />
              <span>Record as an assumption</span>
            </label>
            <p className="help">A reversible choice recorded so that work can go on. Overturn it later if it proves wrong.</p>
          </div>
          <Field label="Reversibility" help="A one-way decision is hard to take back, and the ledger treats it so.">
            <Select value={draft.reversibility} onChange={(event) => set({ reversibility: event.target.value as DecisionDraft["reversibility"] })}>
              <option value="">Not stated</option>
              <option value="reversible">Reversible</option>
              <option value="one_way">One way</option>
            </Select>
          </Field>
          <Field label="Scope" help="What it applies to, such as an area of the work. Decisions in force with an overlapping scope are named when it is recorded.">
            <input className="input" autoComplete="off" value={draft.scope} onChange={(event) => set({ scope: event.target.value })} />
          </Field>
          <CategoryChoices names={names} chosen={draft.categories} onChange={(categories) => set({ categories })} />
          <Field label="Citations" help="What it rests on, one to a line: a record such as requirement:FR-RETRY-1, or a path.">
            <textarea className="textarea mono" rows={2} autoComplete="off" spellCheck={false} value={draft.citations} onChange={(event) => set({ citations: event.target.value })} />
          </Field>
          <Field label="Replaces" help="Takes the place of a decision in force, with no rework. To reverse one and rework what rested on it, overturn it instead.">
            <Select value={draft.supersedes} onChange={(event) => set({ supersedes: event.target.value })}>
              <option value="">Nothing</option>
              {decisions.filter(inForce).map((row) => (
                <option key={row.id} value={row.id}>
                  {row.id}: {shortened(row.statement)}
                </option>
              ))}
            </Select>
          </Field>
          <details className="disclosure">
            <summary>More</summary>
            <div className="field-stack">
              <Field label="Answers the question" help="The ID of the question this decision answers." error={problems.questionID}>
                <input className="input mono" autoComplete="off" spellCheck={false} value={draft.questionID} onChange={(event) => set({ questionID: event.target.value })} />
              </Field>
              <Field label="Judged compatible with" help="The IDs of decisions it overlaps and does not conflict with, separated by a space or a comma." error={problems.noConflict}>
                <textarea className="textarea mono" rows={2} autoComplete="off" spellCheck={false} value={draft.noConflict} onChange={(event) => set({ noConflict: event.target.value })} />
              </Field>
              <Field label="Decision ID (optional)" help="Leave it empty and ropeshift names the decision." error={problems.id}>
                <input className="input mono" autoComplete="off" spellCheck={false} value={draft.id} onChange={(event) => set({ id: event.target.value })} />
              </Field>
            </div>
          </details>
        </form>
      ) : (
        <div className="field-stack">
          <h3 className="h4" ref={heading} tabIndex={-1}>
            Review the {kind}
          </h3>
          <dl className="kv">
            <Fact name="Statement">{draft.statement.trim()}</Fact>
            {draft.rationale.trim() ? <Fact name="Rationale">{draft.rationale.trim()}</Fact> : null}
            <Fact name="Kind">{draft.assumed ? "An assumption" : "A decision"}</Fact>
            {draft.reversibility ? <Fact name="Reversibility">{words(draft.reversibility)}</Fact> : null}
            {draft.scope.trim() ? <Fact name="Scope">{draft.scope.trim()}</Fact> : null}
            {draft.categories.length ? <Fact name="Categories">{draft.categories.map((name) => words(name)).join(", ")}</Fact> : null}
            {listOf(draft.citations).length ? (
              <Fact name="Cites" mono>
                {listOf(draft.citations).join(", ")}
              </Fact>
            ) : null}
            {replaces ? <Fact name="Replaces">{replaces.id}</Fact> : null}
            {draft.questionID.trim() ? <Fact name="Answers question">{draft.questionID.trim()}</Fact> : null}
          </dl>
          <p className="small muted">
            {draft.assumed
              ? "An assumption lets work go on under it. It is recorded once, as yours, and if it proves wrong you overturn it: the work that rested on it goes to rework."
              : "A decision is recorded once, as yours. It stays on the ledger, and you change it by overturning it: the work that rested on it goes to rework."}
          </p>
          {change.failure ? <Refusal>{change.failure}</Refusal> : null}
        </div>
      )}
    </Dialog>
  );
}

// RecordDecision is the button that opens the dialog, in the decisions' heading. decisions are the ones the view
// lists, which the dialog offers to replace. The address can open it too, which is how the command palette does.
export function RecordDecision({ decisions }: { decisions: Decision[] }) {
  const { open, show, close } = useDialogParam("record");
  return (
    <>
      <Button icon="plus" onClick={show}>
        Record decision
      </Button>
      {open ? <RecordDecisionDialog decisions={decisions} onClose={close} /> : null}
    </>
  );
}
