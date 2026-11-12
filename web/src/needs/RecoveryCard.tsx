import { decideApproval } from "../api/client";
import type { Approval, RecoveryDetail } from "../api/types";
import { Id } from "../parts/parts";
import { useAct } from "./useAct";

// The words for each act an orchestrator can ask a member to allow.
const acts: Record<string, string> = {
  lane_restart: "restart the lane of",
  lane_reset: "reset the breaker of",
  work_retry: "retry",
  work_replan: "send back to planning",
};

function detailOf(approval: Approval): Partial<RecoveryDetail> {
  try {
    return JSON.parse(approval.detail ?? "{}") as Partial<RecoveryDetail>;
  } catch {
    return {};
  }
}

// recoveryHeading says what approving the request does.
export function recoveryHeading(approval: Approval): string {
  return `Let the orchestrator ${acts[approval.subject] ?? approval.subject} ${approval.work_order_id}?`;
}

// A recovery request is an act an orchestrator's policy did not allow. Approving it runs that act as this
// member's decision, on the subject this page read: a work order or lane that moved since makes it void.
export function RecoveryCard({ approval, after }: { approval: Approval; after: () => Promise<void> }) {
  const detail = detailOf(approval);
  const { act, busy, failure } = useAct(after);
  const decide = (allow: boolean) => act(() => decideApproval(approval.id, approval.subject_digest, allow, ""));
  return (
    <div className="card-body">
      <dl className="facts">
        <div>
          <dt>Why the orchestrator asks</dt>
          <dd>{detail.reason ?? ""}</dd>
        </div>
        <div>
          <dt>The policy limit it went past</dt>
          <dd>{detail.limit ?? ""}</dd>
        </div>
        <div>
          <dt>Work order</dt>
          <dd>
            <Id value={approval.work_order_id} />
          </dd>
        </div>
        {approval.lane_id ? (
          <div>
            <dt>Lane</dt>
            <dd>
              <Id value={approval.lane_id} />
            </dd>
          </div>
        ) : null}
        <div>
          <dt>Asked by</dt>
          <dd className="mono">{approval.requested_by}</dd>
        </div>
      </dl>
      {failure ? (
        <p className="refusal" role="alert">
          {failure}
        </p>
      ) : null}
      <div className="actions">
        <button className="button button-strong" type="button" disabled={busy} onClick={() => void decide(true)}>
          Approve
        </button>
        <button className="button" type="button" disabled={busy} onClick={() => void decide(false)}>
          Decline
        </button>
      </div>
    </div>
  );
}
