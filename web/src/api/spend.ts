import { call, project } from "./client";
import type { SpendLimitChange, SpendStopEnded, WorkOrderSpend } from "./types";

// What a member reads and does at a work order's spend limits (ADR-0037), as `ropeshift spend` does. A limit in US
// dollars at list price is a limit of 0 when there is none.

const route = (id: string, rest = "") => project(`/work-orders/${encodeURIComponent(id)}/spend${rest}`);

// readWorkOrderSpend reads one work order's charged spend against its soft and hard limits, as `ropeshift spend
// --work-order` prints it.
export function readWorkOrderSpend(id: string): Promise<WorkOrderSpend> {
  return call("GET", route(id));
}

// How a raise names the limit it asks for: a percentage of the limit in force, or an amount.
export type Raise = { by_percent: number } | { to_usd: number };

// raiseSpendLimit lifts a work order's hard limit and wakes the lane the limit parked, as `ropeshift spend raise` does,
// and answers the limit before and after.
export function raiseSpendLimit(id: string, raise: Raise): Promise<SpendLimitChange> {
  return call("POST", route(id, "/raise"), raise);
}

// resumeSpend grants a work order one more generation's allowance, as `ropeshift spend resume` does, and answers the
// limit before and after.
export function resumeSpend(id: string): Promise<SpendLimitChange> {
  return call("POST", route(id, "/resume"), {});
}

// blockSpend blocks a work order a hard limit parked, with the reason spend_limit, as `ropeshift spend block` does.
export function blockSpend(id: string): Promise<SpendStopEnded> {
  return call("POST", route(id, "/block"), {});
}

// replanSpend sends a work order a hard limit parked to planning, as `ropeshift spend replan` does.
export function replanSpend(id: string): Promise<SpendStopEnded> {
  return call("POST", route(id, "/replan"), {});
}
