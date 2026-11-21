import { useEffect, useId, useRef, useState } from "react";
import type { ReactNode } from "react";

import { answerBatch } from "../api/client";
import type { BatchAnswer, Place } from "../api/client";
import type { Batch, QuestionItem } from "../api/types";
import { Ladder } from "../planning/Ladder";
import { Button } from "../ui/controls";
import { DecisionCard } from "../ui/DecisionCard";
import { RecordChip, Refusal, ScopeName } from "../ui/display";
import { Field } from "../ui/forms";
import { useDiscuss } from "./discuss";
import type { Frame } from "./frame";
import { NoOrchestrator } from "./NoOrchestrator";
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

function Choice({ name, value, checked, recommended, onPick, label, tradeoff }: {
  name: string;
  value: string;
  checked: boolean;
  recommended: boolean;
  label: string;
  // tradeoff is what choosing this option gives up, as the asker wrote it.
  tradeoff?: string;
  onPick: (value: string) => void;
}) {
  return (
    <label className="option">
      <input type="radio" name={name} value={value} checked={checked} onChange={() => onPick(value)} />
      <span className="option-text">{label}</span>
      {recommended ? <span className="reco-tag">Recommended</span> : null}
      {tradeoff ? <span className="small muted option-tradeoff">{tradeoff}</span> : null}
    </label>
  );
}

// foundByText says every citation of an item is what the core found by text and judged nobody: no orchestrator finished
// the item's ladder, and no rung that names a candidate is one an orchestrator ran before it stopped.
const foundByText = (item: QuestionItem) =>
  item.no_orchestrator === true && (item.ladder_trace ?? []).every((step) => !step.candidates?.length || step.outcome === "unjudged");

// hasRecommendation says the asker recommended an answer, which a card then leads with.
export const hasRecommendation = (item: QuestionItem) => (item.recommendation ?? "").trim() !== "";

// askerOf names who asked a question, as a person reads it: the lane, or whoever the ledger recorded.
const askerOf = (item: QuestionItem) => (item.lane ? `lane ${item.lane}` : (item.asked_by ?? ""));

// Recommendation leads a question's card: the answer the asker recommends, why, and who asked. While the other options
// are folded, it also says what the recommendation trades off, so agreeing never hides what is agreed to. A question
// that came with none says so, and asks for an answer.
function Recommendation({ item, draft, folded }: { item: QuestionItem; draft: Draft; folded: boolean }) {
  const asker = askerOf(item);
  const tradeoff = item.recommendation ? item.tradeoffs?.[item.recommendation] : undefined;
  if (!hasRecommendation(item)) {
    return (
      <p className="small muted question-reason">
        {item.no_orchestrator ? "It came with no recommendation, and no orchestrator judged it." : "It came with no recommendation."} Choose an answer, or discuss it first.
      </p>
    );
  }
  return (
    <div className="reco">
      <p className="reco-line">
        <span className="reco-tag">Recommended</span> <strong>{item.recommendation}</strong>
      </p>
      {folded && tradeoff ? <p className="small muted option-tradeoff">{tradeoff}</p> : null}
      {item.recommendation_rationale ? <p className="small question-reason">Why it is recommended: {item.recommendation_rationale}</p> : null}
      {asker ? <p className="small muted">Asked by {asker}</p> : null}
      {isOneWay(item) && answerOf(draft) === "" ? <p className="small muted">Nothing is chosen: a one-way answer is always your own choice.</p> : null}
    </div>
  );
}

