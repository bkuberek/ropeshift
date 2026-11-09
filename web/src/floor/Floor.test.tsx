import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { Completed, InProgress, OnHold, Operations, ShopFloor, Stuck } from "../api/types";
import { openPage } from "../opening";
import { FakeEvents, golden, refusal, serveViews } from "../testing";

function serving<T>(name: string, change: (view: T) => void) {
  const view = golden<T>(name);
  change(view);
  return serveViews((call) => (call.path.startsWith(`/v1/views/${name}`) ? { status: 200, body: view } : undefined));
}

const line = (name: string) => screen.findByRole("article", { name });

describe("in progress", () => {
  it("lists running work, the longest first, with what its lane is doing", async () => {
    serveViews();
    openPage("/in-progress");
    const rows = await screen.findAllByRole("article");
    expect(rows[0]?.getAttribute("aria-labelledby")).toBeTruthy();
    expect(within(rows[0] as HTMLElement).getByRole("heading", { level: 2 }).textContent).toBe("Add the retry budget");
    const card = await line("Write the docs");
    expect(within(card).getByText("wo-running")).toBeTruthy();
    expect(within(card).getByText("lane-wo-running")).toBeTruthy();
    expect(within(card).getByText("Working")).toBeTruthy();
  });

  it("says how long each has run", async () => {
    serveViews();
    openPage("/in-progress");
    const card = await line("Add the retry budget");
    expect(card.textContent).toContain("10m");
    expect(within(card).getByText("for 10 minutes")).toBeTruthy();
  });

  it("states a last reported value as last reported, with its age", async () => {
    serveViews();
    openPage("/in-progress");
    const card = await line("Write the docs");
    expect(within(card).getByText(/^Last reported/).textContent).toMatch(/4m ago/);
  });

  it("names claimed work that has no lane yet", async () => {
    serving<InProgress>("in-progress", (view) => {
      const first = view.items[0];
      if (first) {
        first.work_order.state = "claimed";
        first.lane = undefined;
      }
    });
    openPage("/in-progress");
    const card = await line("Add the retry budget");
    expect(within(card).getByText("Claimed")).toBeTruthy();
    expect(within(card).getByText("No lane yet.")).toBeTruthy();
  });

  it("says so when nothing runs", async () => {
    serving<InProgress>("in-progress", (view) => {
      view.items = [];
    });
    openPage("/in-progress");
    expect(await screen.findByText("Nothing is in progress.")).toBeTruthy();
  });
});

describe("on hold", () => {
  it("says what each parked lane waits for", async () => {
    serveViews();
    openPage("/on-hold");
    const card = await line("Wait for the check");
    const wakes = within(card).getByText("Wakes").closest("div");
    expect(wakes?.textContent).toContain("When question question-1 is answered, or when a person resumes it");
    expect(within(wakes as HTMLElement).getByRole("link", { name: "question-1" }).getAttribute("href")).toBe("/questions?id=question-1");
    expect(card.textContent).not.toContain("any_of");
    expect(within(card).getAllByText("Parked").length).toBeGreaterThan(0);
    expect(within(card).getByText("wo-parked")).toBeTruthy();
    expect(within(card).getByText("lane-wo-parked")).toBeTruthy();
  });

  it("says when its next check falls", async () => {
    serving<OnHold>("on-hold", (view) => {
      const wake = view.items[0]?.wakes[0];
      if (wake) {
        wake.next_eval_at = view.as_of + 5 * 60_000;
      }
    });
    openPage("/on-hold");
    expect(within(await line("Wait for the check")).getByText("in 5m")).toBeTruthy();
  });

  it("says so when a parked lane's wake is gone", async () => {
    serving<OnHold>("on-hold", (view) => {
      if (view.items[0]) {
        view.items[0].wakes = [];
      }
    });
    openPage("/on-hold");
    expect(within(await line("Wait for the check")).getByText("No wake is recorded.")).toBeTruthy();
  });

  it("says so when nothing is on hold", async () => {
    serving<OnHold>("on-hold", (view) => {
      view.items = [];
    });
    openPage("/on-hold");
    expect(await screen.findByText("Nothing is on hold.")).toBeTruthy();
  });
});

