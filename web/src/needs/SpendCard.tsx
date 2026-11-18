import { useState } from "react";
import type { FormEvent } from "react";

import { blockSpend, raiseSpendLimit, replanSpend, resumeSpend } from "../api/spend";
import type { Narrative, SpendLane, SpendNeed } from "../api/types";
import { Link } from "../app/route";
import { Button } from "../ui/controls";
import { DecisionCard } from "../ui/DecisionCard";
import { Fact, Facts, Refusal } from "../ui/display";
import { Field } from "../ui/forms";
import type { Frame } from "./frame";
import { allowanceBasisWords, amountError, bandWords, largestLimit, limitBasisWords, money, shareWords, spendKind } from "./spendWords";
import { useAct } from "./useAct";

const plural = (count: number, noun: string) => `${count} ${noun}${count === 1 ? "" : "s"}`;

// A narrative part is a list of what the agent wrote, or the word that it wrote nothing. An agent may leave a list out,
// so a missing one reads as empty.
function Part({ name, lines }: { name: string; lines: string[] | null | undefined }) {
  const written = lines ?? [];
  return (
    <Fact name={name}>
      {written.length ? (
        <ul className="criteria">
          {written.map((line, at) => (
            <li key={at}>{line}</li>
          ))}
        </ul>
      ) : (
        <span className="muted">nothing</span>
      )}
    </Fact>
  );
}

function Written({ narrative }: { narrative: Narrative }) {
  return (
    <Facts label="What the lane wrote at its handoff">
      <Part name="Done" lines={narrative.done} />
      <Part name="In progress" lines={narrative.in_progress} />
      <Part name="Next steps" lines={narrative.next_steps} />
      <Part name="Risks" lines={narrative.risks} />
    </Facts>
  );
}

// digestWords says what a lane's last digest counted, in a line.
function digestWords(lane: SpendLane): string {
  const digest = lane.digest;
  if (!digest) {
    return "no digest";
  }
  // A stored digest may lack either collection, so a missing one counts nothing.
  const calls = Object.values(digest.tool_calls ?? {}).reduce((sum, count) => sum + count, 0);
  const files = (digest.files_written ?? []).length + (digest.files_written_more ?? 0);
  return [plural(digest.turns, "turn"), plural(calls, "tool call"), plural(files, "file") + " written", digest.complete ? "" : "may be incomplete"].filter(Boolean).join(" · ");
}

// LaneLeft is what one lane of the stopped work order left: its last digest and its last handoff, which holds the
// narrative its agent wrote, or says that the handoff degraded to what the ledger knew.
function LaneLeft({ lane }: { lane: SpendLane }) {
  const handoff = lane.handoff;
  return (
    <li>
      <details className="disclosure">
        <summary>
          Lane <span className="mono">{lane.lane}</span>
          {lane.generation ? `, generation ${lane.generation}` : ""}: {digestWords(lane)}
        </summary>
        <div className="stack-8">
          <p className="small">
            <Link to={`/lane?id=${encodeURIComponent(lane.lane)}`}>Open lane {lane.lane}</Link>
          </p>
          {handoff?.narrative ? (
            <Written narrative={handoff.narrative} />
          ) : handoff ? (
            <p className="muted">{`Its handoff in generation ${handoff.generation} degraded, so it has no narrative: only what the ledger knew was kept.`}</p>
          ) : (
            <p className="muted">It left no handoff.</p>
          )}
        </div>
      </details>
    </li>
  );
}

