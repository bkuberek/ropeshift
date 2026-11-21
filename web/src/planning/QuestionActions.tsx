import { useEffect, useId, useRef, useState } from "react";

import { useChange } from "../admin/parts";
import { usePageActions } from "../app/pageActions";
import { addressOf, Link } from "../app/route";
import { answerQuestion } from "../api/client";
import type { Question, QuestionItem } from "../api/types";
import { answerOf, AnswerField, isOneWay, startingDraft } from "../needs/QuestionBatch";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { Field } from "../ui/forms";
import { useToast } from "../ui/overlays";
import { DecideNowButton, onTheLadder, useDecideNow } from "./DecideNow";
import { DismissQuestion, waiting } from "./DismissQuestion";
import { MergeQuestion, mergeTargets } from "./MergeQuestion";

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
// same starting choice: a recommendation stands chosen, and a one-way choice starts with nothing chosen. With
// assume, it records the answer as an assumption, which a member records in an orchestrator's place and which can be
// overturned, with the reason it was assumed.
function AnswerForm({ question, id, assume = false, onDone }: { question: Question; id: string; assume?: boolean; onDone: () => void }) {
  const item = itemOf(question);
  const [draft, setDraft] = useState(() => startingDraft(item));
  const change = useChange();
  const toast = useToast();
  const answer = answerOf(draft);
  // An assumption states why it is assumed, which the ledger requires too.
  const why = draft.rationale.trim();
  const ready = answer !== "" && (!assume || why !== "");
  // The form opens with the cursor in its first choice, so a member who asked for it by keys is in it.
  const shown = useRef<HTMLFormElement>(null);
  useEffect(() => {
    shown.current?.querySelector<HTMLElement>("input:checked, input, textarea")?.focus();
  }, []);

  const send = async () => {
    if (!ready) {
      return;
    }
    // No place is named, so the listener the answer came through is what the decision records.
    const sent = await change.run(async () => {
      await answerQuestion(question.id, answer, undefined, draft.replaces, assume ? { rationale: draft.rationale.trim() } : undefined);
      return true;
    });
    if (sent) {
      toast("success", assume ? "Recorded the assumption." : "Answered the question.");
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
      {assume ? (
        <Field label="Why it is assumed" warning={why === "" ? "Say why, or this assumption is not sent." : undefined}>
          <textarea className="textarea" rows={2} autoComplete="off" value={draft.rationale} disabled={change.busy} onChange={(event) => setDraft({ ...draft, rationale: event.target.value })} />
        </Field>
      ) : null}
      <p className="small muted">
        {assume
          ? "The assumption is recorded once, as yours, and stays reversible: anyone may overturn it, and the question closes."
          : "Each answer is recorded once, as yours: it becomes a decision, and the question closes."}
      </p>
      {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      <div className="row-tight">
        <Button variant="primary" type="submit" loading={change.busy} disabled={change.busy || !ready}>
          {assume ? "Record this assumption" : "Send this answer"}
        </Button>
        <Button onClick={onDone}>Cancel</Button>
      </div>
    </form>
  );
}

// oneWayRefusal is why a one-way question takes no assumption, which the ledger refuses too.
const oneWayRefusal = "A one-way question is answered by a person, never assumed. Answer it instead.";

type Working = "" | "answer" | "assume";

// cardInNeedsYou is where a question that reached a person is decided: its card in Needs you, with its primary button
// focused. A question still on the ladder has no card there yet, so its Decide now moves it there first.
const cardInNeedsYou = (question: Question) =>
  question.status === "needs_human" && question.batch_id ? addressOf("/needs-you", { item: `questions:${question.batch_id}`, focus: "primary" }) : "";

// QuestionActions are what a member does to a question that waits, in an orchestrator's place or not: decide it in
// Needs you once it reached a person, answer it where it stands, record an assumption for a reversible one, merge it
// into a duplicate that stays, or dismiss it.
export function QuestionActions({ question, others = [] }: { question: Question; others?: Question[] }) {
  const [working, setWorking] = useState<Working>("");
  const [refused, setRefused] = useState(false);
  const [dismissing, setDismissing] = useState(false);
  const [merging, setMerging] = useState(false);
  const form = useId();
  const oneWay = isOneWay(itemOf(question));
  // A merge needs a question that stays, so the palette lists Merge only while one waits, and a request for the dialog
  // ends when the last one goes, rather than opening it by itself when another arrives.
  const mergeable = mergeTargets(question, others).length > 0;
  const decideNow = useDecideNow(question);
  useEffect(() => {
    if (!mergeable) {
      setMerging(false);
    }
  }, [mergeable]);
  const assume = () => {
    setRefused(oneWay);
    setWorking(oneWay ? "" : "assume");
  };
  const toggle = (next: Working) => {
    setRefused(false);
    setWorking(working === next ? "" : next);
  };
  // The command palette lists each while the question waits. Each opens the form or the dialog, and sends nothing.
  usePageActions(
    waiting(question)
      ? [
          { key: `action:answer-question:${question.id}`, group: "Actions", id: question.id, title: `Answer question: ${question.text}`, icon: "question", keywords: "reply", run: () => toggle("answer") },
          { key: `action:assume-question:${question.id}`, group: "Actions", id: question.id, title: `Assume an answer: ${question.text}`, icon: "pencil", keywords: "assumption orchestrator", run: assume },
          ...(mergeable ? [{ key: `action:merge-question:${question.id}`, group: "Actions" as const, id: question.id, title: `Merge question: ${question.text}`, icon: "link" as const, keywords: "duplicate same", run: () => setMerging(true) }] : []),
          { key: `action:dismiss-question:${question.id}`, group: "Actions", id: question.id, title: `Dismiss question: ${question.text}`, icon: "x", keywords: "close", run: () => setDismissing(true) },
          ...(onTheLadder(question)
            ? [{ key: `action:decide-question-now:${question.id}`, group: "Actions" as const, id: question.id, title: `Decide now: ${question.text}`, icon: "inbox" as const, keywords: "needs you ladder", run: decideNow.decide }]
            : []),
        ]
      : [],
  );
  if (!waiting(question)) {
    return null;
  }
  const card = cardInNeedsYou(question);
  return (
    <>
      <div className="row-tight">
        {card ? (
          <Link to={card} className="btn btn-sm btn-primary" aria-label={`Decide now: ${question.text}`}>
            Decide now
          </Link>
        ) : null}
        <Button small variant={card ? "secondary" : "primary"} aria-label={`Answer ${question.text}`} aria-expanded={working === "answer"} aria-controls={working === "answer" ? form : undefined} onClick={() => toggle("answer")}>
          Answer
        </Button>
        <Button small aria-label={`Assume an answer to ${question.text}`} aria-expanded={working === "assume"} aria-controls={working === "assume" ? form : undefined} onClick={() => (working === "assume" ? toggle("assume") : assume())}>
          Assume
        </Button>
        <MergeQuestion question={question} others={others} asked={merging} onClosed={() => setMerging(false)} />
        <DismissQuestion question={question} asked={dismissing} onClosed={() => setDismissing(false)} />
        <DecideNowButton question={question} now={decideNow} />
      </div>
      {refused ? <Refusal>{oneWayRefusal}</Refusal> : null}
      {working ? <AnswerForm key={working} question={question} id={form} assume={working === "assume"} onDone={() => setWorking("")} /> : null}
    </>
  );
}