describe("stuck", () => {
  it("lists what stopped, the longest stop first, and why", async () => {
    serveViews();
    openPage("/stuck");
    const rows = await screen.findAllByRole("article");
    expect(rows).toHaveLength(4);
    const blocked = await line("Move the archive");
    expect(within(blocked).getByText("Blocked")).toBeTruthy();
    expect(within(blocked).getByText("no_outcome")).toBeTruthy();
    expect(within(blocked).getByText("wo-blocked")).toBeTruthy();
  });

  it("takes an action through the API, as the member the session names", async () => {
    document.cookie = "ropeshift_csrf=token-from-cookie";
    const sent = serveViews((call) => (call.method === "POST" ? { status: 204 } : undefined));
    openPage("/stuck");
    await userEvent.click(within(await line("Fix the flaky test")).getByRole("button", { name: "Reset the breaker" }));
    await waitFor(() => expect(sent.some((call) => call.method === "POST")).toBe(true));
    const change = sent.find((call) => call.method === "POST");
    expect(change?.path).toBe("/v1/work-orders/wo-tripped/lane-reset");
    expect(change?.csrf).toBe("token-from-cookie");
  });

  it("asks why before it changes who owns work", async () => {
    const sent = serveViews((call) => (call.method === "POST" ? { status: 204 } : undefined));
    openPage("/stuck");
    const card = await line("Rotate the keys");
    await userEvent.click(within(card).getByRole("button", { name: "End this claim" }));
    expect(sent.some((call) => call.method === "POST")).toBe(false);
    await userEvent.type(within(card).getByRole("textbox", { name: "Why" }), "The worker is gone.");
    await userEvent.click(within(card).getByRole("button", { name: "End this claim" }));
    await waitFor(() => expect(sent.find((call) => call.method === "POST")?.body).toEqual({ reason: "The worker is gone." }));
  });

  it("reads again after an action", async () => {
    const sent = serveViews((call) => (call.method === "POST" ? { status: 204 } : undefined));
    openPage("/stuck");
    const card = await line("Fix the flaky test");
    const reads = sent.filter((call) => call.path === "/v1/views/stuck").length;
    await userEvent.click(within(card).getByRole("button", { name: "Reset the breaker" }));
    await waitFor(() => expect(sent.filter((call) => call.path === "/v1/views/stuck").length).toBeGreaterThan(reads));
  });

  it("says what the server refused, and leaves the line standing", async () => {
    serveViews((call) => (call.method === "POST" ? refusal(409, "conflict", "A worker took it first.") : undefined));
    openPage("/stuck");
    const card = await line("Fix the flaky test");
    await userEvent.click(within(card).getByRole("button", { name: "Reset the breaker" }));
    expect((await within(card).findByRole("alert")).textContent).toBe("A worker took it first.");
  });

  // ATTACK: a page left open outlives its session, and the server is what refuses.
  it("asks for a session when a change meets none, and changes nothing", async () => {
    let live = true;
    const sent = serveViews((call) => {
      if (call.method === "POST") {
        live = false;
        return refusal(401, "session_required", "This needs a member's session.");
      }
      return live ? undefined : refusal(401, "session_required");
    });
    openPage("/stuck");
    const card = await line("Fix the flaky test");
    await userEvent.click(within(card).getByRole("button", { name: "Reset the breaker" }));
    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeTruthy();
    expect(sent.filter((call) => call.method === "POST")).toHaveLength(1);
  });

  it("offers no action where the server offers none, and draws no rung", async () => {
    serving<Stuck>("stuck", (view) => {
      if (view.items[0]) {
        view.items[0].actions = [];
      }
    });
    openPage("/stuck");
    const card = await line("Rotate the keys");
    expect(within(card).queryByRole("button")).toBeNull();
    expect(card.closest("li")?.className).toContain("need-plain");
  });

  it("names a lane parked too long, the fifth of the design's list", async () => {
    serving<Stuck>("stuck", (view) => {
      const first = view.items[0];
      if (first) {
        first.reason = "long_parked";
        first.actions = ["resume"];
      }
    });
    openPage("/stuck");
    const card = await line("Rotate the keys");
    expect(within(card).getByText("Long parked")).toBeTruthy();
    expect(within(card).getByText("Its lane has stayed parked for a long time.")).toBeTruthy();
    expect(within(card).getByRole("button", { name: "Resume" })).toBeTruthy();
  });

  it("says what a lane parked too long waits for", async () => {
    serving<Stuck>("stuck", (view) => {
      const first = view.items[0];
      if (first) {
        first.reason = "long_parked";
        first.actions = ["resume", "cancel"];
        first.wakes = [{ id: "wake-1", expression: `{"any_of":[{"all_of":[{"question_answered":"q-signing-key"}]},{"human_resume":true}]}` }];
      }
    });
    openPage("/stuck");
    const card = await line("Rotate the keys");
    expect(within(card).getByText(/^Its lane has stayed parked/).textContent).toBe(
      "Its lane has stayed parked for a long time. It waits until question q-signing-key is answered, or until a person resumes it.",
    );
    expect(within(card).getByRole("link", { name: "q-signing-key" })).toBeTruthy();
  });

  it("says so when nothing is stuck", async () => {
    serving<Stuck>("stuck", (view) => {
      view.items = [];
    });
    openPage("/stuck");
    expect(await screen.findByText("Nothing is stuck.")).toBeTruthy();
  });
});