// AnswerField is the control that answers one question: its options with the recommendation marked, and a
// box for a person's own words. Needs you and the open questions both answer through it. A page that shows
// the question's text beside it sets quiet, so the text stays the group's name and is not said twice. A batch
// sets why, which asks a person's own answer for the reason it is given, because an answer that is not the
// recommendation has no rationale of its own. A card sets lead, which puts the recommendation first and keeps the
// other options folded until open, and extra holds its buttons for this question.
export function AnswerField({ item, draft, onChange, disabled, quiet = false, why = false, lead = false, open = true, id, extra }: {
  item: QuestionItem;
  draft: Draft;
  disabled: boolean;
  quiet?: boolean;
  why?: boolean;
  lead?: boolean;
  open?: boolean;
  id?: string;
  extra?: ReactNode;
  onChange: (draft: Draft) => void;
}) {
  const name = useId();
  const options = item.options ?? [];
  const own = answerOf(draft) !== "" && !accepts(item, draft);
  const folded = lead && hasRecommendation(item) && !open;
  return (
    <fieldset className="question" disabled={disabled} id={id}>
      <legend className={quiet ? "visually-hidden" : "question-text"}>{item.text}</legend>
      <div className="row-tight question-tags">
        {isOneWay(item) ? <span className="cond warn">One-way: choose for yourself</span> : null}
        {item.blocking ? <span className="cond warn">Blocks work</span> : null}
        {item.no_orchestrator ? <span className="cond">No orchestrator judged it</span> : null}
        {(item.categories ?? []).map((category) => (
          <span className="cond" key={category}>
            {category}
          </span>
        ))}
      </div>
      {lead ? <Recommendation item={item} draft={draft} folded={folded} /> : null}
      {item.scope ? (
        <p className="small muted">
          Scope: <ScopeName scope={item.scope} title={item.scope_title} />
        </p>
      ) : null}
      {item.unblocks ? <p className="small muted question-unblocks">Unblocks: {item.unblocks}</p> : null}
      {extra}
      {folded ? null : options.map((option) => (
        <Choice
          key={option}
          name={name}
          value={option}
          label={option}
          checked={draft.choice === option}
          recommended={option === item.recommendation}
          tradeoff={item.tradeoffs?.[option]}
          onPick={(choice) => onChange({ ...draft, choice })}
        />
      ))}
      {options.length > 0 && !folded ? (
        <Choice
          name={name}
          value={ownWords}
          label="Something else"
          checked={draft.choice === ownWords}
          recommended={apartFromOptions(item)}
          onPick={(choice) => onChange({ ...draft, choice })}
        />
      ) : null}
      {!lead && item.recommendation_rationale ? <p className="small muted question-reason">Why it is recommended: {item.recommendation_rationale}</p> : null}
      {draft.choice === ownWords && !folded ? (
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
          {foundByText(item) ? "Found by text, judged by nobody:" : "Considered:"}{" "}
          {(item.citations ?? []).map((citation) => (
            <RecordChip key={citation} id={citation} />
          ))}
        </p>
      ) : null}
      <Ladder steps={item.ladder_trace} />
      <ReplacesField value={draft.replaces} overlaps={item.overlaps} onChange={(replaces) => onChange({ ...draft, replaces })} />
    </fieldset>
  );
}

// Answered is what a person sent from a batch and the ledger recorded: each question, by its ID and its text,
// with its answer, and the member the ledger recorded the answers for.
export type Answered = { by: string; answers: { id: string; question: string; answer: string }[] };

// consequenceOf says what a one-way answer does, which its confirm names before it is recorded.
function consequenceOf(item: QuestionItem): ReactNode {
  const after = "nobody can take it back, and work that already finished under it goes back for one rework.";
  return item.scope ? (
    <>
      It binds <ScopeName scope={item.scope} title={item.scope_title} />, {after}
    </>
  ) : (
    `It cannot be undone: ${after}`
  );
}

// OneWayConfirm stands in place under a one-way question whose recommendation a person agreed to, and records it
// only once they confirm. It takes focus, so a screen reader reads the consequence first.
function OneWayConfirm({ item, busy, onCancel, onRecord }: { item: QuestionItem; busy: boolean; onCancel: () => void; onRecord: () => void }) {
  const box = useRef<HTMLDivElement>(null);
  const heading = useId();
  useEffect(() => box.current?.focus(), []);
  return (
    <div className="confirm" role="group" aria-labelledby={heading} tabIndex={-1} ref={box}>
      <p id={heading}>
        <strong>Record a one-way decision?</strong> {consequenceOf(item)}
      </p>
      <div className="row-tight">
        <Button disabled={busy} onClick={onCancel}>
          Cancel
        </Button>
        <Button variant="primary" loading={busy} disabled={busy} onClick={onRecord}>
          Record one-way decision
        </Button>
      </div>
    </div>
  );
}

const plural = (count: number, noun: string) => `${count} ${noun}${count === 1 ? "" : "s"}`;

