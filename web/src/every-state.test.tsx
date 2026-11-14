import { screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { Lane, ShopFloor, Stuck } from "./api/types";
import { cardStates, conditionWords, conversationStates, deliveryStates, delegateStates, laneStates, workStates } from "./floor/states";
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
  card_states: string[];
  delegate_states: string[];
  conversation_states: string[];
  message_states: string[];
  views: Record<string, unknown>;
};

const fixture = golden<Fixture>("every-state");
const floor = fixture.views["shop-floor"] as ShopFloor;
const stuck = fixture.views.stuck as Stuck;

function serveFixture() {
  return serveViews((call) => {
    const name = /^\/v1\/views\/([a-z-]+)/.exec(call.path)?.[1];
    return name ? { status: 200, body: fixture.views[name] } : undefined;
  });
}

// Where a person finds a work order in a state: the tab of Work orders its state's row names, or Stuck
// when the server lists it as stuck, whatever its state.
function homeOf(state: string): string {
  const listedStuck = stuck.items.some((item) => item.work_order?.id === `wo-${state}`);
  return `/work?group=${listedStuck ? "stuck" : workStates[state]?.home}`;
}

// The queue stays mounted behind every view, so a row counts only if a person can see it.
function rowOf(id: string): Promise<HTMLElement> {
  return waitFor(() => {
    const row = screen.getAllByText(id).find((element) => !element.closest("[hidden]"))?.closest("article, tr, .list-row");
    if (!row) {
      throw new Error(`${id} stands in no row a person can see`);
    }
    return row as HTMLElement;
  });
}

// cellOf reads one column of a table row, by the name the column gives each cell.
function cellOf(row: HTMLElement, column: string): string {
  return row.querySelector(`[data-label="${column}"]`)?.textContent ?? "";
}

const laneIn = (state: string): Lane => {
  const lane = floor.lanes.find((row) => row.id === `lane-wl-${state}`);
  if (!lane) {
    throw new Error(`no lane in ${state}`);
  }
  return lane;
};

describe("every work order state", () => {
  it.each(fixture.work_order_states)("%s has words, a tone, a glyph, and a tab that shows it", async (state) => {
    const meaning = workStates[state];
    expect(meaning, `no row in workStates for ${state}`).toBeDefined();
    serveFixture();
    openPage(homeOf(state));
    const row = await rowOf(`wo-${state}`);
    const badge = within(row)
      .getAllByText(meaning?.label ?? "")
      .map((element) => element.closest(".badge"))
      .find(Boolean);
    expect(badge?.getAttribute("data-tone")).toBe(meaning?.tone);
    expect(badge?.querySelector("svg")?.getAttribute("data-glyph")).toBe(meaning?.glyph);
  });

  it.each(fixture.work_order_states)("%s opens on its own work order page", async (state) => {
    serveFixture();
    openPage(`/work-order?id=wo-${state}`);
    expect(await screen.findByRole("heading", { level: 1, name: new RegExp(`^wo-${state}`) })).toBeTruthy();
    expect(within(screen.getByRole("list", { name: "Lifecycle" })).getByText(workStates[state]?.label ?? state)).toBeTruthy();
  });

  it.each(fixture.work_order_states)("%s stands in the operations count", async (state) => {
    serveFixture();
    openPage("/measures");
    const table = await screen.findByRole("table", { name: "Work orders by state" });
    const row = within(table).getByRole("row", { name: new RegExp(`^${workStates[state]?.label ?? state} \\d+$`) });
    expect(row).toBeTruthy();
  });

  it("has no words for a state the ledger dropped", () => {
    expect(Object.keys(workStates).sort()).toEqual([...fixture.work_order_states].sort());
  });
});