describe("completed", () => {
  it("lists every way work ended, the newest first", async () => {
    serveViews();
    openPage("/completed");
    const rows = await screen.findAllByRole("article");
    expect(rows.map((row) => within(row).getByRole("heading", { level: 2 }).textContent)).toEqual([
      "Drop the old plan", "Try the impossible", "Ship the first cut", "Trim the log",
    ]);
    expect(within(await line("Drop the old plan")).getByText("Canceled")).toBeTruthy();
    expect(within(await line("Try the impossible")).getAllByText("Failed").length).toBeGreaterThan(0);
    expect(within(await line("Ship the first cut")).getByText("Done")).toBeTruthy();
    expect(within(await line("Trim the log")).getByText("Waiting for acceptance")).toBeTruthy();
  });

  it("states what the lane said of the end", async () => {
    serveViews();
    openPage("/completed");
    expect(within(await line("Try the impossible")).getByText(/^It gave up on/)).toBeTruthy();
  });

  it("states each check's result and where it came from", async () => {
    serveViews();
    openPage("/completed");
    const failed = within(await line("Try the impossible")).getByRole("list", { name: "Checks" });
    expect(failed.textContent).toContain("go vet");
    expect(failed.textContent).toContain("Failed");
    expect(failed.textContent).toContain("Lane");
    const passed = within(await line("Ship the first cut")).getByRole("list", { name: "Checks" });
    expect(passed.textContent).toContain("Passed");
  });

  it("states where each criterion stands", async () => {
    serveViews();
    openPage("/completed");
    const criteria = within(await line("Try the impossible")).getByRole("list", { name: "Acceptance criteria" });
    expect(criteria.textContent).toContain("c1");
    expect(criteria.textContent).toContain("A check failed");
  });

  it("asks nobody for anything once work has ended", async () => {
    serveViews();
    openPage("/completed");
    expect(within(await line("Drop the old plan")).getByRole("list", { name: "Acceptance criteria" }).textContent).toContain("No check covered it");
    expect(within(await line("Ship the first cut")).getByRole("list", { name: "Acceptance criteria" }).textContent).toContain("Only the lane said so");
    expect(within(await line("Trim the log")).getByRole("list", { name: "Acceptance criteria" }).textContent).toContain("Needs a member");
  });

  it("counts the attempt that ended it, and never past the limit", async () => {
    serveViews();
    openPage("/completed");
    expect((await line("Try the impossible")).textContent).toContain("Attempt 1 of 1");
    expect((await line("Try the impossible")).textContent).not.toContain("Attempt 2");
  });

  it("states its artifacts as text, whatever an address says", async () => {
    serveViews();
    openPage("/completed");
    const card = await line("Ship the first cut");
    const artifacts = within(card).getByRole("list", { name: "Artifacts" });
    expect(artifacts.textContent).toContain("lane/lane-wo-done");
    expect(artifacts.textContent).toContain("javascript:alert(3)");
    expect(artifacts.querySelector("a")).toBeNull();
  });

  it("sends work waiting for a word to where it is given", async () => {
    serveViews();
    openPage("/completed");
    expect(within(await line("Trim the log")).getByRole("link", { name: "Decide in Needs you" }).getAttribute("href")).toBe("/");
    expect(within(await line("Ship the first cut")).queryByRole("link", { name: "Decide in Needs you" })).toBeNull();
  });

  it("says so when nothing has ended", async () => {
    serving<Completed>("completed", (view) => {
      view.items = [];
    });
    openPage("/completed");
    expect(await screen.findByText("Nothing has ended yet.")).toBeTruthy();
  });
});

