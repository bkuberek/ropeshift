import type { NeedItem, Operations, PlanProgress, Roadmap, ShopFloor } from "../api/types";
import { addressOf } from "../app/route";
import { counting } from "../floor/states";

// What Overview says of a project is what the read models say: the figures are counted from them, and nothing
// here decides what needs a member or how far work got.

// inThisProject says an item of the combined Needs you is of the project shown. An item the server labeled with no
// project is of the only project there is.
const inThisProject = (project: string) => (item: NeedItem) => !item.project || item.project.name === project;

// Tile is one key figure. A value of null says the figure has none, and none says why, in words: a figure is
// never drawn as zero where no source gave one.
export type Tile = { id: string; label: string; value: string | null; none: string; note: string; to: string };

const unread = "Not read yet";

export type TileInput = { project: string; needs?: NeedItem[]; floor?: ShopFloor; operations?: Operations };

// dollars says money in dollars and cents. A cost too small to show in cents says so, and does not read as nothing.
function dollars(value: number): string {
  return value > 0 && value < 0.005 ? "under $0.01" : `$${value.toFixed(2)}`;
}

// costOf sums the estimated cost of every outcome of the window. A sum is a lower bound when any part of it is,
// and it is no figure when no lane reported a cost. The status bar says the same figure.
export function costOf(operations: Operations): string | null {
  const costs = Object.values(operations.measures.cost_per_outcome);
  const total = costs.reduce((sum, cost) => sum + cost.total_usd, 0);
  if (total === 0) {
    return null;
  }
  return `${costs.some((cost) => cost.lower_bound) ? "at least " : ""}${dollars(total)}`;
}

// tilesOf is the six key figures of a project's Overview, each linking to the view that holds what it counts.
export function tilesOf({ project, needs, floor, operations }: TileInput): Tile[] {
  const measures = operations?.measures;
  const ratio = measures?.interruptions_per_completed;
  const window = measures ? `last ${measures.window.name}` : "";
  return [
    {
      id: "waiting",
      label: "Waiting on you",
      value: needs ? String(needs.filter(inThisProject(project)).length) : null,
      none: unread,
      note: "in this project",
      to: addressOf("/needs-you", { project }),
    },
    {
      id: "lanes",
      label: "Lanes running",
      value: floor ? String(floor.lanes.filter((lane) => counting.has(lane.actual_state)).length) : null,
      none: unread,
      note: "counting against the caps",
      to: "/floor",
    },
    {
      id: "review",
      label: "In review or merging",
      value: operations ? String((operations.work_orders.acceptance ?? 0) + (operations.work_orders.integrating ?? 0)) : null,
      none: unread,
      note: "waiting for acceptance or a merge",
      to: "/reviews",
    },
    { id: "done", label: "Done in 7 days", value: measures ? String(measures.completed) : null, none: unread, note: window, to: addressOf("/work", { group: "completed" }) },
    {
      id: "interruptions",
      label: "Interruptions per completed work order",
      value: ratio && ratio.value !== null ? String(Math.round(ratio.value * 100) / 100) : null,
      none: ratio ? "No data yet" : unread,
      note: window ? `interruptions over completed work orders, ${window}` : "interruptions over completed work orders",
      to: "/measures",
    },
    {
      id: "cost",
      label: "Estimated cost this week",
      value: operations ? costOf(operations) : null,
      none: operations ? "None reported" : unread,
      note: window ? `${window}, from what agents reported` : "",
      to: "/usage",
    },
  ];
}

// currentPlan is the plan Overview shows the milestones of: among the plans that have milestones, one with a version
// a member approved and work left, else one with work left, else the first. approved maps a plan to its approved
// version, which the plans view holds.
export function currentPlan(roadmap: Roadmap, approved: Map<string, number> = new Map()): PlanProgress | undefined {
  const plans = roadmap.goals.flatMap((group) => group.plans).filter((row) => row.milestones.length > 0);
  const left = plans.filter((row) => row.done < row.total);
  return left.find((row) => (approved.get(row.plan.id) ?? 0) > 0) ?? left[0] ?? plans[0];
}

// LaneRow is a lane that has not ended, with the work it runs and the machine it runs on.
export type LaneRow = { id: string; title: string; state: string; conditions: string[]; machine: string };

// lanesOf lists the lanes of the floor that have not ended, in the floor's order, each on its machine by name.
export function lanesOf(floor: ShopFloor): LaneRow[] {
  const names = new Map(floor.workers.map((line) => [line.worker.id, line.worker.name]));
  return floor.lanes
    .filter((lane) => lane.actual_state !== "ended")
    .map((lane) => ({
      id: lane.id,
      title: lane.work_order_title || lane.work_order,
      state: lane.actual_state,
      conditions: lane.conditions ?? [],
      machine: lane.worker_id ? (names.get(lane.worker_id) ?? lane.worker_id) : "",
    }));
}

// The most items of Needs you the side column lists.
export const topCount = 5;

// topNeeds is what waits on the member in this project, the longest wait first, cut to the top five.
export function topNeeds(items: NeedItem[], project: string): NeedItem[] {
  return items
    .filter(inThisProject(project))
    .sort((a, b) => a.since - b.since)
    .slice(0, topCount);
}