describe("every lane state", () => {
  it.each(fixture.lane_states)("%s has words, a tone, and a glyph, and stands on the floor", async (state) => {
    const meaning = laneStates[state];
    expect(meaning, `no row in laneStates for ${state}`).toBeDefined();
    serveFixture();
    openPage("/floor");
    const row = await rowOf(`lane-wl-${state}`);
    const badge = within(row).getByText(meaning?.label ?? "").closest(".badge");
    expect(badge?.getAttribute("data-tone")).toBe(meaning?.tone);
    expect(badge?.querySelector("svg")?.getAttribute("data-glyph")).toBe(meaning?.glyph);
  });

  it.each(fixture.lane_states)("%s says how fresh it is", async (state) => {
    serveFixture();
    openPage("/floor");
    const lane = laneIn(state);
    const row = await rowOf(lane.id);
    expect(cellOf(row, "Freshness")).toMatch(lane.freshness === "observed" ? /^Observed/ : /^Last reported/);
  });

  it.each(fixture.lane_states)("%s stands in the operations count", async (state) => {
    serveFixture();
    openPage("/measures");
    const table = await screen.findByRole("table", { name: "Lanes by state" });
    const row = within(table).getByRole("row", { name: new RegExp(`^${laneStates[state]?.label ?? state} \\d+$`) });
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
    openPage("/floor");
    const lane = floor.lanes.find((row) => row.conditions?.includes(condition));
    expect(lane, `no lane holds ${condition}`).toBeDefined();
    expect(within(await rowOf(lane?.id ?? "")).getByText(said ?? "")).toBeTruthy();
  });

  it.each(fixture.liveness)("liveness %s shows on a lane", async (liveness) => {
    serveFixture();
    openPage("/floor");
    const lane = floor.lanes.find((row) => row.liveness === liveness);
    expect(lane, `no lane reads ${liveness}`).toBeDefined();
    const row = await rowOf(lane?.id ?? "");
    expect(cellOf(row, "Liveness").toLowerCase().replaceAll(" ", "_")).toContain(liveness);
  });

  it.each(fixture.reach)("reach %s shows on a lane", async (reach) => {
    serveFixture();
    openPage("/floor");
    const lane = floor.lanes.find((row) => row.model_reach === reach && row.freshness === "last_reported") ?? floor.lanes.find((row) => row.model_reach === reach);
    expect(lane, `no lane reads ${reach}`).toBeDefined();
    const row = await rowOf(lane?.id ?? "");
    expect(cellOf(row, "Reach").toLowerCase().replaceAll(" ", "_")).toContain(`model_${reach}`);
  });

  it("holds both kinds of freshness in the fixture", () => {
    expect(new Set(floor.lanes.map((lane) => lane.freshness))).toEqual(new Set(fixture.freshness));
  });
});

// A confirmation card's states and a delegate credential's, as the API lists them: each has words here,
// and none here is a state the API dropped.
describe("every card and delegate state", () => {
  it("has words for each state a card passes through, and no other", () => {
    expect(Object.keys(cardStates).sort()).toEqual([...fixture.card_states].sort());
    for (const state of fixture.card_states) {
      expect(cardStates[state]?.label, state).toBeTruthy();
    }
  });

  it("has words for each state of a delegate credential, and no other", () => {
    expect(Object.keys(delegateStates).sort()).toEqual([...fixture.delegate_states].sort());
  });
});

// An assistant's session states and a message's delivery states, as the API lists them: each has words, a
// tone, and a glyph here, and none here is a state the API dropped.
describe("every session and delivery state", () => {
  it("has words for each state of an assistant's session, and no other", () => {
    expect(Object.keys(conversationStates).sort()).toEqual([...fixture.conversation_states].sort());
    for (const state of fixture.conversation_states) {
      expect(conversationStates[state]?.label, state).toBeTruthy();
    }
  });

  it("has the tone and glyph the design gives each session state", () => {
    const look = (state: string) => `${conversationStates[state]?.tone} ${conversationStates[state]?.glyph}`;
    expect(look("starting")).toBe("active dot");
    expect(look("running")).toBe("active dot");
    expect(look("working")).toBe("active half");
    expect(look("stopping")).toBe("neutral ring");
    expect(look("stopped")).toBe("neutral pause");
    expect(look("failed")).toBe("danger cross");
    expect(conversationStates.failed?.label).toBe("Could not start");
  });

  it("has words for each delivery state of a message, and no other", () => {
    expect(Object.keys(deliveryStates).sort()).toEqual([...fixture.message_states].sort());
    for (const state of fixture.message_states) {
      expect(deliveryStates[state]?.label, state).toBeTruthy();
    }
  });

  it("has the tone and glyph the design gives each delivery state", () => {
    const look = (state: string) => `${deliveryStates[state]?.tone} ${deliveryStates[state]?.glyph}`;
    expect(look("queued")).toBe("neutral ring");
    expect(look("at_worker")).toBe("neutral ring");
    expect(look("handed_off")).toBe("active half");
    expect(look("admitted")).toBe("active dot");
    expect(look("replied")).toBe("success check");
    expect(look("refused")).toBe("danger cross");
    expect(look("expired")).toBe("neutral slash");
    expect(deliveryStates.at_worker?.label).toBe("At the worker");
  });
});
