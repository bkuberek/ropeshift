import { useState } from "react";

import { actOnWork, startOrchestrator, workActions } from "../api/client";
import type { OrchestratorAction, WorkAction, WorkNeed } from "../api/types";
import { Freshness } from "../floor/LaneFacts";
import { laneLabel } from "../floor/states";
import { onlyAPerson, readWake, WakeWords } from "../floor/WakeWords";
import { Id } from "../parts/parts";
import { useAct } from "./useAct";

export const workLabels: Record<string, string> = {
  acceptance: "Accept",
  blocked: "Blocked",
  human_resume: "Parked",
  lapsed: "Lapsed",
  long_parked: "Long parked",
  stalled: "Stalled",
  tripped: "Tripped",
};

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

const actionWords: Record<WorkAction | OrchestratorAction, string> = {
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

// Lead says why work waits. A parked lane that waits on more than a person says what, such as the
// question it asked, so a person can answer that instead of resuming it, and resume stays the way past.
function Lead({ work }: { work: WorkNeed }) {
  const said = (work.orchestrator ? orchestratorWords[work.reason] : reasonWords[work.reason]) ?? work.reason;
  const wake = work.wakes?.[0];
  const term = wake ? readWake(wake.expression) : null;
  if (!wake || !term || onlyAPerson(term)) {
    return <p className="lead">{said}</p>;
  }
  return (
    <>
      <p className="lead">
        {work.reason === "human_resume" ? "Its lane is parked" : `${said} It waits`} <WakeWords expression={wake.expression} word="until" capital={false} />.
      </p>
      {work.actions.includes("resume") ? <p className="quiet">Resume lets it go on now, without waiting.</p> : null}
    </>
  );
}

// Some answers change who owns work, so they say why. Those ask only after the press, and an action
// that needs no words goes in one press.
export function WorkCard({ id, work, after }: { id: string; work: WorkNeed; after: () => Promise<void> }) {
  const [asking, setAsking] = useState<WorkAction | null>(null);
  const [reason, setReason] = useState("");
  const { act, busy, failure } = useAct(after);
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
  return (
    <div className="card-body">
      <Lead work={work} />
      {work.orchestrator ? (
        <dl className="facts">
          <div>
            <dt>Worker</dt>
            <dd>
              <Id value={work.orchestrator.worker_id} />
            </dd>
          </div>
        </dl>
      ) : null}
      {work.lane ? (
        <dl className="facts">
          <div>
            <dt>Lane</dt>
            <dd>
              <Id value={work.lane.id} />
            </dd>
          </div>
          <div>
            <dt>State</dt>
            <dd>
              {laneLabel(work.lane.actual_state)}
              <Freshness freshness={work.lane.freshness} at={work.lane.as_of} />
            </dd>
          </div>
          {work.detail ? (
            <div>
              <dt>Lane said</dt>
              <dd className="mono">{work.detail}</dd>
            </div>
          ) : null}
        </dl>
      ) : null}
      {asking ? (
        <form
          className="reason"
          onSubmit={(event) => {
            event.preventDefault();
            void send(asking, reason.trim());
          }}
        >
          <label className="field">
            <span className="field-label">Why</span>
            <input type="text" value={reason} disabled={busy} required autoFocus onChange={(event) => setReason(event.target.value)} />
          </label>
          <div className="actions">
            <button className="button button-strong" type="submit" disabled={busy || reason.trim() === ""}>
              {actionWords[asking]}
            </button>
            <button className="button" type="button" disabled={busy} onClick={() => setAsking(null)}>
              Cancel
            </button>
          </div>
        </form>
      ) : (
        <div className="actions">
          {work.actions.map((action) => (
            <button
              key={action}
              className="button"
              type="button"
              disabled={busy}
              onClick={() => press(action)}
            >
              {actionWords[action]}
            </button>
          ))}
        </div>
      )}
      {failure ? (
        <p className="refusal" role="alert">
          {failure}
        </p>
      ) : null}
    </div>
  );
}
