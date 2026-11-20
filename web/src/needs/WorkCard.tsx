import { useId, useState } from "react";

import { actOnWork, startOrchestrator, workActions } from "../api/client";
import type { OrchestratorAction, WorkAction, WorkNeed, WorkOrder } from "../api/types";
import { Link } from "../app/route";
import { retryNote } from "../floor/recoverSpecs";
import { RetryNote } from "../floor/RetryNote";
import { onlyAPerson, readWake, WakeWords } from "../floor/WakeWords";
import { Verdicts } from "../parts/Verdicts";
import type { ButtonVariant } from "../ui/controls";
import { Button } from "../ui/controls";
import { DecisionCard } from "../ui/DecisionCard";
import { ConditionChip, Fact, Facts, Freshness, freshnessOf, Id, LaneBadge, Refusal, TargetChip, WorkBadge } from "../ui/display";
import { Field } from "../ui/forms";
import type { Frame } from "./frame";
import { kindOf } from "./kinds";
import { PersonalSetupChoices } from "./PersonalSetup";
import { useAct } from "./useAct";

export { workLabels } from "./kinds";

const reasonWords: Record<string, string> = {
  lapsed: "Its worker stopped renewing its lease, and it may come back.",
  blocked: "Its lane stopped, and no answer to a question would change that.",
  tripped: "Its lane crashed too often, and its breaker stopped it.",
  stalled: "Its lane shows no progress.",
  long_parked: "Its lane has stayed parked for a long time.",
  failed: "Its lane failed, and nothing retries failed work on its own.",
  acceptance: "Its lane finished, and it waits for a member's word.",
  human_resume: "Its lane is parked until a person says go.",
  worker_behind: "Its worker must be upgraded before it can start its lane.",
  history_missing: "Its adoption branch merged without the design history it imported.",
  history_unread: "Its adoption branch merged, and its base could not be checked for the design history it imported.",
  personal_setup: "Its lane's session loaded your own agent CLI setup, despite the flags that keep it out of lanes.",
};

// The reasons whose detail is the card's own words, shown under its lead rather than as what a lane said.
const toldReasons = ["worker_behind", "history_missing", "history_unread", "personal_setup"];

const orchestratorWords: Record<string, string> = {
  tripped: "This orchestrator crashed too often, and its breaker stopped it. Questions wait until it runs.",
  stalled: "This orchestrator shows no progress. Questions wait until it runs.",
};

export const actionWords: Record<WorkAction | OrchestratorAction, string> = {
  reassign: "End this claim",
  resolve: "Back to ready",
  replan: "Back to planning",
  reset: "Reset the breaker",
  respawn: "Start the next generation",
  resume: "Resume",
  accept: "Accept",
  rework: "Send back for rework",
  cancel: "Cancel this work",
  retry: "Retry",
  start_orchestrator: "Start it again",
};

// A retry of work that used every attempt it was given says that it adds one.
const wordsOf = (action: WorkAction | OrchestratorAction, work: WorkNeed) => (action === "retry" && work.extra_attempt ? "Retry with an extra attempt" : actionWords[action]);

// The act a card leads with is its primary button, which stands last; canceling never leads.
const leading: (WorkAction | OrchestratorAction)[] = ["accept", "resume", "reset", "start_orchestrator", "respawn", "resolve", "retry"];

function variantOf(action: WorkAction | OrchestratorAction, primary: WorkAction | OrchestratorAction | undefined): ButtonVariant {
  if (action === primary) {
    return "primary";
  }
  return action === "cancel" ? "danger-outline" : "secondary";
}

// Lead says why work waits. A parked lane that waits on more than a person says what, such as the
// question it asked, so a person can answer that instead of resuming it, and resume stays the way past.
function Lead({ work }: { work: WorkNeed }) {
  const said = (work.orchestrator ? orchestratorWords[work.reason] : reasonWords[work.reason]) ?? work.reason;
  const wake = work.wakes?.[0];
  const term = wake ? readWake(wake.expression) : null;
  if (!wake || !term || onlyAPerson(term)) {
    return (
      <>
        <p>{said}</p>
        {toldReasons.includes(work.reason) && work.detail ? <p className="muted">{work.detail}</p> : null}
        {work.reason === "failed" && work.extra_attempt ? <p className="muted">It used every attempt it was given, so a retry adds one, which only a member can do.</p> : null}
        {work.standing?.why ? <p className="muted">It waits because {work.standing.why}.</p> : null}
      </>
    );
  }
  return (
    <>
      <p>
        {work.reason === "human_resume" ? "Its lane is parked" : `${said} It waits`} <WakeWords expression={wake.expression} word="until" capital={false} />.
      </p>
      {work.actions.includes("resume") ? <p className="muted">Resume lets it go on now, without waiting.</p> : null}
    </>
  );
}