// SpendCard is a work order a hard spend limit parked (ADR-0037, FR-SPEND-7): one item however many lanes served it.
// It shows what the work order is charged against its limits and what each lane left, and offers the five ways out:
// raise the limit by half or by all of it, or to an amount, let one more generation go on, block the work with the
// reason spend_limit, or send it to replanning. Each is a member's act and none starts an agent by itself except the
// two that lift the limit, which wake the lane it parked.
export function SpendCard({ need, after, frame }: { need: SpendNeed; after: () => Promise<void>; frame: Frame }) {
  const { act, busy, failure } = useAct(after);
  const [amount, setAmount] = useState("");
  const id = need.work_order.id;
  const spend = need.spend;
  const problem = amountError(amount, spend);
  const canAmount = amount.trim() !== "" && problem === "";
  const offers = (action: SpendNeed["actions"][number]) => need.actions.includes(action);
  const raiseTo = (event: FormEvent) => {
    event.preventDefault();
    if (canAmount) {
      void act(() => raiseSpendLimit(id, { to_usd: Number(amount.trim()) }));
    }
  };
  return (
    <DecisionCard
      kind={spendKind}
      project={frame.project}
      title={frame.title}
      provenance={frame.provenance}
      binding={`Bound to the hard limit of ${money(spend.hard_limit)}. A raise that lifts it past what is charged wakes the lane.`}
      actions={
        <>
          {offers("spend_block") ? (
            <Button variant="danger-outline" disabled={busy} onClick={() => void act(() => blockSpend(id))}>
              Block
            </Button>
          ) : null}
          {offers("spend_replan") ? (
            <Button disabled={busy} onClick={() => void act(() => replanSpend(id))}>
              Send to replanning
            </Button>
          ) : null}
          {offers("spend_raise") ? (
            <>
              <Button disabled={busy} onClick={() => void act(() => raiseSpendLimit(id, { by_percent: 50 }))}>
                Raise by 50 percent
              </Button>
              <Button disabled={busy} onClick={() => void act(() => raiseSpendLimit(id, { by_percent: 100 }))}>
                Raise by 100 percent
              </Button>
            </>
          ) : null}
          {offers("spend_resume") ? (
            <Button variant="primary" loading={busy} disabled={busy} onClick={() => void act(() => resumeSpend(id))}>
              Resume once
            </Button>
          ) : null}
        </>
      }
    >
      <p>This work order reached its hard spend limit, so its lane handed off and parked. Nothing runs on it until you choose a way out.</p>
      <Facts>
        <Fact name="Work order">
          <Link to={`/work-order?id=${encodeURIComponent(id)}`}>{need.work_order.title || id}</Link>
        </Fact>
        <Fact name="Charged">
          {money(spend.charged.charged_usd)}
          <span className="muted small">{` ${spend.label}`}</span>
        </Fact>
        <Fact name="Soft limit">{spend.soft_limit > 0 ? money(spend.soft_limit) : "none"}</Fact>
        <Fact name="Hard limit">
          {money(spend.hard_limit)}
          {spend.raised_to ? " (raised by a member)" : ""}
        </Fact>
        <Fact name="Remaining">{money(spend.remaining)}</Fact>
        <Fact name="Cost band">{bandWords(need.band)}</Fact>
        <Fact name="Limits from">{spend.basis ? limitBasisWords[spend.basis] : "not recorded"}</Fact>
        <Fact name="Waiting share">{shareWords(need.waiting_share)}</Fact>
      </Facts>
      {need.resume ? (
        <p className="help">
          {`Resume once lets one more generation spend ${money(need.resume.allowance_usd)}, sized by ${allowanceBasisWords[need.resume.basis]}, and sets the hard limit to ${money(need.resume.limit_usd)}.`}
        </p>
      ) : (
        <p className="help">
          Nothing sizes one more generation for this work order, so Resume once is not offered. Raise its limit instead.
        </p>
      )}
      {offers("spend_raise") ? (
        <form className="reason" onSubmit={raiseTo}>
          <Field
            label="Raise the hard limit to (US dollars)"
            help={`Above the ${money(Math.max(spend.hard_limit, spend.charged.charged_usd))} in force, and at most ${money(largestLimit)}.`}
            error={problem}
          >
            <input className="input mono field-narrow" inputMode="decimal" autoComplete="off" spellCheck={false} value={amount} disabled={busy} onChange={(event) => setAmount(event.target.value)} />
          </Field>
          <Button type="submit" disabled={busy || !canAmount}>
            Raise to this amount
          </Button>
        </form>
      ) : null}
      <section aria-label="What the lanes left">
        <h3 className="label">{plural(need.lanes.length, "lane")} served this work order</h3>
        {need.lanes.length ? (
          <ul className="criteria">
            {need.lanes.map((lane) => (
              <LaneLeft key={lane.lane} lane={lane} />
            ))}
          </ul>
        ) : (
          <p className="muted">No lane is recorded for it.</p>
        )}
      </section>
      {failure ? <Refusal>{failure}</Refusal> : null}
    </DecisionCard>
  );
}
