import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { Autonomy, WorkerRecord } from "../api/admin";
import type { Plans, ReadyWait } from "../api/types";
import { openPage } from "../opening";
import type { Sent } from "../testing";
import { golden, serveViews, testProject } from "../testing";
import { readyWaitWords } from "./ReadyWait";

// FR-UI-13: ready work that no worker's orchestrator will claim says why, wherever it shows, with what a member can do
// about it. The golden project's ready work is one planned work order and one contract review, and its worker is solo.

const guide = "generated-28";
const review = "review-1";

const solo: WorkerRecord = { id: "worker-a", name: "solo", epoch: 1, manifest: { capacity: { max_lanes: 4 } } };

const attended: ReadyWait = { reason: "run_window", worker: "worker-a", worker_name: "solo" };

const fourHours = 4 * 3_600_000;

const autonomy = (worker: string): Autonomy => ({ as_of: 1, worker_id: worker, sign_in: "subscription", level: "attended", risk_acknowledged: false, may_claim: false });

// serving serves the golden project with these waits on its ready work, and answers what Start and Open a run window
// send. A change nothing else answers is a 200.
function serving(waits: Record<string, ReadyWait> = { [guide]: attended }) {
  const plans = golden<Plans>("plans");
  plans.ready_waits = waits;
  return serveViews((call) => {
    if (call.path.startsWith("/v1/views/plans")) {
      return { status: 200, body: plans };
    }
    if (call.method === "POST" && call.path === "/v1/workers/worker-a/windows") {
      return { status: 201, body: { id: "w1", worker_id: "worker-a", opened_by: "member", opened_at: 0, closes_at: fourHours } };
    }
    if (call.method === "POST") {
      return { status: 200, body: {} };
    }
    if (call.path === "/v1/workers") {
      return { status: 200, body: { workers: [solo] } };
    }
    const asked = /^\/v1\/workers\/([^/]+)\/autonomy$/.exec(call.path);
    if (asked) {
      return { status: 200, body: autonomy(decodeURIComponent(asked[1] ?? "")) };
    }
    return undefined;
  });
}

const changes = (sent: Sent[]) => sent.filter((call) => call.method === "POST");

const rowOf = async (title: string) => {
  const link = await screen.findByRole("link", { name: title });
  const row = link.closest("tr");
  if (!row) {
    throw new Error(`no row holds ${title}`);
  }
  return row;
};

