import type { Approval } from "../api/types";

// Decided is what a person sent from a request's card and the ledger recorded: the member the decision was
// recorded for, whether it approved or declined, and the reason given with it.
export type Decided = { by: string; approved: boolean; reason: string };

// decidedOf reads the request the decide route answered. A route that answers nothing leaves what the person
// sent, which the route accepted.
export function decidedOf(recorded: Approval | undefined, sent: { allow: boolean; reason?: string }): Decided {
  return {
    by: recorded?.decided_by ?? "",
    approved: recorded?.state ? recorded.state === "approved" : sent.allow,
    reason: (recorded ? recorded.reason : sent.reason) ?? "",
  };
}
