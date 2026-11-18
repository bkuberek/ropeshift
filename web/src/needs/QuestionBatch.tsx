import { useId, useState } from "react";

import { answerBatch } from "../api/client";
import type { BatchAnswer, Place } from "../api/client";
import type { Batch, QuestionItem } from "../api/types";
import { Button } from "../ui/controls";
import { DecisionCard } from "../ui/DecisionCard";
import { RecordChip, Refusal, ScopeName } from "../ui/display";
import { Field } from "../ui/forms";
import type { Frame } from "./frame";
import { ReplacesField } from "./Replaces";
import { useAct } from "./useAct";

const ownWords = "__own_words__";

// replaces is the ID of the decision in force the answer replaces, or empty when it replaces none. rationale is why
// a person's own answer is the one to give: the recommendation brings its own, so only an answer that is not the
// recommendation needs it.
export type Draft = { choice: string; words: string; replaces: string; rationale: string };

// isOneWay says an answer to the item cannot be taken back: the question reached a person for a one-way choice,
// or the decision policy keeps its category from an orchestrator. The item states it, as the ledger will
// record it. An item that states nothing falls back on its reason.
export function isOneWay(item: QuestionItem): boolean {
  return item.reversibility ? item.reversibility === "one_way" : item.escalation_reason === "one_way";
}

// A recommendation is ready to send, so a batch that needs only agreement takes one press. One that is
// none of the options waits in the box for a person's own words, and that choice starts chosen, so what
// a person sees chosen is what a send counts and sends. A one-way choice is why the question reached a
// person, whatever made it one, so it starts with nothing chosen and waits for their own choice.
export function startingDraft(item: QuestionItem): Draft {
  const options = item.options ?? [];
  const recommendation = item.recommendation ?? "";
  const oneWay = isOneWay(item);
  if (recommendation !== "" && options.includes(recommendation)) {
    return { choice: oneWay ? "" : recommendation, words: "", replaces: "", rationale: "" };
  }
  if (options.length === 0) {
    return { choice: ownWords, words: oneWay ? "" : recommendation, replaces: "", rationale: "" };
  }
  return { choice: oneWay || recommendation === "" ? "" : ownWords, words: recommendation, replaces: "", rationale: "" };
}

// apartFromOptions says a recommendation stands in the box for a person's own words.
function apartFromOptions(item: QuestionItem): boolean {
  return (item.recommendation ?? "") !== "" && !(item.options ?? []).includes(item.recommendation ?? "");
}

export function answerOf(draft: Draft): string {
  return (draft.choice === ownWords ? draft.words : draft.choice).trim();
}

// accepts says the draft is the recommendation itself, which the ledger records with the recommendation's own
// rationale and the item's citations.
export function accepts(item: QuestionItem, draft: Draft): boolean {
  const answer = answerOf(draft);
  return answer !== "" && answer === (item.recommendation ?? "").trim();
}

// sendable says the draft can be sent: an answer, and for one that is a person's own, why.
function sendable(item: QuestionItem, draft: Draft): boolean {
  return answerOf(draft) !== "" && (accepts(item, draft) || draft.rationale.trim() !== "");
}

function Choice({ name, value, checked, recommended, onPick, label }: {
  name: string;
  value: string;
  checked: boolean;
  recommended: boolean;
  label: string;
  onPick: (value: string) => void;
}) {
  return (
    <label className="option">
      <input type="radio" name={name} value={value} checked={checked} onChange={() => onPick(value)} />
      <span className="option-text">{label}</span>
      {recommended ? <span className="reco-tag">Recommended</span> : null}
    </label>
  );
}

