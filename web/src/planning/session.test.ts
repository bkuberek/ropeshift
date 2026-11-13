import { describe, expect, it } from "vitest";

import { refusal, serveApi } from "../testing";
import { planCriterion } from "../api/admin";
import { startPlanningSession } from "./session";

const ulid = /\/[0-9A-Z]{26}(\/|$)/;

function serve(review: unknown = { state: "ready" }, refuse?: (path: string) => ReturnType<typeof refusal> | undefined) {
  return serveApi((call) => {
    const refused = refuse?.(call.path);
    if (refused) {
      return refused;
    }
    if (call.path.endsWith("/review")) {
      return { status: 200, body: review };
    }
    return { status: 200, body: { id: "wo", title: "Saved searches" } };
  });
}

const steps = (sent: ReturnType<typeof serve>) => sent.map((call) => `${call.method} ${call.project} ${call.path.replace(ulid, "/ID$1")}`);

describe("starting a planning session", () => {
  it("makes the calls `ropeshift plan start` makes, in its order, in the project it names", async () => {
    const sent = serve();
    const started = await startPlanningSession("tidewater", { goal: "Saved searches\nwith alerts", repository: "repo-1", worker: "worker-1" });
    expect(steps(sent)).toEqual([
      "POST tidewater /v1/work-orders",
      "POST tidewater /v1/work-orders/ID/review",
      "POST tidewater /v1/work-orders/ID/claims",
      "POST tidewater /v1/work-orders/ID/lanes",
    ]);
    expect(sent[0]?.body).toMatchObject({ kind: "plan", title: "Saved searches", statement: "Saved searches\nwith alerts", repository_id: "repo-1", criteria: [{ id: "c1", text: planCriterion }] });
    expect(sent[2]?.body).toMatchObject({ worker_id: "worker-1" });
    const id = /work-orders\/([0-9A-Z]{26})/.exec(sent[1]?.path ?? "")?.[1];
    expect(started.workOrder).toBe(id);
    expect((sent[3]?.body as { lane_id: string }).lane_id).toBe(started.lane);
    expect(started.lane).toMatch(/^[0-9A-Z]{26}$/);
  });

  it("starts a session with no goal and no repository, as the command does", async () => {
    const sent = serve();
    await startPlanningSession("tidewater", { goal: "  \n", repository: "", worker: "worker-1" });
    expect(sent[0]?.body).toMatchObject({ kind: "plan", title: "Planning session", statement: "" });
    expect(sent[0]?.body).not.toHaveProperty("repository_id");
  });

  it("stops at a review that names what holds the work back, and says which work order it left", async () => {
    const sent = serve({ state: "contract_review", failures: [{ check: "criteria", detail: "no criterion" }] });
    await expect(startPlanningSession("tidewater", { goal: "x", repository: "", worker: "worker-1" })).rejects.toThrow(/was recorded and is not ready: criteria: no criterion/);
    expect(steps(sent)).toHaveLength(2);
  });

  it("shows a refusal in the API's words, and goes no further", async () => {
    const sent = serve({ state: "ready" }, (path) => (path.endsWith("/claims") ? refusal(409, "ineligible", "The fleet is paused.") : undefined));
    await expect(startPlanningSession("tidewater", { goal: "x", repository: "", worker: "worker-1" })).rejects.toThrow("The fleet is paused.");
    expect(steps(sent)).toHaveLength(3);
  });
});