describe("the shop floor", () => {
  it("states each worker and how fresh its word is", async () => {
    serveViews();
    openPage("/shop-floor");
    const workers = await screen.findByRole("region", { name: "Workers" });
    const card = within(workers).getByRole("article", { name: "solo" });
    expect(within(card).getByText("worker-a")).toBeTruthy();
    expect(within(card).getByText(/^Last reported/)).toBeTruthy();
    expect(within(card).getByText("Epoch").closest("div")?.textContent).toContain("1");
  });

  it("states a worker's orchestrator, wanted and actual", async () => {
    serveViews();
    openPage("/shop-floor");
    const workers = await screen.findByRole("region", { name: "Workers" });
    const orchestrator = within(workers).getByRole("group", { name: "Orchestrator" });
    expect(orchestrator.textContent).toContain("orchestrator-worker-a");
    expect(within(orchestrator).getByText("Wanted").closest("div")?.textContent).toContain("Running");
    expect(within(orchestrator).getByText("Actual").closest("div")?.textContent).toContain("Not reported");
  });

  it("labels an orchestrator's state as its worker's word is labeled", async () => {
    serving<ShopFloor>("shop-floor", (view) => {
      const orchestrator = view.workers[0]?.orchestrator;
      if (orchestrator) {
        orchestrator.actual_state = "working";
      }
    });
    openPage("/shop-floor");
    const orchestrator = await screen.findByRole("group", { name: "Orchestrator" });
    const actual = within(orchestrator).getByText("Actual").closest("div");
    expect(actual?.textContent).toContain("Working");
    expect(actual?.textContent).toContain("Last reported");
  });

  it("says when a worker runs no orchestrator", async () => {
    serving<ShopFloor>("shop-floor", (view) => {
      if (view.workers[0]) {
        view.workers[0].orchestrator = undefined;
      }
    });
    openPage("/shop-floor");
    expect(await screen.findByText("No orchestrator.")).toBeTruthy();
  });

  it("keeps observed lanes apart from last reported ones", async () => {
    serveViews();
    openPage("/shop-floor");
    const observed = await screen.findByRole("region", { name: "Observed lanes" });
    const reported = await screen.findByRole("region", { name: "Last reported lanes" });
    const view = golden<ShopFloor>("shop-floor");
    for (const lane of view.lanes) {
      const side = lane.freshness === "observed" ? observed : reported;
      expect(within(side).getByText(lane.id), lane.id).toBeTruthy();
    }
  });

  it("marks a last reported lane so it cannot pass for a live one", async () => {
    serveViews();
    openPage("/shop-floor");
    const reported = await screen.findByRole("region", { name: "Last reported lanes" });
    const card = within(reported).getByText("lane-wo-running").closest("article");
    expect(card?.className).toContain("lane-old");
    expect(reported.textContent).toContain("may no longer hold");
  });

  it("states how long ago each value came", async () => {
    serveViews();
    openPage("/shop-floor");
    const card = (await screen.findByText("lane-wo-running")).closest("article");
    expect(card?.textContent).toMatch(/Last reported.*4m ago/);
  });

  it("states a lane's generations", async () => {
    serveViews();
    openPage("/shop-floor");
    const card = (await screen.findByText("lane-wo-running")).closest("article");
    expect(within(card as HTMLElement).getByText("Generation").closest("div")?.textContent).toContain("1");
  });

  it("says so when no lane stands on one side", async () => {
    serving<ShopFloor>("shop-floor", (view) => {
      view.lanes = view.lanes.filter((lane) => lane.freshness === "observed");
    });
    openPage("/shop-floor");
    const reported = await screen.findByRole("region", { name: "Last reported lanes" });
    expect(within(reported).getByText("None.")).toBeTruthy();
  });

  it("says so when no worker has joined", async () => {
    serving<ShopFloor>("shop-floor", (view) => {
      view.workers = [];
      view.lanes = [];
    });
    openPage("/shop-floor");
    expect(await screen.findByText("No worker has joined.")).toBeTruthy();
  });
});