// AnswerField is the control that answers one question: its options with the recommendation marked, and a
// box for a person's own words. Needs you and the open questions both answer through it. A page that shows
// the question's text beside it sets quiet, so the text stays the group's name and is not said twice. A batch
// sets why, which asks a person's own answer for the reason it is given, because an answer that is not the
// recommendation has no rationale of its own.
export function AnswerField({ item, draft, onChange, disabled, quiet = false, why = false }: {
  item: QuestionItem;
  draft: Draft;
  disabled: boolean;
  quiet?: boolean;
  why?: boolean;
  onChange: (draft: Draft) => void;
}) {
  const name = useId();
  const options = item.options ?? [];
  const own = answerOf(draft) !== "" && !accepts(item, draft);
  return (
    <fieldset className="question" disabled={disabled}>
      <legend className={quiet ? "visually-hidden" : "question-text"}>{item.text}</legend>
      <div className="row-tight question-tags">
        {isOneWay(item) ? <span className="cond warn">One-way: choose for yourself</span> : null}
        {item.blocking ? <span className="cond warn">Blocks work</span> : null}
        {(item.categories ?? []).map((category) => (
          <span className="cond" key={category}>
            {category}
          </span>
        ))}
      </div>
      {item.scope ? (
        <p className="small muted">
          Scope: <ScopeName scope={item.scope} title={item.scope_title} />
        </p>
      ) : null}
      {options.map((option) => (
        <Choice
          key={option}
          name={name}
          value={option}
          label={option}
          checked={draft.choice === option}
          recommended={option === item.recommendation}
          onPick={(choice) => onChange({ ...draft, choice })}
        />
      ))}
      {options.length > 0 ? (
        <Choice
          name={name}
          value={ownWords}
          label="Something else"
          checked={draft.choice === ownWords}
          recommended={apartFromOptions(item)}
          onPick={(choice) => onChange({ ...draft, choice })}
        />
      ) : null}
      {item.recommendation_rationale ? <p className="small muted question-reason">Why it is recommended: {item.recommendation_rationale}</p> : null}
      {draft.choice === ownWords ? (
        <Field label="Your answer">
          <textarea className="textarea" rows={3} autoComplete="off" value={draft.words} onChange={(event) => onChange({ ...draft, words: event.target.value })} />
        </Field>
      ) : null}
      {why && own ? (
        <Field
          label="Why"
          help="Your answer is not the recommendation, so it is recorded with your reason."
          warning={draft.rationale.trim() === "" ? "Say why, or this answer is not sent." : undefined}
        >
          <textarea className="textarea" rows={2} autoComplete="off" value={draft.rationale} onChange={(event) => onChange({ ...draft, rationale: event.target.value })} />
        </Field>
      ) : null}
      {(item.citations ?? []).length > 0 ? (
        <p className="small muted question-cites">
          Considered:{" "}
          {(item.citations ?? []).map((citation) => (
            <RecordChip key={citation} id={citation} />
          ))}
        </p>
      ) : null}
      <ReplacesField value={draft.replaces} onChange={(replaces) => onChange({ ...draft, replaces })} />
    </fieldset>
  );
}

// Answered is what a person sent from a batch and the ledger recorded: each question, by its ID and its text,
// with its answer, and the member the ledger recorded the answers for.
export type Answered = { by: string; answers: { id: string; question: string; answer: string }[] };

export function QuestionBatch({ batch, after, frame, compact = false, place, onAnswered }: {
  batch: Batch;
  after: () => Promise<void>;
  frame: Frame;
  compact?: boolean;
  place?: Place;
  onAnswered?: (answered: Answered) => void;
}) {
  const form = useId();
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const { act, busy, failure } = useAct(after);
  const draftOf = (item: QuestionItem) => drafts[item.id] ?? startingDraft(item);
  const ready = batch.items.filter((item) => sendable(item, draftOf(item)));
  const count = batch.items.length;
  // A one-way answer is never chosen for a person, so it waits here until they choose it themselves.
  const waiting = batch.items.filter((item) => isOneWay(item) && answerOf(draftOf(item)) === "").length;

  // The answers go in one request, which records every one or none, so a refusal leaves nothing half sent and
  // names the first answer it met. Each send is safe to repeat. Only an answer the ledger recorded counts as the
  // person's.
  const send = () =>
    act(async () => {
      const answers = ready.map((item): BatchAnswer => {
        const draft = draftOf(item);
        const replaces = draft.replaces ? { supersedes: draft.replaces } : {};
        return accepts(item, draft)
          ? { question: item.id, accepted: true, ...replaces }
          : { question: item.id, answer: answerOf(draft), rationale: draft.rationale.trim(), ...replaces };
      });
      const recorded = await answerBatch(batch.batch.id, answers, place);
      onAnswered?.({
        by: recorded[0]?.decided_by ?? "",
        answers: ready.map((item) => ({ id: item.id, question: item.text, answer: answerOf(draftOf(item)) })),
      });
    });

  const label = ready.length === count ? (count === 1 ? "Send this answer" : `Send ${count} answers`) : `Send ${ready.length} of ${count} answer${count === 1 ? "" : "s"}`;
  return (
    <DecisionCard
      compact={compact}
      kind={{ icon: "question", name: "Questions" }}
      project={frame.project}
      title={frame.title}
      provenance={frame.provenance}
      binding="Each answer is recorded once, as yours."
      actions={
        <Button variant="primary" type="submit" form={form} loading={busy} disabled={busy || ready.length === 0}>
          {label}
        </Button>
      }
    >
      <form
        id={form}
        className="questions"
        onSubmit={(event) => {
          event.preventDefault();
          void send();
        }}
      >
        {waiting > 0 ? (
          <p className="small muted" role="status">
            {waiting === 1 ? "1 one-way question waits" : `${waiting} one-way questions wait`} for your own choice, and {waiting === 1 ? "is" : "are"} not sent until you choose.
          </p>
        ) : null}
        {batch.items.map((item) => (
          <AnswerField key={item.id} item={item} draft={draftOf(item)} disabled={busy} why onChange={(draft) => setDrafts((was) => ({ ...was, [item.id]: draft }))} />
        ))}
      </form>
      {failure ? <Refusal>{failure}</Refusal> : null}
    </DecisionCard>
  );
}
