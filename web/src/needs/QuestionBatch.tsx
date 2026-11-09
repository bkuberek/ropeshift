import { useId, useState } from "react";

import { ApiError, answerQuestion } from "../api/client";
import type { Batch, QuestionItem } from "../api/types";
import { useAct } from "./useAct";

const ownWords = "__own_words__";

type Draft = { choice: string; words: string };

// A recommendation is ready to send, so a batch that needs only agreement takes one press. One that is
// none of the options waits in the box for a person's own words, and that choice starts chosen, so what
// a person sees chosen is what a send counts and sends. A one-way choice is why the question reached a
// person, so it starts with nothing chosen.
function startingDraft(item: QuestionItem): Draft {
  const options = item.options ?? [];
  const recommendation = item.recommendation ?? "";
  const oneWay = item.escalation_reason === "one_way";
  if (recommendation !== "" && options.includes(recommendation)) {
    return { choice: oneWay ? "" : recommendation, words: "" };
  }
  if (options.length === 0) {
    return { choice: ownWords, words: oneWay ? "" : recommendation };
  }
  return { choice: oneWay || recommendation === "" ? "" : ownWords, words: recommendation };
}

// apartFromOptions says a recommendation stands in the box for a person's own words.
function apartFromOptions(item: QuestionItem): boolean {
  return (item.recommendation ?? "") !== "" && !(item.options ?? []).includes(item.recommendation ?? "");
}

function answerOf(draft: Draft): string {
  return (draft.choice === ownWords ? draft.words : draft.choice).trim();
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
    <label className={checked ? "choice choice-on" : "choice"}>
      <input type="radio" name={name} value={value} checked={checked} onChange={() => onPick(value)} />
      <span className="choice-text">{label}</span>
      {recommended ? <span className="choice-note">Recommended</span> : null}
    </label>
  );
}

function Question({ item, draft, onChange, disabled }: {
  item: QuestionItem;
  draft: Draft;
  disabled: boolean;
  onChange: (draft: Draft) => void;
}) {
  const name = useId();
  const options = item.options ?? [];
  return (
    <fieldset className="question" disabled={disabled}>
      <legend className="question-text">{item.text}</legend>
      <div className="tags">
        {item.blocking ? <span className="tag tag-strong">Blocks work</span> : null}
        {(item.categories ?? []).map((category) => (
          <span className="tag" key={category}>
            {category}
          </span>
        ))}
      </div>
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
      {draft.choice === ownWords ? (
        <label className="field">
          <span className="field-label">Your answer</span>
          <textarea rows={3} value={draft.words} onChange={(event) => onChange({ ...draft, words: event.target.value })} />
        </label>
      ) : null}
      {(item.citations ?? []).length > 0 ? (
        <p className="quiet">
          Considered: <span className="mono">{(item.citations ?? []).join(", ")}</span>
        </p>
      ) : null}
    </fieldset>
  );
}

export function QuestionBatch({ batch, after }: { batch: Batch; after: () => Promise<void> }) {
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [leftover, setLeftover] = useState("");
  const { act, busy, failure } = useAct(after);
  const draftOf = (item: QuestionItem) => drafts[item.id] ?? startingDraft(item);
  const ready = batch.items.filter((item) => answerOf(draftOf(item)) !== "");
  const count = batch.items.length;

  // One question that someone else answered first leaves the others to send, so one refusal does not
  // stop the rest, and each send is safe to repeat.
  const send = () =>
    act(async () => {
      const refused: string[] = [];
      for (const item of ready) {
        try {
          await answerQuestion(item.id, answerOf(draftOf(item)));
        } catch (error) {
          if (!(error instanceof ApiError) || error.code !== "resolved") {
            refused.push(error instanceof Error ? error.message : String(error));
          }
        }
      }
      setLeftover(refused.join(" "));
    });

  return (
    <form
      className="card-body"
      onSubmit={(event) => {
        event.preventDefault();
        void send();
      }}
    >
      {batch.items.map((item) => (
        <div className="question-block" key={item.id}>
          <Question item={item} draft={draftOf(item)} disabled={busy} onChange={(draft) => setDrafts((was) => ({ ...was, [item.id]: draft }))} />
        </div>
      ))}
      {failure || leftover ? (
        <p className="refusal" role="alert">
          {failure || leftover}
        </p>
      ) : null}
      <div className="actions">
        <button className="button button-strong" type="submit" disabled={busy || ready.length === 0}>
          {ready.length === count ? (count === 1 ? "Send this answer" : `Send ${count} answers`) : `Send ${ready.length} of ${count} answers`}
        </button>
      </div>
    </form>
  );
}
