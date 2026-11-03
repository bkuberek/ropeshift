import { useState } from "react";

import { actOnWork, workActions } from "../api/client";
import type { WorkAction, WorkNeed } from "../api/types";
import { Freshness } from "../floor/LaneFacts";
import { laneLabel } from "../floor/states";
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

const actionWords: Record<WorkAction, string> = {
  reassign: "End this claim",
  resolve: "Back to ready",
  replan: "Back to planning",
  reset: "Reset the breaker",
  respawn: "Start the next generation",
  resume: "Resume",
  accept: "Accept",
  rework: "Send back for rework",
};

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
  return (
    <div className="card-body">
      <p className="lead">{reasonWords[work.reason] ?? work.reason}</p>
      {work.lane ? (
        <dl className="facts">
          <div>
            <dt>Lane</dt>
            <dd className="mono">{work.lane.id}</dd>
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
              onClick={() => (workActions[action].needsReason ? setAsking(action) : void send(action, ""))}
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
