import { describe, expect, it } from "vitest";

import { laneRank, splitLanes } from "./laneOrder";

const row = (id: string, state: string) => ({ id, lane: { actual_state: state } });

describe("the order of a machine's lanes", () => {
  it("ranks an idle lane with the lanes that run, before parked ones", () => {
    expect(laneRank("idle")).toBeLessThan(laneRank("parked"));
    expect(laneRank("idle")).toBe(laneRank("working"));
  });

  it("lists live lanes by rank in the server's order, and sets ended lanes apart", () => {
    const { live, ended } = splitLanes([row("a", "ended"), row("b", "parked"), row("c", "idle"), row("d", "needs_decision"), row("e", "working"), row("f", "tripped")]);
    expect(live.map((one) => one.id)).toEqual(["c", "e", "d", "b", "f"]);
    expect(ended.map((one) => one.id)).toEqual(["a"]);
  });
});
