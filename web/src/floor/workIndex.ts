import { readView } from "../api/client";
import type { Completed, CompletedWork, HeldLane, InProgress, Lane, MilestoneProgress, OnHold, PlanDetail, Plans, Stamp, Stuck, StuckItem, WorkOrder } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import type { WorkGroup } from "./states";
import { workStates } from "./states";

// WorkRow is one work order and what the read models say of it: since when it stands in its state, its
// lane, what a parked lane waits for, why it stopped, how its review stands, and its plan and milestone.
export type WorkRow = {
  order: WorkOrder;
  since?: number;
  lane?: Lane;
  held?: HeldLane;
  stuck?: StuckItem;
  completed?: CompletedWork;
  plan?: PlanDetail;
  milestone?: MilestoneProgress;
};

export type WorkIndex = { rows: WorkRow[]; stamp: Stamp; readAt: number };

const groupOrder: WorkGroup[] = ["open", "in-progress", "on-hold", "stuck", "review", "completed"];

// groupOf is the tab a work order stands in. Stuck work is what the stuck read model lists, whatever
// state it is in, because the server decides what is stuck.
export function groupOf(row: WorkRow): WorkGroup {
  if (row.stuck) {
    return "stuck";
  }
  return workStates[row.order.state]?.home ?? "open";
}

// index joins the five read models by work order. Each list is the server's, so the order within a
// group is the server's too: the longest wait first.
export function index(plans: Plans, running: InProgress, held: OnHold, stuck: Stuck, completed: Completed): WorkRow[] {
  const rows = new Map<string, WorkRow>();
  const row = (order: WorkOrder) => {
    const found = rows.get(order.id) ?? { order };
    found.order = order;
    rows.set(order.id, found);
    return found;
  };
  for (const detail of plans.plans) {
    for (const order of detail.work_orders) {
      const at = row(order);
      at.plan = detail;
    }
  }
  for (const order of plans.unplanned) {
    row(order);
  }
  for (const line of running.items) {
    Object.assign(row(line.work_order), { since: line.since, lane: line.lane });
  }
  for (const line of held.items) {
    Object.assign(row(line.work_order), { since: line.since, lane: line.lane, held: line });
  }
  for (const line of completed.items) {
    Object.assign(row(line.work_order), { since: line.since, completed: line });
  }
  for (const item of stuck.items) {
    if (item.work_order) {
      Object.assign(row(item.work_order), { since: item.since, lane: item.lane ?? rows.get(item.work_order.id)?.lane, stuck: item });
    }
  }
  for (const found of rows.values()) {
    const contract = found.plan?.contracts.find((history) => history.id === found.order.contract_id);
    const milestoneId = contract?.versions[contract.versions.length - 1]?.milestone;
    found.milestone = found.plan?.milestones.find((milestone) => milestone.id === milestoneId);
  }
  const all = [...rows.values()];
  return groupOrder.flatMap((group) => all.filter((found) => groupOf(found) === group));
}

// useWorkIndex reads the five read models that together say where every work order stands.
export function useWorkIndex(live: Live): { index: WorkIndex | null; failure: string; busy: boolean } {
  const plans = useView<Plans>(() => readView<Plans>("plans"), "work:plans", live);
  const running = useView<InProgress>(() => readView<InProgress>("in-progress"), "work:in-progress", live);
  const held = useView<OnHold>(() => readView<OnHold>("on-hold"), "work:on-hold", live);
  const stuck = useView<Stuck>(() => readView<Stuck>("stuck"), "work:stuck", live);
  const completed = useView<Completed>(() => readView<Completed>("completed"), "work:completed", live);
  const reads = [plans, running, held, stuck, completed];
  const failure = reads.find((read) => read.failure)?.failure ?? "";
  const busy = reads.some((read) => read.busy);
  if (!plans.view || !running.view || !held.view || !stuck.view || !completed.view) {
    return { index: null, failure, busy };
  }
  const oldest = reads.reduce((min, read) => Math.min(min, read.readAt), Number.POSITIVE_INFINITY);
  return {
    index: { rows: index(plans.view, running.view, held.view, stuck.view, completed.view), stamp: { as_of: completed.view.as_of, revision: completed.view.revision }, readAt: oldest },
    failure,
    busy,
  };
}
