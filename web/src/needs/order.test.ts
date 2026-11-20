import { describe, expect, it } from "vitest";

import type { Approval, NeedItem, QuestionItem } from "../api/types";
import { arrange, flatten, gateRequests, laneOf, orderOf, permissionRequests, tierOf } from "./order";

const approval = (id: string, lane: string, since: number, tier = 1, kind = "tool_call", gate = false): NeedItem => ({
  kind: "approval",
  id,
  since,
  tier,
  approval: { id, lane_id: lane, work_order_id: "wo-1", generation: 1, kind, subject: `call ${id}`, subject_digest: `d-${id}`, summary: "", requested_by: lane, state: "open", gate } as Approval,
});

const question = (id: string, lane: string): QuestionItem => ({ id, text: `Question ${id}`, lane, asked_by: lane });

const batch = (id: string, since: number, tier: number, items: QuestionItem[]): NeedItem => ({
  kind: "questions",
  id,
  since,
  tier,
  batch: { batch: { id, status: "delivered", opened_at: 0, closes_at: 0 }, items },
});

const plan = (id: string, since: number): NeedItem => ({ kind: "plan_approval", id, since, tier: 3 }) as NeedItem;

const ids = (items: NeedItem[]) => items.map((item) => item.id);

describe("the order of Needs you", () => {
  const items = [plan("plan-old", 1), approval("a-new", "L-1", 30), batch("b-mid", 20, 3, [question("q1", "L-2")]), approval("a-old", "L-1", 10)];

  it("reads its order from the address, importance when it names none or one it does not know", () => {
    expect(orderOf(null)).toBe("importance");
    expect(orderOf("newest")).toBe("newest");
    expect(orderOf("oldest")).toBe("oldest");
    expect(orderOf("loudest")).toBe("importance");
  });

  it("puts what holds a running session first, then the oldest of each tier", () => {
    const sections = arrange(items, "importance", false);
    expect(sections.map((section) => section.tier)).toEqual([1, 3]);
    expect(ids(flatten(sections))).toEqual(["a-old", "a-new", "plan-old", "b-mid"]);
  });

  it("orders by wait alone, oldest or newest first, with no tiers", () => {
    expect(arrange(items, "oldest", false).map((section) => section.tier)).toEqual([null]);
    expect(ids(flatten(arrange(items, "oldest", false)))).toEqual(["plan-old", "a-old", "b-mid", "a-new"]);
    expect(ids(flatten(arrange(items, "newest", false)))).toEqual(["a-new", "b-mid", "a-old", "plan-old"]);
  });

  it("takes an item a daemon read states no tier for as a decision", () => {
    const { tier, ...untiered } = plan("plan-x", 1);
    expect(tier).toBe(3);
    expect(tierOf(untiered as NeedItem)).toBe(3);
  });
});

describe("grouping by lane", () => {
  it("gathers a lane's items at the place of its first, and leaves an item of no lane alone", () => {
    const items = [approval("a1", "L-1", 1), approval("b1", "L-2", 2), approval("a2", "L-1", 3), plan("p", 4)];
    const [section] = arrange(items, "oldest", true);
    expect(section?.entries.map((entry) => [entry.lane, ids(entry.items)])).toEqual([
      ["L-1", ["a1", "a2"]],
      ["", ["b1"]],
      ["", ["p"]],
    ]);
  });

  it("groups within a tier, never across one", () => {
    const sections = arrange([approval("a1", "L-1", 1), approval("a2", "L-1", 2, 2, "recovery"), approval("a3", "L-1", 3)], "importance", true);
    expect(sections.map((section) => [section.tier, section.entries.map((entry) => ids(entry.items))])).toEqual([
      [1, [["a1", "a3"]]],
      [2, [["a2"]]],
    ]);
  });

  it("takes a batch's lane only when every question in it came from that lane", () => {
    expect(laneOf(batch("b", 1, 3, [question("q1", "L-1"), question("q2", "L-1")]))).toBe("L-1");
    expect(laneOf(batch("b", 1, 3, [question("q1", "L-1"), question("q2", "L-2")]))).toBe("");
    expect(laneOf(batch("b", 1, 3, [{ id: "q", text: "Asked by a member" }]))).toBe("");
    expect(laneOf(plan("p", 1))).toBe("");
  });

  it("approves all of a lane's permission requests, and no other kind of request", () => {
    const entry = [approval("a1", "L-1", 1), approval("m", "L-1", 2, 3, "merge"), approval("a2", "L-1", 3, 1, "artifact"), batch("b", 4, 1, [question("q", "L-1")])];
    expect(permissionRequests(entry).map((row) => row.id)).toEqual(["a1", "a2"]);
  });

  // A gate or a push asks in every mode by design, so a batch never covers one: each waits for its own review.
  it("leaves every gate out of what Approve all covers", () => {
    const entry = [approval("a1", "L-1", 1), approval("push", "L-1", 2, 1, "tool_call", true), approval("a2", "L-1", 3)];
    expect(permissionRequests(entry).map((row) => row.id)).toEqual(["a1", "a2"]);
    expect(gateRequests(entry).map((row) => row.id)).toEqual(["push"]);
  });
});
