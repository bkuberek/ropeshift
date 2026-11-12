import { useId, useState } from "react";

import { actOnWork, startOrchestrator, workActions } from "../api/client";
import type { OrchestratorAction, WorkAction, WorkNeed } from "../api/types";
import { Link } from "../app/route";
import { onlyAPerson, readWake, WakeWords } from "../floor/WakeWords";
import { Ago } from "../parts/parts";
import { Verdicts } from "../parts/Verdicts";
import type { ButtonVariant } from "../ui/controls";
import { Button } from "../ui/controls";
import { DecisionCard } from "../ui/DecisionCard";
import { ConditionChip, Fact, Facts, Id, LaneBadge, Refusal, TargetChip, WorkBadge } from "../ui/display";
import { Field } from "../ui/forms";
import type { Frame } from "./frame";
import { kindOf } from "./kinds";
import { useAct } from "./useAct";

export { workLabels } from "./kinds";

const reasonWords: Record<string, string> = {
  lapsed: "Its worker stopped renewing its lease, and it may come back.",
  blocked: "Its lane stopped, and no answer to a question would change that.",
  tripped: "Its lane crashed too often, and its breaker stopped it.",
  stalled: "Its lane shows no progress.",
  long_parked: "Its lane has stayed parked for a long time.",
  acceptance: "Its lane finished, and it waits for a member's word.",
  human_resume: "Its lane is parked until a person says go.",
};

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
  start_orchestrator: "Start it again",
};

// The act a card leads with is its primary button, which stands last; canceling never leads.
const leading: (WorkAction | OrchestratorAction)[] = ["accept", "resume", "reset", "start_orchestrator", "respawn", "resolve"];

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
export function WorkCard({ id, work, after, frame, inPage = false }: { id: string; work: WorkNeed; after: () => Promise<void>; frame: Frame; inPage?: boolean }) {
  const [asking, setAsking] = useState<WorkAction | null>(null);
  const [reason, setReason] = useState("");
  const { act, busy, failure } = useAct(after);
  const form = useId();
  const workOrder = work.work_order?.id ?? id;
  const send = async (action: WorkAction, words: string) => {
    if (await act(() => actOnWork(workOrder, action, words))) {
      setAsking(null);
      setReason("");
    }
  };
  const press = (action: WorkAction | OrchestratorAction) => {
    if (action === "start_orchestrator") {
      void act(() => startOrchestrator(work.orchestrator?.worker_id ?? id));
    } else if (workActions[action].needsReason) {
      setAsking(action);
    } else {
      void send(action, "");
    }
  };
  const primary = leading.find((action) => work.actions.includes(action));
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
            <Button variant={asking === "cancel" ? "danger" : "primary"} type="submit" form={form} disabled={busy || reason.trim() === ""}>
              {actionWords[asking]}
            </Button>
          </>
        ) : (
          ordered.map((action) => (
            <Button key={action} variant={variantOf(action, primary)} disabled={busy} onClick={() => press(action)}>
              {actionWords[action]}
            </Button>
          ))
        )
      }
    >
      <Lead work={work} />
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
              <span className={work.lane.freshness === "observed" ? "fresh" : "fresh reported"}>
                {work.lane.freshness === "observed" ? "Observed" : "Last reported"} <Ago at={work.lane.as_of} />
              </span>
            </span>
          </Fact>
          {work.detail ? (
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
            void send(asking, reason.trim());
          }}
        >
          <Field label="Why (required)">
            <textarea className="textarea" rows={2} autoComplete="off" value={reason} disabled={busy} required autoFocus onChange={(event) => setReason(event.target.value)} />
          </Field>
        </form>
      ) : null}
      {failure ? <Refusal>{failure}</Refusal> : null}
    </DecisionCard>
  );
}
