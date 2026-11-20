import type { Approval, NeedItem } from "../api/types";

// The orders Needs you offers. Importance is the default, and the address keeps any other as `order`.
export type Order = "importance" | "oldest" | "newest";

export const orders: { id: Order; label: string }[] = [
  { id: "importance", label: "Importance, then oldest" },
  { id: "oldest", label: "Oldest first" },
  { id: "newest", label: "Newest first" },
];

export function orderOf(param: string | null): Order {
  return orders.find((row) => row.id === param)?.id ?? "importance";
}

// The tiers of importance, as the API names them: what holds a running session comes first, because a lane waits on
// it, then what stopped work, then what waits for a decision, then what can wait.
export const tierNames: Record<number, string> = {
  1: "Holding a running session",
  2: "Work is stopped",
  3: "Waiting for your decision",
  4: "Later",
};

const decisionTier = 3;

// tierOf is the item's tier. A daemon that states none, an older one, is read as stating a decision.
export function tierOf(item: NeedItem): number {
  return item.tier ?? decisionTier;
}

// permissionKinds are the requests a lane raises to go on: a call to run and an artifact to write.
const permissionKinds = new Set(["tool_call", "artifact"]);

const isPermission = (item: NeedItem): item is Extract<NeedItem, { kind: "approval" }> => item.kind === "approval" && permissionKinds.has(item.approval.kind);

// laneOf is the lane an item belongs to: the lane a permission request came from, or the lane that asked every
// question of a batch. Any other item belongs to no lane, and stands alone.
export function laneOf(item: NeedItem): string {
  if (isPermission(item)) {
    return item.approval.lane_id;
  }
  if (item.kind === "questions") {
    const lanes = new Set(item.batch.items.map((question) => question.lane ?? ""));
    const [only] = lanes;
    return lanes.size === 1 ? (only ?? "") : "";
  }
  return "";
}

// permissionRequests are what Approve all covers in a lane's group: its open permission requests, each bound to its
// own digest. It never covers a merge, a recovery act, a plan, a card, or a question.
export function permissionRequests(items: NeedItem[]): Approval[] {
  return items.filter(isPermission).map((item) => item.approval);
}

// Entry is one row of the list: a lane's items together, or one item that belongs to no lane, with lane empty.
export type Entry = { lane: string; items: NeedItem[] };

// Section is the entries of one tier, or of the whole list when the order is by wait alone, with tier null.
export type Section = { tier: number | null; entries: Entry[] };

function compare(order: Order) {
  return (a: NeedItem, b: NeedItem) => {
    if (order === "newest") {
      return b.since - a.since;
    }
    if (order === "importance" && tierOf(a) !== tierOf(b)) {
      return tierOf(a) - tierOf(b);
    }
    return a.since - b.since;
  };
}

// entriesOf gathers the items of one lane at the place of its first, when grouped, and keeps a lane with one item as
// that item alone, since its own line already names the lane.
function entriesOf(items: NeedItem[], grouped: boolean): Entry[] {
  if (!grouped) {
    return items.map((item) => ({ lane: "", items: [item] }));
  }
  const byLane = new Map<string, NeedItem[]>();
  for (const item of items) {
    const lane = laneOf(item);
    if (lane) {
      byLane.set(lane, [...(byLane.get(lane) ?? []), item]);
    }
  }
  const entries: Entry[] = [];
  const placed = new Set<string>();
  for (const item of items) {
    const lane = laneOf(item);
    const together = lane ? (byLane.get(lane) ?? []) : [];
    if (together.length < 2) {
      entries.push({ lane: "", items: [item] });
    } else if (!placed.has(lane)) {
      placed.add(lane);
      entries.push({ lane, items: together });
    }
  }
  return entries;
}

// arrange is the list as Needs you shows it: sorted in the order chosen, in tiers when the order is importance, with
// each lane's items together when grouped. A stable sort keeps the server's order between equals.
export function arrange(items: NeedItem[], order: Order, grouped: boolean): Section[] {
  const sorted = [...items].sort(compare(order));
  if (order !== "importance") {
    return [{ tier: null, entries: entriesOf(sorted, grouped) }];
  }
  const tiers = [...new Set(sorted.map(tierOf))];
  return tiers.map((tier) => ({ tier, entries: entriesOf(sorted.filter((item) => tierOf(item) === tier), grouped) }));
}

// flatten is every item in the order the list shows them, which J and K and the next item after a decision follow.
export function flatten(sections: Section[]): NeedItem[] {
  return sections.flatMap((section) => section.entries.flatMap((entry) => entry.items));
}
