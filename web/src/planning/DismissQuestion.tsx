import { useState } from "react";

import { useChange } from "../admin/parts";
import { dismissQuestion } from "../api/client";
import type { Question } from "../api/types";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { Dialog, useToast } from "../ui/overlays";

// A question that still waits for an answer is one a member may dismiss.
export const waiting = (question: Question) => ["open", "triage", "needs_human"].includes(question.status);

function DismissDialog({ question, onClose }: { question: Question; onClose: () => void }) {
  const change = useChange();
  const toast = useToast();
  const dismiss = async () => {
    const done = await change.run(async () => {
      await dismissQuestion(question.id);
      return true;
    });
    if (done) {
      toast("success", "Dismissed the question.");
      onClose();
    }
  };
  return (
    <Dialog
      open
      title="Dismiss this question?"
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="danger-outline" loading={change.busy} onClick={() => void dismiss()}>
            Dismiss question
          </Button>
        </>
      }
    >
      <div className="field-stack">
        <p>{question.text}</p>
        <p className="small muted">The question closes with no answer. It stays on record, and it cannot be opened again.</p>
        {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      </div>
    </Dialog>
  );
}

// DismissQuestion is the button on a question that waits, and the dialog it opens asks before it closes the
// question for good, as `ropeshift question dismiss` does.
export function DismissQuestion({ question }: { question: Question }) {
  const [open, setOpen] = useState(false);
  if (!waiting(question)) {
    return null;
  }
  return (
    <>
      <Button small aria-label={`Dismiss ${question.text}`} onClick={() => setOpen(true)}>
        Dismiss
      </Button>
      {open ? <DismissDialog question={question} onClose={() => setOpen(false)} /> : null}
    </>
  );
}
