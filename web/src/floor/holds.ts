// The reasons the ledger holds the start of ready work for, which `internal/workorders/start.go` names, and
// the short phrase each reads as. `holds.test.ts` holds this list to the ledger's, so a reason added there
// fails until it has a phrase here.
export type HoldReason =
  | "waiting_for_merge"
  | "dependencies_diverge"
  | "base_unavailable"
  | "no_checkout"
  | "continued_branch_missing"
  | "pull_request_pending"
  | "forge_unavailable"
  | "decision";

export const holdWords: Record<HoldReason, string> = {
  waiting_for_merge: "Waiting for a merge",
  dependencies_diverge: "Dependencies on separate lines",
  base_unavailable: "Base branch not readable",
  no_checkout: "No clone on the worker",
  continued_branch_missing: "Branch to continue is missing",
  pull_request_pending: "Waiting for the pull request",
  forge_unavailable: "Forge sign-in unavailable",
  decision: "Waiting for a decision to be read",
};

// holdWord is the phrase for a reason. A reason this build has no phrase for shows as its own name.
export function holdWord(reason: string): string {
  return reason in holdWords ? holdWords[reason as HoldReason] : reason;
}
