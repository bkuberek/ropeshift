import { screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { Lane, Plans, ShopFloor } from "./api/types";
import { conditionWords, laneStates, workStates } from "./floor/states";
import { openPage } from "./opening";
import { golden, serveViews } from "./testing";

// What `TestFixtureCoversEveryState` wrote from a real ledger: both state tables as the ledger holds
// them, and every read model of a ledger with one thing in each state. A state added to a table fails
// the Go test until a walk reaches it, and fails here until a view has words for it and shows it.
type Fixture = {
  work_order_states: string[];
  lane_states: string[];
  conditions: string[];
  liveness: string[];
  reach: string[];
  freshness: string[];
  views: Record<string, unknown>;
};

const fixture = golden<Fixture>("every-state");
const floor = fixture.views["shop-floor"] as ShopFloor;
const plans = fixture.views.plans as Plans;

function serveFixture() {
  return serveViews((call) => {
    const name = /^\/v1\/views\/([a-z-]+)/.exec(call.path)?.[1];
    return name ? { status: 200, body: fixture.views[name] } : undefined;
  });
}

// Where a person finds a work order in a state: a plan's own page for work that has one, the list of
// work with no plan for work that has none, and otherwise the view the state's row names.
function homeOf(state: string): string {
  const home = workStates[state]?.home;
  if (home !== "plans") {
    return `/${home}`;
  }
  const plan = plans.plans.find((row) => row.work_orders.some((order) => order.id === `wo-${state}`));
  return plan ? `/plans?plan=${encodeURIComponent(plan.plan.id)}` : "/plans";
}

// The queue stays mounted behind every view, so a row counts only if a person can see it.
function rowOf(id: string): Promise<HTMLElement> {
  return waitFor(() => {
    const row = screen.getAllByText(id).find((element) => !element.closest("[hidden]"))?.closest("article, .row");
    if (!row) {
      throw new Error(`${id} stands in no row a person can see`);
    }
    return row as HTMLElement;
  });
}

const laneIn = (state: string): Lane => {
  const lane = floor.lanes.find((row) => row.id === `lane-wl-${state}`);
  if (!lane) {
    throw new Error(`no lane in ${state}`);
  }
  return lane;
};

describe("every work order state", () => {
  it.each(fixture.work_order_states)("%s has words and a view that shows it", async (state) => {
    const meaning = workStates[state];
    expect(meaning, `no row in workStates for ${state}`).toBeDefined();
    serveFixture();
    openPage(homeOf(state));
    const row = await rowOf(`wo-${state}`);
    expect(within(row).getAllByText(meaning?.label ?? "").length).toBeGreaterThan(0);
  });

  it.each(fixture.work_order_states)("%s stands in the operations count", async (state) => {
    serveFixture();
    openPage("/operations");
    const table = await screen.findByRole("table", { name: "Work orders by state" });
    const row = within(table).getByRole("row", { name: new RegExp(`^${workStates[state]?.label ?? state} \\d+$`) });
    expect(row).toBeTruthy();
  });

  it("has no words for a state the ledger dropped", () => {
    expect(Object.keys(workStates).sort()).toEqual([...fixture.work_order_states].sort());
  });
});

describe("every lane state", () => {
  it.each(fixture.lane_states)("%s has words and stands on the shop floor", async (state) => {
    const meaning = laneStates[state];
    expect(meaning, `no row in laneStates for ${state}`).toBeDefined();
    serveFixture();
    openPage("/shop-floor");
    const row = await rowOf(`lane-wl-${state}`);
    expect(within(row).getByText(meaning ?? "")).toBeTruthy();
  });

  it.each(fixture.lane_states)("%s says how fresh it is", async (state) => {
    serveFixture();
    openPage("/shop-floor");
    const lane = laneIn(state);
    const side = await screen.findByRole("region", { name: lane.freshness === "observed" ? "Observed lanes" : "Last reported lanes" });
    const row = await rowOf(lane.id);
    expect(side.contains(row)).toBe(true);
    expect(row.textContent).toContain(lane.freshness === "observed" ? "Observed" : "Last reported");
  });

  it.each(fixture.lane_states)("%s stands in the operations count", async (state) => {
    serveFixture();
    openPage("/operations");
    const table = await screen.findByRole("table", { name: "Lanes by state" });
    const row = within(table).getByRole("row", { name: new RegExp(`^${laneStates[state] ?? state} \\d+$`) });
    expect(row).toBeTruthy();
  });

  it("has no words for a state the ledger dropped", () => {
    expect(Object.keys(laneStates).sort()).toEqual([...fixture.lane_states].sort());
  });
});

describe("what stands beside a lane's state", () => {
  it.each(fixture.conditions)("%s has words and shows on its lane", async (condition) => {
    const said = conditionWords[condition];
    expect(said, `no words for ${condition}`).toBeDefined();
    serveFixture();
    openPage("/shop-floor");
    const lane = floor.lanes.find((row) => row.conditions?.includes(condition));
    expect(lane, `no lane holds ${condition}`).toBeDefined();
    expect(within(await rowOf(lane?.id ?? "")).getByText(said ?? "")).toBeTruthy();
  });

  it.each(fixture.liveness)("liveness %s shows on a lane", async (liveness) => {
    serveFixture();
    openPage("/shop-floor");
    const lane = floor.lanes.find((row) => row.liveness === liveness);
    expect(lane, `no lane reads ${liveness}`).toBeDefined();
    const row = await rowOf(lane?.id ?? "");
    expect(within(row).getByText("Liveness").closest("div")?.textContent?.toLowerCase()).toContain(liveness);
  });

  it.each(fixture.reach)("reach %s shows on a lane", async (reach) => {
    serveFixture();
    openPage("/shop-floor");
    const lane = floor.lanes.find((row) => row.model_reach === reach && row.freshness === "last_reported") ?? floor.lanes.find((row) => row.model_reach === reach);
    expect(lane, `no lane reads ${reach}`).toBeDefined();
    const row = await rowOf(lane?.id ?? "");
    expect(within(row).getByText("Model").closest("div")?.textContent?.toLowerCase()).toContain(reach);
  });

  it("holds both kinds of freshness in the fixture", () => {
    expect(new Set(floor.lanes.map((lane) => lane.freshness))).toEqual(new Set(fixture.freshness));
  });
});
