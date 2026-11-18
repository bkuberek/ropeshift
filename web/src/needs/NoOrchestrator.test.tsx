import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { NeedItem, NeedsYou as View, ShopFloor } from "../api/types";
import { CurrentProject } from "../app/route";
import { openPage } from "../opening";
import { golden, goldenNeedsYou, here, refusal, serveApi, serveViews, testProject } from "../testing";
import { NeedsYou } from "./NeedsYou";

// A question no orchestrator took within its batch window reaches Needs you from the core (FR-QD-3): its item says no
// orchestrator judged it, lists what the core found by text as judged by nobody, and shows its ladder trace, and the
// card offers to start an orchestrator beside answering.

const unjudgedTrace = [
  { rung: 1, candidates: ["decision-1"], outcome: "unjudged" },
  { rung: 2, outcome: "unjudged" },
  { rung: 3, outcome: "no_orchestrator" },
  { rung: 4, outcome: "no_orchestrator" },
];

function unjudgedView(): View {
  const view = goldenNeedsYou();
  const batch = view.items.find((item): item is Extract<NeedItem, { kind: "questions" }> => item.kind === "questions");
  const first = batch?.batch.items[0];
  if (!batch || !first) {
    throw new Error("no batch in the golden view");
  }
  first.no_orchestrator = true;
  first.citations = ["decision-1"];
  first.ladder_trace = unjudgedTrace;
  return view;
}

function floorWith(state?: string): ShopFloor {
  const floor = golden<ShopFloor>("shop-floor");
  for (const line of floor.workers) {
    line.orchestrator = state ? { id: `orchestrator-${line.worker.id}`, worker_id: line.worker.id, desired_state: "running", run: 1, resets: 0, actual_state: state } : undefined;
  }
  return floor;
}

function show(view: View) {
  const item = view.items.find((row) => row.kind === "questions");
  window.history.replaceState(null, "", `${here("/")}?item=${encodeURIComponent(`questions:${item?.id ?? ""}`)}`);
  return render(
    <CurrentProject.Provider value={testProject}>
      <NeedsYou view={view} readAt={Date.now()} after={async () => {}} />
    </CurrentProject.Provider>,
  );
}

const serveFloor = (floor: ShopFloor, start: () => { status: number; body?: unknown } = () => ({ status: 200, body: {} })) =>
  serveApi((call) => {
    if (call.method === "GET" && call.path.startsWith("/v1/views/shop-floor")) {
      return { status: 200, body: floor };
    }
    if (call.method === "PUT" && /^\/v1\/workers\/[^/]+\/orchestrator$/.test(call.path)) {
      return start();
    }
    return undefined;
  });

describe("a question no orchestrator judged", () => {
  it("says so on its item, lists what was found by text, and shows its ladder trace", async () => {
    serveFloor(floorWith());
    show(unjudgedView());
    const group = await screen.findByRole("group", { name: "Should the budget hold across restarts?" });
    expect(within(group).getByText("No orchestrator judged it")).toBeTruthy();
    expect(within(group).getByText("Found by text, judged by nobody:")).toBeTruthy();
    const trace = within(group).getByText("How it got here").closest("details");
    expect(trace?.textContent).toContain("No orchestrator worked this rung");
  });

  it("offers to start an orchestrator on each worker whose orchestrator does not run, and starts the one pressed", async () => {
    const sent = serveFloor(floorWith());
    show(unjudgedView());
    const start = await screen.findByRole("button", { name: "Start orchestrator on solo" });
    expect(screen.getByRole("button", { name: "Start orchestrator on build-host-07" })).toBeTruthy();
    await userEvent.click(start);
    await waitFor(() => expect(sent.some((call) => call.method === "PUT" && call.path === "/v1/workers/worker-a/orchestrator")).toBe(true));
    expect(sent.find((call) => call.method === "PUT")?.body).toEqual({ desired_state: "running" });
  });

  it("shows a refusal to start next to the control", async () => {
    serveFloor(floorWith(), () => refusal(404, "not_found", "worker worker-a: not found"));
    show(unjudgedView());
    await userEvent.click(await screen.findByRole("button", { name: "Start orchestrator on solo" }));
    expect(await screen.findByText("worker worker-a: not found")).toBeTruthy();
  });

  it("offers no start while an orchestrator runs, because it takes what is still open", async () => {
    const sent = serveFloor(floorWith("working"));
    show(unjudgedView());
    await waitFor(() => expect(sent.some((call) => call.path.startsWith("/v1/views/shop-floor"))).toBe(true));
    await screen.findByRole("group", { name: "Should the budget hold across restarts?" });
    expect(screen.queryByRole("button", { name: /^Start orchestrator/ })).toBeNull();
  });

  it("offers no start on a batch an orchestrator judged", async () => {
    const sent = serveFloor(floorWith());
    show(goldenNeedsYou());
    await screen.findByRole("group", { name: "Should the budget hold across restarts?" });
    expect(sent.some((call) => call.path.startsWith("/v1/views/shop-floor"))).toBe(false);
    expect(screen.queryByRole("button", { name: /^Start orchestrator/ })).toBeNull();
  });
});

describe("the open questions with no orchestrator running", () => {
  it("say no orchestrator is judging them, and offer to start one", async () => {
    serveViews();
    openPage("/questions");
    await screen.findByRole("article", { name: "Should the budget hold across restarts?" });
    expect(await screen.findByText(/No orchestrator is judging the questions that wait/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Start orchestrator on solo" })).toBeTruthy();
  });

  it("say nothing of it while an orchestrator runs", async () => {
    const sent = serveViews((call) => (call.path.startsWith("/v1/views/shop-floor") ? { status: 200, body: floorWith("idle") } : undefined));
    openPage("/questions");
    await screen.findByRole("article", { name: "Should the budget hold across restarts?" });
    await waitFor(() => expect(sent.some((call) => call.path.startsWith("/v1/views/shop-floor"))).toBe(true));
    expect(screen.queryByText(/No orchestrator is judging/)).toBeNull();
  });
});
