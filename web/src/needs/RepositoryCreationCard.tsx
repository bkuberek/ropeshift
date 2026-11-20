import { useContext, useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";

import { useChange } from "../admin/parts";
import { inProjectOf, retryRepository, skipRepository } from "../api/client";
import type { RepositoryCreationNeed } from "../api/types";
import { ItemProject } from "../app/route";
import type { StateLook } from "../floor/states";
import { CommandLine, creationLine, plainLine, PublicWarning, StepList, stepRows } from "../planning/creation";
import { Button } from "../ui/controls";
import { DecisionCard, DecisionSection } from "../ui/DecisionCard";
import { Fact, Facts, Refusal, StateBadge } from "../ui/display";
import { Field } from "../ui/forms";
import { Dialog } from "../ui/overlays";
import type { Frame } from "./frame";
import { creationKind } from "./kinds";
import { useAct } from "./useAct";

// The states of a creation that waits for a member, or runs, as the word each reads under.
export const creationStates: Record<string, StateLook> = {
  refused: { label: "Refused", tone: "danger", glyph: "cross" },
  failed: { label: "Failed", tone: "danger", glyph: "cross" },
  interrupted: { label: "Interrupted", tone: "warning", glyph: "pause" },
  running: { label: "Creating now", tone: "active", glyph: "dot" },
};

const reasonLimit = 2000;

// reasonProblem says why a reason for skipping is not one the server takes, as it checks: 1 to 2000 characters once trimmed,
// on one line, with no control character, and no mark that hides or reorders text.
export function reasonProblem(reason: string): string {
  const trimmed = reason.trim();
  if (trimmed === "") {
    return "Say why you skip it.";
  }
  if ([...trimmed].length > reasonLimit) {
    return `The reason is over ${reasonLimit} characters. Shorten it.`;
  }
  if (/[\p{Cc}\p{Cf}\u2028\u2029]/u.test(trimmed)) {
    return "The reason is one line, with no control character or line break.";
  }
  return "";
}

// The command a remedy names, when it is the sentence "run `command`".
const commandOf = (remedy: string) => /^run `([^`]+)`$/.exec(remedy)?.[1] ?? "";

// SkipDialog asks why a member leaves a repository uncreated, and sends the reason the server will take.
function SkipDialog({ need, onClose, after, onSkipped }: { need: RepositoryCreationNeed; onClose: () => void; after: () => Promise<void>; onSkipped: () => void }) {
  const [reason, setReason] = useState("");
  const [shown, setShown] = useState(false);
  const field = useRef<HTMLInputElement>(null);
  const change = useChange();
  const project = useContext(ItemProject);
  const problem = reasonProblem(reason);
  const name = plainLine(need.name);
  const submit = async (event?: FormEvent) => {
    event?.preventDefault();
    if (problem) {
      setShown(true);
      field.current?.focus();
      return;
    }
    const done = await change.run(async () => {
      const sent = reason.trim();
      await (project ? inProjectOf(project, () => skipRepository(need.approval, need.name, sent)) : skipRepository(need.approval, need.name, sent));
      onSkipped();
      await after();
      return true;
    });
    if (done) {
      onClose();
    }
  };
  return (
    <Dialog
      open
      title={`Leave ${name} uncreated?`}
      onClose={onClose}
      initialFocus={field}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" loading={change.busy} onClick={() => void submit()}>
            Skip repository
          </Button>
        </>
      }
    >
      <form className="field-stack" noValidate onSubmit={(event) => void submit(event)}>
        <p className="small">Its work stays in contract review until a later approved version creates it.</p>
        <Field label="Reason (required)" error={shown ? problem : undefined}>
          <input ref={field} className="input" type="text" autoComplete="off" required value={reason} onChange={(event) => setReason(event.target.value)} />
        </Field>
        {change.failure ? <Refusal>{plainLine(change.failure)}</Refusal> : null}
      </form>
    </Dialog>
  );
}

type Props = {
  need: RepositoryCreationNeed;
  after: () => Promise<void>;
  frame: Frame;
  // onResolved says, in words, how the member settled the item from this card, so the page can say why it no longer waits.
  onResolved?: (words: string) => void;
};

// What a state reads as in the sentence that announces a change of it.
const stateWords: Record<RepositoryCreationNeed["state"], string> = { refused: "refused", failed: "failed", interrupted: "interrupted", running: "being created" };

const clock = () => new Date().toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });

// RepositoryCreationCard is a repository an approved plan version creates that was refused, failed, or was interrupted,
// or that runs now (ADR-0039). It lists the steps with each outcome as a word, the reason, the remedy, and the commands that
// finish it by hand, and offers Skip and Retry, which creates the repository from its journal and registers it once every
// step succeeds. A creation that runs offers neither.
export function RepositoryCreationCard({ need, after, frame, onResolved }: Props) {
  const { act, busy, failure } = useAct(after);
  const [skipping, setSkipping] = useState(false);
  // problem is why a run that met an error after it began could not finish, as a retry's answer says it.
  const [problem, setProblem] = useState("");
  // said is what the live region announces when the creation's state changes while the card stands: a read of Needs you moves it.
  const [said, setSaid] = useState("");
  const name = plainLine(need.name);
  const was = useRef(need.state);
  useEffect(() => {
    if (was.current !== need.state) {
      was.current = need.state;
      setSaid(`${name} is now ${stateWords[need.state] ?? plainLine(need.state)}`);
    }
  }, [need.state, name]);
  const offers = (action: RepositoryCreationNeed["actions"][number]) => need.actions.includes(action);
  const running = need.state === "running";
  const retry = () =>
    void act(async () => {
      setProblem("");
      setSaid("");
      const decided = await retryRepository(need.approval, need.name);
      setProblem(plainLine(decided.repositories_problem));
      const entry = decided.repositories?.find((one) => one.name === need.name);
      if (entry?.outcome === "created") {
        onResolved?.(`created by you at ${clock()}`);
      } else if (entry?.outcome === "registered" || entry?.outcome === "known") {
        onResolved?.(`registered by you at ${clock()}`);
      }
    });
  const remedy = plainLine(need.remedy);
  const command = commandOf(remedy);
  const creation = need.creation;
  const steps = stepRows(creation);
  return (
    <DecisionCard
      kind={creationKind}
      project={frame.project}
      title={frame.title}
      provenance={frame.provenance}
      state={<StateBadge table={creationStates} state={need.state} />}
      binding={`Bound to approval ${plainLine(need.approval)}, plan ${plainLine(need.plan)}, version ${need.plan_version}. It leaves this list once the project holds ${name}.`}
      actions={
        running ? undefined : (
          <>
            {offers("repository_skip") ? (
              <Button disabled={busy} data-focus={`Skip creating ${name}`} onClick={() => setSkipping(true)}>
                Skip
              </Button>
            ) : null}
            {offers("repository_retry") ? (
              <Button variant="primary" loading={busy} disabled={busy} data-focus={`Retry creating ${name}`} onClick={retry}>
                Retry
              </Button>
            ) : null}
          </>
        )
      }
    >
      <DecisionSection title="What it changes">
        {running ? (
          <p>
            Another request is creating {name} now. It has no action until that creation finishes.
          </p>
        ) : (
          <p>Retry goes on from the last step done and registers {name} once every step succeeds. Skip leaves it uncreated.</p>
        )}
        <p role="status" className="small">
          {busy ? `Creating ${name}` : said}
        </p>
      </DecisionSection>
      <Facts>
        <Fact name="Repository" mono>
          {name}
        </Fact>
        {creation ? (
          <Fact name="On the forge">
            <span className="row-tight">
              <span>{creationLine(creation)}</span>
              <PublicWarning creation={creation} />
            </span>
          </Fact>
        ) : null}
        <Fact name="Path">
          <code>{plainLine(need.manifest_path)}</code>
        </Fact>
        {need.path ? (
          <Fact name="Leads to">
            <code>{plainLine(need.path)}</code>
          </Fact>
        ) : null}
        {creation?.url ? (
          <Fact name="Remote">
            <code>{plainLine(creation.url)}</code>
          </Fact>
        ) : null}
      </Facts>
      <DecisionSection title="Steps">
        {steps.length ? <StepList creation={creation} label={`Steps of ${name}`} /> : <p className="muted">No step ran. Nothing was written.</p>}
      </DecisionSection>
      {need.reason ? (
        <DecisionSection title="Why it stopped">
          <p>{plainLine(need.reason)}</p>
        </DecisionSection>
      ) : null}
      {remedy ? (
        <DecisionSection title="What to do about it">
          <p>{remedy}</p>
          {command ? <CommandLine command={command} /> : null}
        </DecisionSection>
      ) : null}
      {creation?.remaining?.length ? (
        <DecisionSection title="Commands that finish it by hand">
          <ul className="plain-list" aria-label="Commands that finish it by hand">
            {creation.remaining.map((one, at) => (
              <li key={at}>
                <CommandLine command={one} />
              </li>
            ))}
          </ul>
        </DecisionSection>
      ) : null}
      {failure ? <Refusal>{plainLine(failure)}</Refusal> : null}
      {problem ? <Refusal>{problem}</Refusal> : null}
      {skipping ? <SkipDialog need={need} after={after} onClose={() => setSkipping(false)} onSkipped={() => onResolved?.("you skipped it")} /> : null}
    </DecisionCard>
  );
}
