import { describe, expect, it } from "vitest";

import type { NeedsYou, Operations, Roadmap, ShopFloor } from "../api/types";
import { golden } from "../testing";
import { currentPlan, lanesOf, tilesOf, topNeeds } from "./figures";

const operations = () => golden<Operations>("operations");
const floor = () => golden<ShopFloor>("shop-floor");
const needs = () => golden<NeedsYou>("needs-you");
const tile = (id: string, over: Partial<Parameters<typeof tilesOf>[0]> = {}) => tilesOf({ project: "default", needs: needs().items, floor: floor(), operations: operations(), ...over }).find((found) => found.id === id);

describe("the key figures", () => {
  it("are the six the design names, each linking to its view", () => {
    const tiles = tilesOf({ project: "default", needs: needs().items, floor: floor(), operations: operations() });
    expect(tiles.map((found) => [found.label, found.to])).toEqual([
      ["Waiting on you", "/needs-you?project=default"],
      ["Lanes running", "/floor"],
      ["In review or merging", "/reviews"],
      ["Done in 7 days", "/work?group=completed"],
      ["Interruptions per completed work order", "/measures"],
      ["Estimated cost this week", "/usage"],
    ]);
  });

  it("counts what waits on the member in this project", () => {
    expect(tile("waiting")?.value).toBe("12");
    const mixed = needs().items.map((item, at) => ({ ...item, project: { id: at < 5 ? "default" : "other", name: at < 5 ? "default" : "other" } }));
    expect(tile("waiting", { needs: mixed })?.value).toBe("5");
  });

  it("counts the lanes that run, not the ones that ended, parked, or tripped", () => {
    expect(tile("lanes")?.value).toBe("5");
  });

  it("counts the work in acceptance and integrating, from the work orders by state", () => {
    const measured = operations();
    measured.work_orders = { acceptance: 2, integrating: 3, running: 4 };
    expect(tile("review", { operations: measured })?.value).toBe("5");
  });

  it("reads the work done and the interruptions from the measures of the week", () => {
    expect(tile("done")?.value).toBe("2");
    expect(tile("interruptions")?.value).toBe("1");
    const measured = operations();
    measured.measures.interruptions_per_completed = { numerator: 3, denominator: 2, value: 1.5 };
    expect(tile("interruptions", { operations: measured })?.value).toBe("1.5");
  });

  // The figure is interruptions over the work orders completed in the window, and its note says so in the same words.
  it("notes the interruptions as a ratio over the work orders completed in the window", () => {
    expect(tile("interruptions")?.note).toBe("interruptions over completed work orders, last 7d");
  });

  it("says no data where a rate counted nothing, and never zero", () => {
    const measured = operations();
    measured.measures.interruptions_per_completed = { numerator: 0, denominator: 0, value: null };
    expect(tile("interruptions", { operations: measured })?.value).toBeNull();
  });

  it("sums the cost of the week, as a lower bound when any part is one, and none where no lane reported a cost", () => {
    expect(tile("cost")?.value).toBeNull();
    const measured = operations();
    measured.measures.cost_per_outcome = {
      change: { total_usd: 1.234, done: 2, per_done_usd: 0.617, lower_bound: false },
      review: { total_usd: 0.5, done: 1, per_done_usd: 0.5, lower_bound: true },
    };
    expect(tile("cost", { operations: measured })?.value).toBe("at least $1.73");
    measured.measures.cost_per_outcome = { change: { total_usd: 0.001, done: 1, per_done_usd: 0.001, lower_bound: false } };
    expect(tile("cost", { operations: measured })?.value).toBe("under $0.01");
  });

  it("have no value for a read that has not come, and say so", () => {
    const tiles = tilesOf({ project: "default", needs: undefined, floor: undefined, operations: undefined });
    expect(tiles.map((found) => found.value)).toEqual([null, null, null, null, null, null]);
    expect(tiles.map((found) => found.none)).toEqual(Array(6).fill("Not read yet"));
  });

  it("say what a figure that counted nothing has instead of a value", () => {
    expect(tile("interruptions", { operations: { ...operations(), measures: { ...operations().measures, interruptions_per_completed: { numerator: 0, denominator: 0, value: null } } } })?.none).toBe("No data yet");
    expect(tile("cost")?.none).toBe("None reported");
  });
});

describe("the plan", () => {
  const roadmap = () => golden<Roadmap>("roadmap");

  it("is the plan with milestones that still has work left", () => {
    expect(currentPlan(roadmap())?.plan.id).toBe("generated-7");
  });

  it("is nothing where no plan has a milestone", () => {
    const view = roadmap();
    for (const group of view.goals) {
      for (const row of group.plans) {
        row.milestones = [];
      }
    }
    expect(currentPlan(view)).toBeUndefined();
  });

  it("prefers a plan with an approved version among those with work left", () => {
    const view = roadmap();
    const first = view.goals[0]?.plans[0];
    if (!first) {
      throw new Error("no plan");
    }
    const other = structuredClone(first);
    other.plan.id = "other";
    view.goals[0]?.plans.push(other);
    expect(currentPlan(view)?.plan.id).toBe("generated-7");
    expect(currentPlan(view, new Map([["other", 1]]))?.plan.id).toBe("other");
    expect(currentPlan(view, new Map([["other", 1], ["generated-7", 2]]))?.plan.id).toBe("generated-7");
  });

  it("is never a plan whose work is all done while another has work left, and is the first when none has", () => {
    const view = roadmap();
    const first = view.goals[0]?.plans[0];
    if (!first) {
      throw new Error("no plan");
    }
    const finished = structuredClone(first);
    finished.plan.id = "finished";
    finished.done = finished.total;
    view.goals[0]?.plans.unshift(finished);
    expect(currentPlan(view, new Map([["finished", 1]]))?.plan.id).toBe("generated-7");
    first.done = first.total;
    expect(currentPlan(view)?.plan.id).toBe("finished");
  });
});

describe("the lanes", () => {
  it("lists the lanes that have not ended, with the work they run and the machine's name", () => {
    expect(lanesOf(floor()).map((row) => [row.id, row.title, row.state, row.machine])).toEqual([
      ["lane-wo-approval", "Tag the release", "working", "solo"],
      ["lane-wo-effect", "Add the retry budget", "working", "solo"],
      ["lane-wo-lapsed", "Rotate the keys", "working", "solo"],
      ["lane-wo-parked", "Wait for the check", "parked", "solo"],
      ["lane-wo-running", "Write the docs", "working", "solo"],
      ["lane-wo-stalled", "Read the logs", "working", "solo"],
      ["lane-wo-tripped", "Fix the flaky test", "tripped", "solo"],
    ]);
  });

  it("falls back to the machine's ID when it has no name", () => {
    const view = floor();
    view.workers = [];
    expect(lanesOf(view)[0]?.machine).toBe("worker-a");
  });
});

describe("what waits on the member", () => {
  it("is the top five, the longest wait first", () => {
    const top = topNeeds(needs().items, "default");
    expect(top).toHaveLength(5);
    expect(top.map((item) => item.since)).toEqual([...top.map((item) => item.since)].sort((a, b) => a - b));
  });

  it("leaves out what waits in another project", () => {
    const elsewhere = needs().items.map((item) => ({ ...item, project: { id: "other", name: "other" } }));
    expect(topNeeds(elsewhere, "default")).toEqual([]);
  });
});