// A card leads with what the asker recommends, so agreeing is one press: the recommendation of every question whose
// answer can be taken back starts chosen. A one-way question starts with nothing chosen, and its Agree opens a
// confirm in place that names the consequence. Choose another unfolds the other options, and Discuss opens the
// conversation with the session that asked.
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
  // choosing holds each question whose other options a person unfolded, and confirming the one-way question whose
  // Agree waits for its confirm.
  const [choosing, setChoosing] = useState<Record<string, boolean>>({});
  const [confirming, setConfirming] = useState("");
  const opener = useRef<HTMLElement | null>(null);
  const { act, busy, failure } = useAct(after);
  const discuss = useDiscuss(place);
  const draftOf = (item: QuestionItem) => drafts[item.id] ?? startingDraft(item);
  // The card's own send covers only answers that can be taken back: a one-way answer, chosen or not, is recorded on
  // its own through its confirm (FR-UI-16).
  const ready = batch.items.filter((item) => !isOneWay(item) && sendable(item, draftOf(item)));
  const count = batch.items.length;
  // A one-way answer is never chosen for a person, so it waits here until they choose it themselves.
  const waiting = batch.items.filter((item) => isOneWay(item) && answerOf(draftOf(item)) === "").length;
  const [lone] = count === 1 ? batch.items : [];
  const agreeing = ready.length > 0 && ready.every((item) => accepts(item, draftOf(item)));
  // agreesWith says a one-way question's confirm records its recommendation: it has one and nothing else is chosen, or
  // the recommendation is chosen.
  const agreesWith = (item: QuestionItem) => (answerOf(draftOf(item)) === "" ? hasRecommendation(item) : accepts(item, draftOf(item)));
  // confirmable says a one-way question's own button opens its confirm: it has a recommendation to agree with and
  // nothing else chosen, or an answer ready to send.
  const confirmable = (item: QuestionItem) =>
    isOneWay(item) && (answerOf(draftOf(item)) === "" ? hasRecommendation(item) : sendable(item, draftOf(item)));
  const oneWayLabel = (item: QuestionItem) => (agreesWith(item) ? "Agree with recommendation" : "Send this answer");
  // A one-way question alone on its card has its confirm behind the primary button.
  const loneOneWay = lone !== undefined && isOneWay(lone);
  const fieldOf = (item: QuestionItem) => `${form}-question-${item.id}`;

  const askConfirm = (item: QuestionItem) => {
    opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setConfirming(item.id);
  };
  const cancelConfirm = () => {
    setConfirming("");
    requestAnimationFrame(() => opener.current?.focus());
  };

  // Choose another unfolds the other options and puts focus on the one chosen, or the first.
  const toggleChoosing = (item: QuestionItem) => {
    const open = !choosing[item.id];
    setChoosing((was) => ({ ...was, [item.id]: open }));
    if (open) {
      requestAnimationFrame(() => {
        const answers = document.getElementById(fieldOf(item));
        (answers?.querySelector<HTMLElement>("input[type=radio]:checked") ?? answers?.querySelector<HTMLElement>("input[type=radio], textarea"))?.focus();
      });
    }
  };

  // Accept all chooses the recommendation of every question whose answer can be taken back, and leaves a one-way
  // question as it stands, so a person still chooses each of those themselves. It keeps what a person named to replace.
  const acceptAll = () =>
    setDrafts((was) => {
      const next = { ...was };
      for (const item of batch.items) {
        if (!isOneWay(item) && (item.recommendation ?? "") !== "") {
          next[item.id] = { ...startingDraft({ ...item, reversibility: "reversible", escalation_reason: undefined }), replaces: (was[item.id] ?? startingDraft(item)).replaces };
        }
      }
      return next;
    });

  // The answers go in one request, which records every one or none, so a refusal leaves nothing half sent and
  // names the first answer it met. Each send is safe to repeat. Only an answer the ledger recorded counts as the
  // person's.
  const record = (rows: { item: QuestionItem; answer: BatchAnswer; words: string }[]) =>
    act(async () => {
      const recorded = await answerBatch(
        batch.batch.id,
        rows.map((row) => row.answer),
        place,
      );
      setConfirming("");
      onAnswered?.({ by: recorded[0]?.decided_by ?? "", answers: rows.map((row) => ({ id: row.item.id, question: row.item.text, answer: row.words })) });
    });
  const replacing = (draft: Draft) => (draft.replaces ? { supersedes: draft.replaces } : {});
  const send = () => {
    if (ready.length === 0) {
      return Promise.resolve(false);
    }
    return record(
      ready.map((item) => {
        const draft = draftOf(item);
        const answer: BatchAnswer = accepts(item, draft)
          ? { question: item.id, accepted: true, ...(item.reversibility ? { expect_reversibility: item.reversibility } : {}), ...replacing(draft) }
          : { question: item.id, answer: answerOf(draft), rationale: draft.rationale.trim(), ...replacing(draft) };
        return { item, answer, words: answerOf(draft) };
      }),
    );
  };
  // A one-way question a person confirmed is recorded on its own, at once: the recommendation it was read with, when
  // nothing else is chosen or the recommendation is, and otherwise the person's own answer with their reason.
  const recordOneWay = (item: QuestionItem) => {
    const draft = draftOf(item);
    const answer: BatchAnswer = agreesWith(item)
      ? { question: item.id, accepted: true, expect_reversibility: item.reversibility ?? "one_way", ...replacing(draft) }
      : { question: item.id, answer: answerOf(draft), rationale: draft.rationale.trim(), ...replacing(draft) };
    return record([{ item, answer, words: agreesWith(item) ? (item.recommendation ?? "").trim() : answerOf(draft) }]);
  };

  const sendLabel = ready.length === count ? (count === 1 ? "Send this answer" : `Send ${count} answers`) : `Send ${ready.length} of ${count} answer${count === 1 ? "" : "s"}`;
  const label = loneOneWay ? oneWayLabel(lone) : agreeing && count === 1 ? "Agree with recommendation" : agreeing ? `Agree with ${plural(ready.length, "recommendation")}` : sendLabel;
  const recordsRecommendation = loneOneWay ? agreesWith(lone) : agreeing;

  // buttonsOf are what a person does with one question besides agreeing: discuss it, and choose another answer. A
  // one-way question in a batch of several has its own Agree, or Send this answer once another answer is chosen, since
  // the card's send covers only answers that can be taken back. Each opens the question's confirm.
  const buttonsOf = (item: QuestionItem, own: boolean) => (
    <>
      {discuss ? (
        <Button shortcut="D" data-shortcut="discuss" onClick={() => discuss(item)}>
          Discuss
        </Button>
      ) : null}
      {hasRecommendation(item) ? (
        <Button shortcut="C" data-shortcut="choose" aria-expanded={choosing[item.id] === true} aria-controls={fieldOf(item)} onClick={() => toggleChoosing(item)}>
          Choose another
        </Button>
      ) : null}
      {own && confirmable(item) ? (
        <Button disabled={busy || confirming !== ""} onClick={() => askConfirm(item)}>
          {oneWayLabel(item)}
        </Button>
      ) : null}
    </>
  );
  return (
    <DecisionCard
      compact={compact}
      kind={{ icon: "question", name: count === 1 ? "Question" : "Questions" }}
      project={frame.project}
      title={frame.title}
      provenance={frame.provenance}
      binding={recordsRecommendation ? "Agreeing records the recommendation, its rationale, and its citations as your decision." : "Each answer is recorded once, as yours."}
      actions={
        <>
          {lone ? buttonsOf(lone, false) : null}
          <Button
            variant="primary"
            shortcut="A"
            type={loneOneWay ? "button" : "submit"}
            form={loneOneWay ? undefined : form}
            loading={busy}
            disabled={busy || confirming !== "" || (loneOneWay ? !confirmable(lone) : ready.length === 0)}
            onClick={loneOneWay ? () => askConfirm(lone) : undefined}
          >
            {label}
          </Button>
        </>
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
        {count > 1 ? (
          <div className="row-tight">
            <Button type="button" disabled={busy} onClick={acceptAll}>
              Accept all
            </Button>
          </div>
        ) : null}
        {batch.items.some((item) => item.no_orchestrator) ? <NoOrchestrator what="these questions" /> : null}
        {waiting > 0 ? (
          <p className="small muted" role="status">
            {waiting === 1 ? "1 one-way question waits for your own choice, and is" : `${waiting} one-way questions wait for your own choice, and each is`} recorded on its own once you confirm it.
          </p>
        ) : null}
        {batch.items.map((item) => (
          <AnswerField
            key={item.id}
            id={fieldOf(item)}
            item={item}
            draft={draftOf(item)}
            disabled={busy}
            why
            lead
            open={choosing[item.id] === true}
            extra={
              <>
                {lone ? null : <div className="row-tight question-actions">{buttonsOf(item, true)}</div>}
                {confirming === item.id ? <OneWayConfirm item={item} busy={busy} onCancel={cancelConfirm} onRecord={() => void recordOneWay(item)} /> : null}
              </>
            }
            onChange={(draft) => setDrafts((was) => ({ ...was, [item.id]: draft }))}
          />
        ))}
      </form>
      {failure ? <Refusal>{failure}</Refusal> : null}
    </DecisionCard>
  );
}