describe("operations", () => {
  it("states the fleet's mode and its caps", async () => {
    serveViews();
    openPage("/operations");
    const fleet = await screen.findByRole("region", { name: "Fleet" });
    expect(within(fleet).getByText("Mode").closest("div")?.textContent).toContain("Run");
    expect(within(fleet).getByText("Work in progress").closest("div")?.textContent).toContain("No limit");
  });

  it("states a cap that is set", async () => {
    serving<Operations>("operations", (view) => {
      view.fleet.wip_limit = 3;
      view.fleet.fleet_mode = "drain";
    });
    openPage("/operations");
    const fleet = await screen.findByRole("region", { name: "Fleet" });
    expect(within(fleet).getByText("Work in progress").closest("div")?.textContent).toContain("3");
    expect(within(fleet).getByText("Mode").closest("div")?.textContent).toContain("Drain");
  });

  it("counts what waits for a person, and leads there", async () => {
    serveViews();
    openPage("/operations");
    const fleet = await screen.findByRole("region", { name: "Fleet" });
    const waiting = within(fleet).getByText("Waiting for a person").closest("div");
    expect(waiting?.textContent).toContain("9");
    expect(within(waiting as HTMLElement).getByRole("link").getAttribute("href")).toBe("/");
  });

  it("counts workers against those it has heard from", async () => {
    serveViews();
    openPage("/operations");
    const fleet = await screen.findByRole("region", { name: "Fleet" });
    expect(within(fleet).getByText("Workers").closest("div")?.textContent).toContain("0 of 1 observed");
  });

  it("states a zero where a state holds nothing", async () => {
    serveViews();
    openPage("/operations");
    const table = await screen.findByRole("table", { name: "Work orders by state" });
    expect(within(table).getByRole("row", { name: "Claimed 0" })).toBeTruthy();
    expect(within(table).getByRole("row", { name: "Running 5" })).toBeTruthy();
  });

  it("says a lane's count is last word, and where its freshness stands", async () => {
    serveViews();
    openPage("/operations");
    const note = await screen.findByText(/each lane's last state/);
    expect(within(note).getByRole("link", { name: "the shop floor" }).getAttribute("href")).toBe("/shop-floor");
  });

  it("keeps a state this build has no words for in the count", async () => {
    serving<Operations>("operations", (view) => {
      view.work_orders.brand_new = 2;
    });
    openPage("/operations");
    const table = await screen.findByRole("table", { name: "Work orders by state" });
    expect(within(table).getByRole("row", { name: "brand_new 2" })).toBeTruthy();
  });

  it("states each source, its health, and what it could not read", async () => {
    serveViews();
    openPage("/operations");
    const sources = await screen.findByRole("region", { name: "Sources" });
    expect(sources.textContent).toContain("repofiles");
    expect(sources.textContent).toContain("repository-1");
    expect(sources.textContent).toContain("Ok");
    expect(sources.textContent).toContain("plans/bad.md");
  });

  it("shows the north star beside the overturn rate and rework", async () => {
    serveViews();
    openPage("/operations");
    const measures = await screen.findByRole("region", { name: "Product measures" });
    const star = within(measures).getByRole("group", { name: "The north star and its counterweights" });
    const names = within(star)
      .getAllByRole("term")
      .map((term) => term.textContent);
    expect(names).toEqual(["Interruptions per completed work order", "Overturn rate", "Rework", "Autonomous decision rate"]);
    expect(within(star).getByText("Interruptions per completed work order").closest("div")?.textContent).toContain("1 (1 over 1)");
    expect(within(star).getByText("Overturn rate").closest("div")?.textContent).toContain("0% (0 of 1)");
    expect(measures.textContent).toContain("over the last 7d");
  });

  it("says a rate of nothing has no data, rather than zero", async () => {
    serving<Operations>("operations", (view) => {
      view.measures.interruptions_per_completed = { numerator: 3, denominator: 0, value: null };
    });
    openPage("/operations");
    const measures = await screen.findByRole("region", { name: "Product measures" });
    expect(within(measures).getByText("Interruptions per completed work order").closest("div")?.textContent).toContain("No data yet");
    expect(within(measures).getByText("Autonomous decision rate").closest("div")?.textContent).toContain("No data yet");
  });

  it("counts a batch of questions as one interruption, and names each kind", async () => {
    serving<Operations>("operations", (view) => {
      view.measures.interruptions = 4;
      view.measures.interruptions_by_kind = { batch: 2, approval: 1, forced: 1 };
    });
    openPage("/operations");
    const measures = await screen.findByRole("region", { name: "Product measures" });
    expect(within(measures).getByText("Interruptions").closest("div")?.textContent).toContain(
      "4: 2 batches of questions, 1 approvals, 1 forced actions",
    );
  });

  it("states cost as a lower bound when some of it went unreported", async () => {
    serving<Operations>("operations", (view) => {
      view.measures.cost_per_outcome = { change: { total_usd: 3, done: 2, per_done_usd: 1.5, lower_bound: true } };
    });
    openPage("/operations");
    const table = await screen.findByRole("table", { name: "Cost per outcome" });
    expect(within(table).getByRole("row").textContent).toContain("at least $1.5000 each");
  });

  it("states park time by trigger, at the median and the 95th percentile", async () => {
    serving<Operations>("operations", (view) => {
      view.measures.park_time = { answer: { count: 3, median_ms: 1_800_000, p95_ms: 7_200_000 } };
    });
    openPage("/operations");
    const table = await screen.findByRole("table", { name: "Park time by trigger" });
    expect(within(table).getByRole("row").textContent).toContain("median 30m, 95th percentile 2h, of 3");
  });
});

describe("a work view on the stream", () => {
  it("reads again when something happens", async () => {
    const sent = serveViews();
    openPage("/stuck");
    await line("Fix the flaky test");
    await waitFor(() => expect(FakeEvents.opened).toHaveLength(1));
    const reads = sent.filter((call) => call.path === "/v1/views/stuck").length;
    FakeEvents.opened[0]?.emit();
    await waitFor(() => expect(sent.filter((call) => call.path === "/v1/views/stuck").length).toBe(reads + 1));
  });
});
