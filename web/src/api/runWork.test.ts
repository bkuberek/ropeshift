import { describe, expect, it } from "vitest";

import { refusal, serveApi } from "../testing";
import { claimAndStart, newRunIDs, startClaimed } from "./runWork";

const ulid = /^[0-9A-Z]{26}$/;

describe("claiming and starting work", () => {
  it("claims for the worker, then starts the lane, as `work claim` and `lane start` do", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    const lane = await claimAndStart("wo-1", "worker-1");
    expect(sent.map((call) => `${call.method} ${call.path}`)).toEqual(["POST /v1/work-orders/wo-1/claims", "POST /v1/work-orders/wo-1/lanes"]);
    expect(sent[0]?.body).toMatchObject({ worker_id: "worker-1" });
    expect((sent[0]?.body as { claim_id: string }).claim_id).toMatch(ulid);
    expect(lane).toMatch(ulid);
    expect(sent[1]?.body).toEqual({ lane_id: lane });
  });

  it("sends the IDs it is given, so a second try finds the first", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    const ids = newRunIDs("lane-named");
    await claimAndStart("wo-1", "worker-1", ids);
    await claimAndStart("wo-1", "worker-1", ids);
    expect(sent.map((call) => call.body)).toEqual([
      { claim_id: ids.claim, worker_id: "worker-1" },
      { lane_id: "lane-named" },
      { claim_id: ids.claim, worker_id: "worker-1" },
      { lane_id: "lane-named" },
    ]);
  });

  it("goes no further than a claim that is refused", async () => {
    const sent = serveApi((call) => (call.path.endsWith("/claims") ? refusal(409, "ineligible", "The fleet is paused.") : { status: 200, body: {} }));
    await expect(claimAndStart("wo-1", "worker-1")).rejects.toThrow("The fleet is paused.");
    expect(sent).toHaveLength(1);
  });

  it("says the claim was made when the lane start is refused", async () => {
    serveApi((call) => (call.path.endsWith("/lanes") ? refusal(409, "at_cap", "The worker is at its lane cap.") : { status: 200, body: {} }));
    let claimed = 0;
    await expect(claimAndStart("wo-1", "worker-1", newRunIDs(), { onClaimed: () => (claimed += 1) })).rejects.toThrow("The worker is at its lane cap.");
    expect(claimed).toBe(1);
  });

  it("names the project a change is for, when it is not the page's", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    await claimAndStart("wo-1", "worker-1", newRunIDs(), { project: "tidewater" });
    await startClaimed("wo-1", "lane-1", { project: "tidewater" });
    expect(sent.map((call) => call.project)).toEqual(["tidewater", "tidewater", "tidewater"]);
  });

  it("starts the lane of work already claimed with a lane request alone", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    expect(await startClaimed("wo-1", "")).toMatch(ulid);
    expect(sent.map((call) => call.path)).toEqual(["/v1/work-orders/wo-1/lanes"]);
  });
});