describe("ready work that no worker claims", () => {
  it("says why in plain words for each wait", () => {
    expect(readyWaitWords(attended)).toBe("solo is attended and no run window is open, so its orchestrator claims no work. Open a run window, or let it run unattended.");
    expect(readyWaitWords({ reason: "manual", worker: "worker-a", worker_name: "solo" })).toBe("solo is manual, so its orchestrator claims no work: a member starts each lane.");
    expect(readyWaitWords({ reason: "no_orchestrator", worker: "worker-a", worker_name: "solo" })).toBe(
      "No orchestrator runs on solo, so nothing claims ready work. Start its orchestrator, or start the work yourself.",
    );
    expect(readyWaitWords({ reason: "cap", worker: "worker-a", worker_name: "solo", cap: "lanes_per_worker" })).toBe(
      "solo holds as many lanes as its cap allows, so it claims more work only when one of its lanes ends.",
    );
    expect(readyWaitWords({ reason: "cap", cap: "wip_limit" })).toMatch(/^The fleet's cap on work in progress is reached/);
    expect(readyWaitWords({ reason: "cap", cap: "lanes_per_repository" })).toMatch(/^The cap on lanes in this repository is reached/);
    expect(readyWaitWords({ reason: "fleet", fleet: "pause" })).toBe("The fleet is paused, so no worker claims work.");
    expect(readyWaitWords({ reason: "fleet", fleet: "drain" })).toBe("The fleet is draining, so no worker claims new work.");
    expect(readyWaitWords({ reason: "spend_limit" })).toMatch(/^The project reached its spend limit/);
    expect(readyWaitWords({ reason: "no_worker" })).toBe("No worker can run it: none meets its needs.");
  });

  it("says why on the Work orders list, beneath the work's title, and work a worker will claim says nothing", async () => {
    serving();
    openPage("/work");
    const row = await rowOf("Write the guide");
    expect(within(row).getByText(/^solo is attended and no run window is open/)).toBeTruthy();
    const other = await rowOf("Review Write the docs");
    expect(within(other).queryByText(/no run window/)).toBeNull();
  });

  it("opens a run window of the server's default length in one click", async () => {
    document.cookie = "ropeshift_csrf=token-from-cookie";
    const sent = serving();
    openPage("/work");
    await userEvent.click(within(await rowOf("Write the guide")).getByRole("button", { name: "Open a run window" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    const [opened] = changes(sent);
    expect(opened?.url).toBe("/v1/workers/worker-a/windows");
    expect(opened?.csrf).toBe("token-from-cookie");
    expect(opened?.body).toMatchObject({ length_ms: 0 });
    expect((opened?.body as { id: string }).id).toMatch(/^[0-9A-Z]{26}$/);
    expect((await screen.findAllByText("Opened a run window on solo for 4 hours. Its orchestrator claims ready work until it closes.")).length).toBeGreaterThan(0);
  });

  it("leads Run unattended to the worker's level, where the stated risk is read first, and changes nothing itself", async () => {
    const sent = serving();
    openPage("/work");
    const link = within(await rowOf("Write the guide")).getByRole("link", { name: "Run unattended" });
    expect(link.getAttribute("href")).toBe("/settings?section=autonomy&worker=worker-a");
    expect(changes(sent)).toHaveLength(0);
  });

  it("says why on the work order's own page, with the same actions", async () => {
    serving();
    openPage(`/work-order?id=${guide}`);
    expect(await screen.findByText(/^solo is attended and no run window is open/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Open a run window" })).toBeTruthy();
    expect(screen.getByRole("link", { name: "Run unattended" })).toBeTruthy();
  });

  it("says on the Overview how many ready work orders wait, and why", async () => {
    serving({ [guide]: attended, [review]: attended });
    openPage(`/projects/${testProject}`);
    expect(await screen.findByText(/^2 ready work orders wait\./)).toBeTruthy();
    expect(screen.getByText(/solo is attended and no run window is open/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Open a run window" })).toBeTruthy();
  });
});

describe("starting ready work from the list", () => {
  it("offers Start on each ready row", async () => {
    serving();
    openPage("/work");
    expect(within(await rowOf("Write the guide")).getByRole("button", { name: "Start Write the guide" })).toBeTruthy();
    expect(within(await rowOf("Review Write the docs")).getByRole("button", { name: "Start Review Write the docs" })).toBeTruthy();
  });

  it("starts all ready work on one worker, each claimed and then started, in the list's order", async () => {
    const sent = serving();
    openPage("/work");
    await userEvent.click(await screen.findByRole("button", { name: "Start all ready" }));
    const box = await screen.findByRole("dialog", { name: "Start all ready work" });
    const worker = await within(box).findByRole("combobox", { name: "Worker" });
    await waitFor(() => expect((worker as HTMLSelectElement).value).toBe("worker-a"));
    await userEvent.click(within(box).getByRole("button", { name: "Start 2 work orders" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(4));
    expect(changes(sent).map((call) => call.path)).toEqual([
      `/v1/work-orders/${guide}/claims`,
      `/v1/work-orders/${guide}/lanes`,
      `/v1/work-orders/${review}/claims`,
      `/v1/work-orders/${review}/lanes`,
    ]);
    expect(changes(sent)[0]?.body).toMatchObject({ worker_id: "worker-a" });
    expect((await screen.findAllByText("Started 2 work orders on solo.")).length).toBeGreaterThan(0);
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Start all ready work" })).toBeNull());
  });
});
