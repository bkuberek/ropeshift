import { useId, useState } from "react";

import { useChange } from "../admin/parts";
import { mergeQuestion } from "../api/client";
import type { Question } from "../api/types";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { Dialog, useToast } from "../ui/overlays";
import { waiting } from "./DismissQuestion";

function MergeDialog({ question, targets, onClose }: { question: Question; targets: Question[]; onClose: () => void }) {
  const [into, setInto] = useState("");
  const change = useChange();
  const toast = useToast();
  const name = useId();
  const chosen = targets.find((target) => target.id === into);
  const merge = async () => {
    if (!chosen) {
      return;
    }
    const done = await change.run(async () => {
      await mergeQuestion(question.id, chosen.id);
      return true;
    });
    if (done) {
      toast("success", `Merged the question into "${chosen.text}".`);
      onClose();
    }
  };
  return (
    <Dialog
      open
      title="Merge this question?"
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" loading={change.busy} disabled={!chosen || change.busy} onClick={() => void merge()}>
            Merge question
          </Button>
        </>
      }
    >
      <div className="field-stack">
        <p>{question.text}</p>
        <fieldset className="question">
          <legend className="label">The question that stays</legend>
          {targets.map((target) => (
            <label key={target.id} className="option">
              <input type="radio" name={name} value={target.id} checked={into === target.id} onChange={() => setInto(target.id)} />
              <span className="option-text">{target.text}</span>
            </label>
          ))}
        </fieldset>
        <p className="small muted">This question closes as a duplicate of the one that stays, and every lane waiting on it waits on that answer instead.</p>
        {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      </div>
    </Dialog>
  );
}

// plannerAsked says a planning lane asked the question, so it goes to the person.
const plannerAsked = (question: Question) => question.escalation_reason === "planner_asked";

// MergeQuestion is the button on a question that waits, and the dialog it opens names the question that stays, as
// `ropeshift question merge --into` does. A member merges a duplicate in an orchestrator's place. A planning lane's
// question merges only into one a person answers, another planning lane's or one that waits for a person, as the
// ledger holds it.
export function MergeQuestion({ question, others, asked = false, onClosed }: { question: Question; others: Question[]; asked?: boolean; onClosed?: () => void }) {
  const [pressed, setOpen] = useState(false);
  const open = pressed || asked;
  const takes = (other: Question) => !plannerAsked(question) || plannerAsked(other) || other.status === "needs_human";
  const targets = others.filter((other) => other.id !== question.id && waiting(other) && takes(other));
  if (!waiting(question) || targets.length === 0) {
    return null;
  }
  return (
    <>
      <Button small aria-label={`Merge ${question.text}`} onClick={() => setOpen(true)}>
        Merge
      </Button>
      {open ? (
        <MergeDialog
          question={question}
          targets={targets}
          onClose={() => {
            setOpen(false);
            onClosed?.();
          }}
        />
      ) : null}
    </>
  );
}
