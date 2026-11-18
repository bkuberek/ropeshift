import { useEffect, useId, useRef, useState } from "react";

import { useChange } from "../admin/parts";
import { usePageActions } from "../app/pageActions";
import { answerQuestion } from "../api/client";
import type { Question, QuestionItem } from "../api/types";
import { answerOf, AnswerField, startingDraft } from "../needs/QuestionBatch";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { useToast } from "../ui/overlays";
import { DismissQuestion, waiting } from "./DismissQuestion";

// itemOf is the question as Needs you shows it to be answered: the same fields, so the same control answers it.
const itemOf = (question: Question): QuestionItem => ({
  id: question.id,
  text: question.text,
  categories: question.categories,
  blocking: question.blocking,
  options: question.options,
  recommendation: question.recommendation,
  recommendation_rationale: question.recommendation_rationale,
  escalation_reason: question.escalation_reason,
  reversibility: question.reversibility,
  overlaps: question.overlaps,
});

// AnswerForm answers one question in place, through the control Needs you answers through, and so with the
// same starting choice: a recommendation stands chosen, and a one-way choice starts with nothing chosen.
function AnswerForm({ question, id, onDone }: { question: Question; id: string; onDone: () => void }) {
  const item = itemOf(question);
  const [draft, setDraft] = useState(() => startingDraft(item));
  const change = useChange();
  const toast = useToast();
  const answer = answerOf(draft);
  // The form opens with the cursor in its first choice, so a member who asked for it by keys is in it.
  const shown = useRef<HTMLFormElement>(null);
  useEffect(() => {
    shown.current?.querySelector<HTMLElement>("input:checked, input, textarea")?.focus();
  }, []);

  const send = async () => {
    if (answer === "") {
      return;
    }
    // No place is named, so the listener the answer came through is what the decision records.
    const sent = await change.run(async () => {
      await answerQuestion(question.id, answer, undefined, draft.replaces);
      return true;
    });
    if (sent) {
      toast("success", "Answered the question.");
      onDone();
    }
  };

  return (
    <form
      id={id}
      ref={shown}
      className="questions"
      onSubmit={(event) => {
        event.preventDefault();
        void send();
      }}
    >
      <AnswerField item={item} draft={draft} disabled={change.busy} quiet onChange={setDraft} />
      <p className="small muted">Each answer is recorded once, as yours: it becomes a decision, and the question closes.</p>
      {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      <div className="row-tight">
        <Button variant="primary" type="submit" loading={change.busy} disabled={change.busy || answer === ""}>
          Send this answer
        </Button>
        <Button onClick={onDone}>Cancel</Button>
      </div>
    </form>
  );
}

// QuestionActions are what a member does to a question that waits: answer it where it stands, or dismiss it.
export function QuestionActions({ question }: { question: Question }) {
  const [answering, setAnswering] = useState(false);
  const [dismissing, setDismissing] = useState(false);
  const form = useId();
  // The command palette lists both while the question waits. Each opens the form or the dialog, and sends nothing.
  usePageActions(
    waiting(question)
      ? [
          { key: `action:answer-question:${question.id}`, group: "Actions", id: question.id, title: `Answer question: ${question.text}`, icon: "question", keywords: "reply", run: () => setAnswering(true) },
          { key: `action:dismiss-question:${question.id}`, group: "Actions", id: question.id, title: `Dismiss question: ${question.text}`, icon: "x", keywords: "close", run: () => setDismissing(true) },
        ]
      : [],
  );
  if (!waiting(question)) {
    return null;
  }
  return (
    <>
      <div className="row-tight">
        <Button small variant="primary" aria-label={`Answer ${question.text}`} aria-expanded={answering} aria-controls={answering ? form : undefined} onClick={() => setAnswering(!answering)}>
          Answer
        </Button>
        <DismissQuestion question={question} asked={dismissing} onClosed={() => setDismissing(false)} />
      </div>
      {answering ? <AnswerForm question={question} id={form} onDone={() => setAnswering(false)} /> : null}
    </>
  );
}