// Some answers change who owns work, so they say why. Those ask only after the press, and an action
// that needs no words goes in one press.
// inPage is a card on its own work order's page, which shows the review in a tab and needs no link to itself.
// onRetried is told when a member's retry returned the work to ready, with the work order as it now stands, so the
// page can say that no lane has started and offer Start.
export function WorkCard({
  id,
  work,
  after,
  frame,
  inPage = false,
  onRetried,
}: {
  id: string;
  work: WorkNeed;
  after: () => Promise<void>;
  frame: Frame;
  inPage?: boolean;
  onRetried?: (order: WorkOrder) => void;
}) {
  const [asking, setAsking] = useState<WorkAction | null>(null);
  const [reason, setReason] = useState("");
  const { act, busy, failure } = useAct(after);
  const form = useId();
  const workOrder = work.work_order?.id ?? id;
  // A retry is told from its reply, before the read after it, which no longer lists the work.
  const send = async (action: WorkAction, words: string) => {
    if (
      await act(async () => {
        const reply = await actOnWork(workOrder, action, words);
        const state = (reply as { state?: string } | undefined)?.state;
        if (action === "retry" && state === "ready" && work.work_order) {
          onRetried?.({ ...work.work_order, state });
        }
      })
    ) {
      setAsking(null);
      setReason("");
    }
  };
  const press = (action: WorkAction | OrchestratorAction) => {
    if (action === "start_orchestrator") {
      void act(() => startOrchestrator(work.orchestrator?.worker_id ?? id));
    } else if (workActions[action].needsReason) {
      if (action === "rework" && work.send_back_reason) {
        setReason(work.send_back_reason);
      }
      setAsking(action);
    } else {
      void send(action, "");
    }
  };
  // A plan ropeshift held back as unfinished leads with Send back, because accepting it would accept what is missing.
  const unfinished = work.unfinished ?? [];
  const primary = unfinished.length > 0 && work.actions.includes("rework") ? "rework" : leading.find((action) => work.actions.includes(action));
  const ordered = [...work.actions.filter((action) => action === "cancel"), ...work.actions.filter((action) => action !== "cancel" && action !== primary), ...(primary ? [primary] : [])];
  return (
    <DecisionCard
      kind={kindOf({ kind: "work", work, id, since: 0 })}
      project={frame.project}
      title={frame.title}
      provenance={frame.provenance}
      state={work.work_order && !inPage ? <WorkBadge state={work.work_order.state} /> : undefined}
      actions={
        asking ? (
          <>
            <Button disabled={busy} onClick={() => setAsking(null)}>
              Cancel
            </Button>
            <Button variant={asking === "cancel" ? "danger" : "primary"} type="submit" form={form} disabled={busy || (asking !== "retry" && reason.trim() === "")}>
              {wordsOf(asking, work)}
            </Button>
          </>
        ) : (
          ordered.map((action) => (
            <Button key={action} variant={variantOf(action, primary)} disabled={busy} onClick={() => press(action)}>
              {wordsOf(action, work)}
            </Button>
          ))
        )
      }
    >
      <Lead work={work} />
      {unfinished.length > 0 ? (
        <>
          <p>Ropeshift did not offer this plan for approval, because it is not finished. Still missing:</p>
          <ul>
            {unfinished.map((line) => (
              <li key={line}>{line}</li>
            ))}
          </ul>
        </>
      ) : null}
      {work.reason === "personal_setup" ? <PersonalSetupChoices work={work} /> : null}
      {work.standing && !inPage ? <Verdicts standing={work.standing} /> : null}
      {work.orchestrator ? (
        <Facts>
          <Fact name="Orchestrator">
            <TargetChip kind="orchestrator" small>
              Orchestrator · {work.orchestrator.worker_id}
            </TargetChip>
          </Fact>
          <Fact name="Worker">
            <Id value={work.orchestrator.worker_id} />
          </Fact>
        </Facts>
      ) : null}
      {work.work_order && !inPage ? (
        <p className="small">
          <Link to={`/work-order?id=${encodeURIComponent(work.work_order.id)}`}>Open work order {work.work_order.id}</Link>
        </p>
      ) : null}
      {work.lane ? (
        <Facts>
          <Fact name="Lane">
            <Id value={work.lane.id} />
          </Fact>
          <Fact name="State">
            <span className="row-tight">
              <LaneBadge state={work.lane.actual_state} />
              {(work.lane.conditions ?? []).map((condition) => (
                <ConditionChip key={condition} name={condition} />
              ))}
              <Freshness kind={freshnessOf(work.lane.freshness)} at={work.lane.as_of} source="the lane's worker" />
            </span>
          </Fact>
          {work.detail && !toldReasons.includes(work.reason) ? (
            <Fact name="Lane said" mono>
              {work.detail}
            </Fact>
          ) : null}
        </Facts>
      ) : null}
      {asking ? (
        <form
          id={form}
          className="reason"
          onSubmit={(event) => {
            event.preventDefault();
            void send(asking, asking === "retry" ? retryNote(reason) : reason.trim());
          }}
        >
          {asking === "retry" ? (
            <RetryNote cause={work.detail} label="Why" note={reason} disabled={busy} autoFocus onChange={setReason} />
          ) : (
            <Field label="Why (required)">
              <textarea className="textarea" rows={2} autoComplete="off" value={reason} disabled={busy} required autoFocus onChange={(event) => setReason(event.target.value)} />
            </Field>
          )}
        </form>
      ) : null}
      {failure ? <Refusal>{failure}</Refusal> : null}
    </DecisionCard>
  );
}
