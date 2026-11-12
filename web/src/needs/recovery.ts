import type { Approval, RecoveryDetail } from "../api/types";

// The words for each act an orchestrator can ask a member to allow.
const acts: Record<string, string> = {
  lane_restart: "restart the lane of",
  lane_reset: "reset the breaker of",
  work_retry: "retry",
  work_replan: "send back to planning",
};

export function detailOf(approval: Approval): Partial<RecoveryDetail> {
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
