import { describe, expect, it } from "vitest";

import type { PlanProgress, PlannedWork } from "../api/types";
import { golden } from "../testing";
import { layersByMilestone, noMilestone, waitingVersions } from "./roadmap";

const order = (id: string, milestone?: string): PlannedWork => ({ id, title: id, state: "ready", milestone });

function plan(layers: string[][], work: PlannedWork[], critical: string[] = []): PlanProgress {
  return {
    plan: { id: "p", title: "P", status: "active" },
    total: work.length,
    done: 0,
    states: {},
    milestones: [],
    layers,
    critical_path: { work_orders: critical, weight: critical.length },
    work,
  };
}

describe("a milestone's work by dependency layer", () => {
  const row = plan(
    [["a", "b"], ["c", "d"], ["e"], ["f"]],
    [order("a", "m1"), order("b", "m2"), order("c", "m2"), order("d", "m1"), order("e", "m2"), order("f", "m2")],
    ["a", "c", "e", "f"],
  );

  it("keeps the work of that milestone in the plan's layers, in the order work can start", () => {
    const layers = layersByMilestone(row, "m2");
    expect(layers.map((layer) => [layer.layer, layer.work.map((work) => work.id)])).toEqual([
      [1, ["b"]],
      [2, ["c"]],
      [3, ["e"]],
      [4, ["f"]],
    ]);
  });

  it("leaves out a layer the milestone has no work in, and names the layers the plan numbers", () => {
    const layers = layersByMilestone(row, "m1");
    expect(layers.map((layer) => [layer.layer, layer.work.map((work) => work.id)])).toEqual([
      [1, ["a"]],
      [2, ["d"]],
    ]);
  });

  it("marks the work on the critical path, which is the plan's and not the milestone's", () => {
    expect(layersByMilestone(row, "m2").flatMap((layer) => layer.work.filter((work) => work.critical).map((work) => work.id))).toEqual(["c", "e", "f"]);
    expect(layersByMilestone(row, "m1").flatMap((layer) => layer.work.filter((work) => work.critical).map((work) => work.id))).toEqual(["a"]);
  });

  it("gathers work no milestone holds under the empty milestone", () => {
    const loose = plan([["a"], ["b"]], [order("a"), order("b", "m1")]);
    expect(layersByMilestone(loose, noMilestone).map((layer) => layer.work.map((work) => work.id))).toEqual([["a"]]);
  });

  it("names a work order the layers lack by its own layer after the last, so none goes missing", () => {
    const stray = plan([["a"]], [order("a", "m1"), order("z", "m1")]);
    expect(layersByMilestone(stray, "m1").map((layer) => [layer.layer, layer.work.map((work) => work.id)])).toEqual([
      [1, ["a"]],
      [2, ["z"]],
    ]);
  });
});

describe("a version that waits", () => {
  it("is named for each plan whose approval waits, with the version, and no plan with nothing waiting", () => {
    const plans = golden<{ plans: { plan: { id: string; title: string }; pending_version?: number; approved_version?: number }[] }>("plans").plans;
    const waiting = waitingVersions(plans);
    expect(waiting).toHaveLength(1);
    expect(waiting[0]).toMatchObject({ title: "The factory floor", version: 6 });
    expect(waiting[0]?.approved).toBeGreaterThan(0);
  });
});
