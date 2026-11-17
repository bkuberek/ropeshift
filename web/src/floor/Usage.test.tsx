import { screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { LaneTelemetryList, Operations } from "../api/types";
import { openPage } from "../opening";
import { golden, refusal, serveViews } from "../testing";

// Usage's breakdown gains the cost of each lane, from the route `ropeshift telemetry lanes` reads: each lane's context
// at its last call, its tokens by type, the roster agents that spent some, and the cost its agent reported, which reads
// "at least" when some of it was never reported, and is never a bill.

function serveLanes(read: () => { status: number; body?: unknown } = () => ({ status: 200, body: golden<LaneTelemetryList>("telemetry-lanes") })) {
  return serveViews((call) => (call.method === "GET" && call.path === "/v1/telemetry/lanes" ? read() : undefined));
}

const cell = (row: HTMLElement, column: string) => row.querySelector<HTMLElement>(`[data-label="${column}"]`);
const flat = (element: Element | null | undefined) => (element?.textContent ?? "").replaceAll(/\s+/g, " ").trim();

async function table(): Promise<HTMLElement[]> {
  const regions = await screen.findAllByRole("region", { name: "Estimated cost by lane" });
  const region = regions.find((one) => one.classList.contains("table-wrap"));
  if (!region) {
    throw new Error("no table of lane costs");
  }
  return waitFor(() => {
    const rows = within(region).getAllByRole("row").slice(1);
    if (rows.length === 0) {
      throw new Error("no lane row");
    }
    return rows;
  });
}

// said is the tokens of a lane as the command prints them: each type in order of its name, then the number.
const said = (tokens: Record<string, number>) =>
  Object.keys(tokens)
    .sort()
    .map((kind) => `${kind} ${tokens[kind]?.toLocaleString("en-US")}`)
    .join(", ");

describe("the cost of each lane on the Usage page", () => {
  const read = golden<LaneTelemetryList>("telemetry-lanes");

  it("reads the lanes from the route `telemetry lanes` reads, and lists each one", async () => {
    const calls = serveLanes();
    openPage("/usage");
    const rows = await table();
    expect(rows.map((row) => flat(cell(row, "Lane")))).toEqual(read.lanes.map((lane) => lane.lane));
    expect(calls.filter((call) => call.path === "/v1/telemetry/lanes").map((call) => call.method)).toEqual(["GET"]);
  });

  it("says each lane's context, its tokens by type, and its cost, as at least what its agent reported only when some went unreported", async () => {
    serveLanes();
    openPage("/usage");
    const rows = await table();
    read.lanes.forEach((lane, at) => {
      const row = rows[at] as HTMLElement;
      expect(flat(cell(row, "Context")), lane.lane).toBe(`${lane.context_tokens.toLocaleString("en-US")} tokens`);
      expect(flat(cell(row, "Tokens")), lane.lane).toContain(said(lane.tokens));
    });
    expect(read.lanes.map((lane) => lane.lower_bound)).toEqual([true, false]);
    expect(flat(cell(rows[0] as HTMLElement, "Est. cost"))).toBe("at least $1.23");
    expect(flat(cell(rows[1] as HTMLElement, "Est. cost"))).toBe("under $0.01");
  });

  it("lists the tokens each roster agent spent inside a lane, which are part of the lane's tokens and not added to them", async () => {
    serveLanes();
    openPage("/usage");
    const rows = await table();
    const agents = read.lanes[0]?.agents ?? {};
    expect(Object.keys(agents).length).toBeGreaterThan(0);
    for (const [agent, spent] of Object.entries(agents)) {
      expect(flat(cell(rows[0] as HTMLElement, "Tokens")), agent).toContain(`${agent}: ${said(spent)}`);
    }
    expect(flat(cell(rows[1] as HTMLElement, "Tokens"))).not.toMatch(/:/);
  });

  it("says costs are estimated at list price from what agents reported, and are not a bill", async () => {
    serveLanes();
    openPage("/usage");
    await table();
    expect(screen.getAllByText(/estimated at list price/).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/not a bill/).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/reads at least a figure when some of it was never reported/).length).toBeGreaterThan(0);
  });

  it("says no lane has reported when the store holds nothing, and never draws it as a cost of zero", async () => {
    serveLanes(() => ({ status: 200, body: golden<LaneTelemetryList>("telemetry-lanes-empty") }));
    openPage("/usage");
    expect(await screen.findByText("No lane has reported its tokens or cost")).toBeTruthy();
    expect(screen.queryAllByRole("region", { name: "Estimated cost by lane" }).filter((one) => one.classList.contains("table-wrap"))).toEqual([]);
  });

  it("says a control plane with no telemetry store reads none", async () => {
    serveLanes(() => ({ status: 200, body: golden<LaneTelemetryList>("telemetry-lanes-unavailable") }));
    openPage("/usage");
    expect(await screen.findByText("This control plane reads no telemetry store")).toBeTruthy();
  });

  it("says why when the read is refused", async () => {
    serveLanes(() => refusal(500, "internal", "the telemetry store could not be read"));
    openPage("/usage");
    expect(await screen.findByText(/the telemetry store could not be read/)).toBeTruthy();
  });
});

// The estimated cost of each outcome reads as a lower bound only where the measures flag one, as the Overview's sum
// does: a sum every lane reported in full is the whole of what was reported.
describe("the estimated cost by outcome on the Usage page", () => {
  function serveOutcomes() {
    const measured = golden<Operations>("operations");
    measured.measures.cost_per_outcome = {
      change: { total_usd: 3, done: 2, per_done_usd: 1.5, lower_bound: true },
      plan: { total_usd: 0.5, done: 1, per_done_usd: 0.5, lower_bound: false },
    };
    return serveViews((call) => (call.method === "GET" && call.path.startsWith("/v1/views/operations") ? { status: 200, body: measured } : undefined));
  }

  it("says at least only of a sum that is a lower bound, and says that is the only one", async () => {
    serveOutcomes();
    openPage("/usage");
    const outcomes = await screen.findByRole("region", { name: /^Estimated cost by outcome in/ });
    const rows = await waitFor(() => {
      const found = within(outcomes).getAllByRole("row").slice(1);
      if (found.length !== 2) {
        throw new Error("no outcome rows");
      }
      return found;
    });
    expect(rows.map((row) => flat(cell(row, "Est. cost")))).toEqual(["at least $3.00", "$0.50"]);
    const foot = flat(outcomes.querySelector(".panel-foot"));
    expect(foot).toMatch(/reads at least a figure only when some of it was never reported/);
    expect(foot).not.toMatch(/so a sum is a lower bound/);
  });
});
