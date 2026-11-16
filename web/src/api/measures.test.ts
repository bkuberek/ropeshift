import { describe, expect, it } from "vitest";

import routes from "../../testdata/routes.json";
import { refusal, serveApi } from "../testing";
import { deliveryGroupings, readCalibration, readDelivery, readDeliveryByProject, readPlanForecast, readPlanForecasts } from "./measures";
import type { LookBack } from "./measures";
import type { Project, Route } from "./types";

const served = (routes as Route[]).map((route) => ({
  method: route.method,
  path: new RegExp(`^${route.pattern.replaceAll("{id}", "[^/]+").replaceAll("{project}", "[^/]+")}(\\?.*)?$`),
}));
const reachable = (method: string, path: string) => served.some((route) => route.method === method && route.path.test(path));

const group = (key: string, done: number) => ({ key, done, failed: 0, canceled: 0, per_week: done / 4, share: {}, trend: [] });
const lookBack = (by: string, keys: [string, number][]): LookBack => ({ by, from: 1, to: 2, groups: keys.map(([key, done]) => group(key, done)) });
const project = (name: string, state: Project["state"] = "active"): Project => ({ id: name, name, state, created_at: 1, repositories: [] });

describe("the reads of delivery and forecasts", () => {
  it("reads look back over a window by a grouping from the route the listener serves", async () => {
    const sent = serveApi(() => ({ status: 200, body: lookBack("size", []) }));
    await readDelivery("7d", "size");
    expect(sent.map((call) => call.path)).toEqual(["/v1/measures/delivery?window=7d&by=size"]);
    expect(reachable("GET", sent[0]?.url ?? "")).toBe(true);
  });

  it("reads estimate calibration over a window from the route the listener serves", async () => {
    const sent = serveApi(() => ({ status: 200, body: { from: 1, to: 2, scopes: [], milestones: {} } }));
    await readCalibration("7d");
    expect(sent.map((call) => call.path)).toEqual(["/v1/measures/estimates?window=7d"]);
    expect(reachable("GET", sent[0]?.url ?? "")).toBe(true);
  });

  it("reads a plan's newest forecast, or the version it is asked for, with the plan's ID escaped", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    await readPlanForecast("plan a/b");
    await readPlanForecast("plan a/b", 3);
    expect(sent.map((call) => call.path)).toEqual(["/v1/plans/plan%20a%2Fb/forecast", "/v1/plans/plan%20a%2Fb/forecast?version=3"]);
    for (const call of sent) {
      expect(reachable("GET", call.url)).toBe(true);
    }
  });

  it("names the groupings the command takes, and no other", () => {
    expect(deliveryGroupings.map((kind) => kind.value)).toEqual(["kind", "size", "agent", "repository"]);
  });

  it("reads every active project apart for all projects, each project one group, as the command does", async () => {
    const sent = serveApi((call) => ({ status: 200, body: lookBack("kind", [["", call.project === "alpha" ? 3 : 5], ["change", 1]]) }));
    const read = await readDeliveryByProject([project("alpha"), project("old", "archived"), project("beta")], "30d");
    expect(sent.map((call) => `${call.project} ${call.path}`)).toEqual(["alpha /v1/measures/delivery?window=30d&by=kind", "beta /v1/measures/delivery?window=30d&by=kind"]);
    expect(read.by).toBe("project");
    expect(read.groups.map((one) => [one.key, one.done])).toEqual([
      ["alpha", 3],
      ["beta", 5],
    ]);
  });

  it("reads no group for an installation with no active project", async () => {
    serveApi(() => ({ status: 200, body: {} }));
    expect((await readDeliveryByProject([project("old", "archived")], "30d")).groups).toEqual([]);
  });

  it("reads the forecast of each plan it is given, and leaves out one that has no version to forecast", async () => {
    const sent = serveApi((call) =>
      call.path.includes("/plans/bare/") ? refusal(404, "not_found", "plan bare has no version yet") : { status: 200, body: { plan_id: call.path.split("/")[3], plan_version: 1, state: "forecast", estimate_set: null, forecasts: [] } },
    );
    const read = await readPlanForecasts(["one", "bare", "two"]);
    expect(Object.keys(read).sort()).toEqual(["one", "two"]);
    expect(sent.map((call) => call.path).sort()).toEqual(["/v1/plans/bare/forecast", "/v1/plans/one/forecast", "/v1/plans/two/forecast"]);
  });

  it("fails when a read fails for any other reason, so a page says what it could not read", async () => {
    serveApi(() => refusal(500, "unavailable", "the ledger could not be read"));
    await expect(readPlanForecasts(["one"])).rejects.toThrow("the ledger could not be read");
  });
});
