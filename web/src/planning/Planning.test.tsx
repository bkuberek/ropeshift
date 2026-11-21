import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { PlanForecast } from "../api/measures";
import type { Completed, InProgress, OnHold, PlanOutline } from "../api/types";
import { startedEntry } from "../lanes/starting";
import { openPage } from "../opening";
import type { Sent } from "../testing";
import { FakeEvents, golden, here, openLedgerStream, refusal, serveViews } from "../testing";

const lane = "lane-wo-effect";
const parkedLane = "lane-wo-parked";

type Answer = { status: number; body?: unknown };

const workers = { workers: [{ id: "worker-a", name: "solo", epoch: 1, protocol: 2, manifest: {} }] };

// serve makes two of the golden work orders planning sessions, one running and one parked, and answers the
// outline from its golden.
function serve(answer?: (sent: Sent) => Answer | undefined, outline: PlanOutline = golden<PlanOutline>("outline")) {
  const running = golden<InProgress>("in-progress");
  const first = running.items.find((item) => item.lane?.id === lane);
  if (first) {
    first.work_order.kind = "plan";
    first.work_order.title = "Saved searches v2";
  }
  const held = golden<OnHold>("on-hold");
  const parked = held.items.find((item) => item.lane?.id === parkedLane);
  if (parked) {
    parked.work_order.kind = "plan";
    parked.work_order.title = "Adopt tidewater";
  }
  return serveViews((call) => {
    const own = answer?.(call);
    if (own) {
      return own;
    }
    if (call.method === "GET" && call.path === "/v1/views/in-progress") {
      return { status: 200, body: running };
    }
    if (call.method === "GET" && call.path === "/v1/views/on-hold") {
      return { status: 200, body: held };
    }
    if (call.method === "GET" && /^\/v1\/lanes\/[^/]+\/outline$/.test(call.path)) {
      return { status: 200, body: outline };
    }
    if (call.method === "GET" && call.path === "/v1/workers") {
      return { status: 200, body: workers };
    }
    if (call.method === "GET" && call.path === "/v1/repositories") {
      return { status: 200, body: golden("repositories") };
    }
    if (call.method === "GET" && call.path === "/v1/sources") {
      return { status: 200, body: { sources: [] } };
    }
    return undefined;
  });
}

const paneStream = (id: string): FakeEvents => {
  const found = FakeEvents.opened.find((stream) => stream.path === `/v1/panes/${id}`);
  if (!found) {
    throw new Error(`no pane stream of ${id}`);
  }
  return found;
};

describe("the planning pane with no session", () => {
  it("says so, and offers to start one", async () => {
    serve((call) => (call.path === "/v1/views/in-progress" || call.path === "/v1/views/on-hold" ? { status: 200, body: { as_of: 1, revision: 1, items: [] } } : undefined));
    openPage(here("/planning"));
    expect(await screen.findByRole("heading", { level: 1, name: "Planning" })).toBeTruthy();
    expect(await screen.findByText("No planning session is running")).toBeTruthy();
    expect(screen.getAllByRole("button", { name: "Start planning session" }).length).toBeGreaterThan(0);
  });

  it("starts a session with the calls the command makes, and opens its pane", async () => {
    const sent = serve((call) => {
      if (call.method === "POST" && call.path.endsWith("/review")) {
        return { status: 200, body: { state: "ready" } };
      }
      if (call.method === "POST") {
        return { status: 200, body: { id: "wo", title: "Saved searches" } };
      }
      if (call.path === "/v1/views/in-progress" || call.path === "/v1/views/on-hold") {
        return { status: 200, body: { as_of: 1, revision: 1, items: [] } };
      }
      return undefined;
    });
    openPage(here("/planning"));
    const user = userEvent.setup();
    await user.click((await screen.findAllByRole("button", { name: "Start planning session" }))[0] as HTMLElement);
    const dialog = await screen.findByRole("dialog", { name: "Start planning session" });
    await user.type(within(dialog).getByLabelText("Goal"), "Saved searches v2");
    await waitFor(() => expect((within(dialog).getByLabelText("Worker") as HTMLSelectElement).value).toBe("worker-a"));
    await user.click(within(dialog).getByRole("button", { name: "Start planning session" }));
    await waitFor(() => expect(window.location.pathname).toBe("/projects/default/planning"));
    expect(window.location.search).toMatch(/^\?lane=[0-9A-Z]{26}$/);
    const steps = sent.filter((call) => call.method === "POST").map((call) => call.path.replace(/\/[0-9A-Z]{26}(\/|$)/, "/ID$1"));
    expect(steps).toEqual(["/v1/work-orders", "/v1/work-orders/ID/review", "/v1/work-orders/ID/claims", "/v1/work-orders/ID/lanes"]);
    expect(sent.find((call) => call.path === "/v1/work-orders")?.body).toMatchObject({ kind: "plan", title: "Saved searches v2", statement: "Saved searches v2" });
  });

  it("reads the goal from a file in the browser, and refuses one that is no goal", async () => {
    serve((call) => (call.path === "/v1/views/in-progress" || call.path === "/v1/views/on-hold" ? { status: 200, body: { as_of: 1, revision: 1, items: [] } } : undefined));
    openPage(here("/planning"));
    const user = userEvent.setup();
    await user.click((await screen.findAllByRole("button", { name: "Start planning session" }))[0] as HTMLElement);
    const dialog = await screen.findByRole("dialog", { name: "Start planning session" });
    await user.upload(within(dialog).getByLabelText("Or read the goal from a file"), new File(["# Saved searches\nWith alerts"], "goal.md", { type: "text/markdown" }));
    await waitFor(() => expect((within(dialog).getByLabelText("Goal") as HTMLTextAreaElement).value).toBe("# Saved searches\nWith alerts"));
    expect(within(dialog).getByText("Read from goal.md. Edit it above before you start.")).toBeTruthy();
    await user.upload(within(dialog).getByLabelText("Or read the goal from a file"), new File(["x".repeat(300 * 1024)], "big.md", { type: "text/markdown" }));
    expect(await within(dialog).findByText(/big\.md is 300 KiB\. A goal is a few pages/)).toBeTruthy();
  });

  it("shows the plan of the session that ended last, from its branch, with its work order", async () => {
    const completed = golden<Completed>("completed");
    const ended = completed.items.find((item) => item.standing.artifacts.some((artifact) => artifact.lane_id === "lane-wo-done"));
    if (!ended) {
      throw new Error("the golden holds no work that lane-wo-done ran");
    }
    ended.work_order.kind = "plan";
    ended.work_order.title = "Berths and sailings";
    const sent = serve((call) => {
      if (call.path === "/v1/views/in-progress" || call.path === "/v1/views/on-hold") {
        return { status: 200, body: { as_of: 1, revision: 1, items: [] } };
      }
      return call.path === "/v1/views/completed" ? { status: 200, body: completed } : undefined;
    });
    openPage(here("/planning"));
    expect(await screen.findByText("No planning session is running")).toBeTruthy();
    const last = await screen.findByRole("region", { name: "The last planning session" });
    expect(within(last).getByRole("link", { name: "Berths and sailings" }).getAttribute("href")).toBe(`/projects/default/work-order?id=${ended.work_order.id}`);
    expect(last.textContent).toContain("lane lane-wo-done");
    const plan = await within(last).findByRole("complementary", { name: "Plan of the last session" });
    expect(await within(plan).findByRole("heading", { name: "Documents" })).toBeTruthy();
    expect(sent.some((call) => call.path === "/v1/lanes/lane-wo-done/outline")).toBe(true);
    expect(within(last).queryByRole("region", { name: /^Pane of lane/ })).toBeNull();
  });

  // servedEnded answers completed work in which wo-failed, which ended last, and wo-done were planning sessions.
  function servedEnded(outline?: PlanOutline) {
    const completed = golden<Completed>("completed");
    for (const item of completed.items) {
      item.work_order.kind = ["wo-failed", "wo-done"].includes(item.work_order.id) ? "plan" : "change";
    }
    return serve((call) => {
      if (call.path === "/v1/views/in-progress" || call.path === "/v1/views/on-hold") {
        return { status: 200, body: { as_of: 1, revision: 1, items: [] } };
      }
      if (outline && /^\/v1\/lanes\/[^/]+\/outline$/.test(call.path)) {
        return { status: 200, body: outline };
      }
      return call.path === "/v1/views/completed" ? { status: 200, body: completed } : undefined;
    });
  }

  it("shows the last session a member accepted, and none that failed or was canceled after it", async () => {
    const sent = servedEnded();
    openPage(here("/planning"));
    const last = await screen.findByRole("region", { name: "The last planning session" });
    expect(within(last).getByRole("link", { name: "Ship the first cut" }).getAttribute("href")).toBe("/projects/default/work-order?id=wo-done");
    expect(last.textContent).toContain("lane lane-wo-done");
    await within(last).findByRole("heading", { name: "Documents" });
    expect(sent.some((call) => call.path === "/v1/lanes/lane-wo-failed/outline")).toBe(false);
  });

  it("reads the accepted plan once, however much the ledger says, because it does not change", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const sent = servedEnded();
      openPage(here("/planning"));
      const last = await screen.findByRole("region", { name: "The last planning session" });
      await within(last).findByRole("heading", { name: "Documents" });
      const reads = () => sent.filter((call) => call.path === "/v1/lanes/lane-wo-done/outline").length;
      expect(reads()).toBe(1);
      const events = await openLedgerStream();
      for (let i = 0; i < 3; i++) {
        act(() => events.emit());
        await act(async () => {
          await vi.advanceTimersByTimeAsync(2_000);
        });
      }
      expect(reads()).toBe(1);
      expect(within(last).queryByRole("heading", { name: "Documents" })).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it("reads completed work at most once every 5 seconds, however fast the ledger changes", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const sent = servedEnded();
      openPage(here("/planning"));
      await screen.findByRole("region", { name: "The last planning session" });
      const reads = () => sent.filter((call) => call.method === "GET" && call.path === "/v1/views/completed").length;
      await waitFor(() => expect(reads()).toBe(1));
      const events = await openLedgerStream();
      for (let i = 0; i < 4; i++) {
        act(() => events.emit());
        await act(async () => {
          await vi.advanceTimersByTimeAsync(800);
        });
      }
      expect(reads(), "a burst inside the gap reads nothing more").toBe(1);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2_500);
      });
      await waitFor(() => expect(reads()).toBe(2));
      act(() => events.emit());
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1_000);
      });
      expect(reads(), "the next change waits out a gap of its own").toBe(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it("says why the accepted plan cannot be read, as a note and not as a failure", async () => {
    servedEnded({ unread: "commit abc123, which a member accepted, could not be read: no such revision: abc123" } as PlanOutline);
    openPage(here("/planning"));
    const last = await screen.findByRole("region", { name: "The last planning session" });
    expect(await within(last).findByText(/commit abc123, which a member accepted, could not be read/)).toBeTruthy();
    expect(within(last).queryByRole("alert")).toBeNull();
  });

  it("shows no last session when no planning session has ended", async () => {
    const completed = golden<Completed>("completed");
    for (const item of completed.items) {
      item.work_order.kind = "change";
    }
    serve((call) => {
      if (call.path === "/v1/views/in-progress" || call.path === "/v1/views/on-hold") {
        return { status: 200, body: { as_of: 1, revision: 1, items: [] } };
      }
      return call.path === "/v1/views/completed" ? { status: 200, body: completed } : undefined;
    });
    openPage(here("/planning"));
    expect(await screen.findByText("No planning session is running")).toBeTruthy();
    expect(screen.queryByRole("region", { name: "The last planning session" })).toBeNull();
  });

  it("shows the API's refusal at the dialog, and stays open", async () => {
    serve((call) => {
      if (call.method === "POST" && call.path.endsWith("/claims")) {
        return refusal(409, "ineligible", "The fleet is paused.");
      }
      if (call.method === "POST" && call.path.endsWith("/review")) {
        return { status: 200, body: { state: "ready" } };
      }
      if (call.method === "POST") {
        return { status: 200, body: { id: "wo" } };
      }
      return call.path === "/v1/views/in-progress" || call.path === "/v1/views/on-hold" ? { status: 200, body: { as_of: 1, revision: 1, items: [] } } : undefined;
    });
    openPage(here("/planning"));
    const user = userEvent.setup();
    await user.click((await screen.findAllByRole("button", { name: "Start planning session" }))[0] as HTMLElement);
    const dialog = await screen.findByRole("dialog", { name: "Start planning session" });
    await waitFor(() => expect((within(dialog).getByLabelText("Worker") as HTMLSelectElement).value).toBe("worker-a"));
    await user.click(within(dialog).getByRole("button", { name: "Start planning session" }));
    expect(await within(dialog).findByText("The fleet is paused.")).toBeTruthy();
    expect(screen.getByRole("dialog", { name: "Start planning session" })).toBeTruthy();
  });
});

describe("a planning session", () => {
  it("names its target, plan, project, work order, lane, and worker, and says what the session can do", async () => {
    serve();
    openPage(here(`/planning?lane=${lane}`));
    const title = await screen.findByRole("heading", { level: 1, name: "Saved searches v2" });
    const head = title.closest(".convo-head") as HTMLElement;
    expect(within(head).getByText("Planning session")).toBeTruthy();
    expect(within(head).getByRole("link", { name: "wo-effect" }).getAttribute("href")).toBe("/projects/default/work-order?id=wo-effect");
    expect(head.textContent).toContain(`lane ${lane} on solo`);
    expect(within(head).getByText("Working")).toBeTruthy();
    expect(within(head).getByText(/Approving the plan stays yours/)).toBeTruthy();
  });

  it("says a session that waits for its person is waiting for you, beside its state", async () => {
    serve((call) => {
      if (call.method === "GET" && call.path === "/v1/views/in-progress") {
        const running = golden<InProgress>("in-progress");
        const first = running.items.find((item) => item.lane?.id === lane);
        if (first?.lane) {
          first.work_order.kind = "plan";
          first.work_order.title = "Saved searches v2";
          first.lane.actual_state = "idle";
          first.lane.conditions = ["waiting_for_person"];
        }
        return { status: 200, body: running };
      }
      return undefined;
    });
    openPage(here(`/planning?lane=${lane}`));
    const title = await screen.findByRole("heading", { level: 1, name: "Saved searches v2" });
    const head = title.closest(".convo-head") as HTMLElement;
    expect(within(head).getByText("Idle")).toBeTruthy();
    expect(within(head).getByText("Waiting for you").className).toContain("cond");
  });

  it("names the agent CLI version its worker checks, beside its state", async () => {
    const checkingWords = "Checking Claude Code 2.1.400: this lane runs on documented surfaces until the check passes; a respawn then picks up the qualified surfaces";
    serve((call) => {
      if (call.method === "GET" && call.path === "/v1/views/in-progress") {
        const running = golden<InProgress>("in-progress");
        const first = running.items.find((item) => item.lane?.id === lane);
        if (first?.lane) {
          first.work_order.kind = "plan";
          first.work_order.title = "Saved searches v2";
          first.lane.checking = { driver: "claude-code", version: "2.1.400", words: checkingWords };
        }
        return { status: 200, body: running };
      }
      return undefined;
    });
    openPage(here(`/planning?lane=${lane}`));
    const title = await screen.findByRole("heading", { level: 1, name: "Saved searches v2" });
    const head = title.closest(".convo-head") as HTMLElement;
    expect(within(head).getByText("Working")).toBeTruthy();
    expect(within(head).getByText(checkingWords)).toBeTruthy();
  });

  it("says beside Degraded mode, in a sentence the chip points to, when the session restarts on qualified surfaces", async () => {
    serve((call) => {
      if (call.method === "GET" && call.path === "/v1/views/in-progress") {
        const running = golden<InProgress>("in-progress");
        const first = running.items.find((item) => item.lane?.id === lane);
        if (first?.lane) {
          first.work_order.kind = "plan";
          first.work_order.title = "Saved searches v2";
          first.lane.conditions = ["degraded_mode"];
        }
        return { status: 200, body: running };
      }
      return undefined;
    });
    openPage(here(`/planning?lane=${lane}`));
    const title = await screen.findByRole("heading", { level: 1, name: "Saved searches v2" });
    const head = title.closest(".convo-head") as HTMLElement;
    const chip = within(head).getByText("Degraded mode");
    const sentence = within(head).getByText("Runs on documented surfaces until it restarts on a qualified version.");
    expect(sentence.id).not.toBe("");
    expect(chip.getAttribute("aria-describedby")).toBe(sentence.id);
  });

  it("opens in Conversations, which names no target", async () => {
    serve();
    openPage(here(`/planning?lane=${lane}`));
    const link = await screen.findByRole("link", { name: "Open in Conversations" });
    expect(link.getAttribute("href")).toBe("/projects/default/conversations");
  });

  it("states that the person is attached, and Detach takes the typing away while the session runs on", async () => {
    serve();
    openPage(here(`/planning?lane=${lane}`));
    await screen.findByRole("heading", { level: 1, name: "Saved searches v2" });
    expect(screen.getByText(/You are attached\. Keys you type in the pane go to the session as you/)).toBeTruthy();
    expect(screen.getByRole("textbox", { name: "Type into the session" })).toBeTruthy();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Detach" }));
    expect(screen.getByText(/You are detached\. The session keeps running/)).toBeTruthy();
    expect(screen.queryByRole("textbox", { name: "Type into the session" })).toBeNull();
    expect(screen.getByRole("region", { name: `Pane of lane ${lane}` })).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Attach" }));
    expect(screen.getByRole("textbox", { name: "Type into the session" })).toBeTruthy();
  });

  it("opens the lane's pane", async () => {
    serve();
    openPage(here(`/planning?lane=${lane}`));
    await screen.findByRole("heading", { level: 1, name: "Saved searches v2" });
    await waitFor(() => paneStream(lane));
    act(() => paneStream(lane).send("screen", { lane_id: lane, generation: 1, screen: "What is the plan for?" }));
    expect((await screen.findByRole("log", { name: `Screen of lane ${lane}` })).textContent).toBe("What is the plan for?");
  });

  it("picks the session that has a lane when the address names none", async () => {
    serve();
    openPage(here("/planning"));
    expect(await screen.findByRole("heading", { level: 1, name: "Saved searches v2" })).toBeTruthy();
  });

  it("shows a lane it has not heard of yet by its address, so a session that just started opens", async () => {
    serve();
    openPage(here("/planning?lane=lane-new"));
    expect(await screen.findByRole("heading", { level: 1, name: "Planning session" })).toBeTruthy();
    expect(screen.getByRole("region", { name: "Pane of lane lane-new" })).toBeTruthy();
    expect(await screen.findByRole("heading", { level: 2, name: "Plan in progress" })).toBeTruthy();
  });
});

// A lane the page was sent to right after it started reaches the ledger only once its worker reads its desired
// state, so its pane answers not_found until then.
describe("a planning session that just started", () => {
  const fresh = "lane-new";
  const streams = () => FakeEvents.opened.filter((stream) => stream.path === `/v1/panes/${fresh}`);

  // serveStarting answers the pane's refusal with not_found while the lane is missing.
  function serveStarting(missing: () => boolean) {
    serve((call) => (call.method === "GET" && call.path === `/v1/panes/${fresh}` && missing() ? refusal(404, "not_found", `lane ${fresh}: not found`) : undefined));
  }

  // refuseNext waits for the next stream of the pane, and refuses it as the server does a lane it does not hold.
  async function refuseNext(count: number) {
    await waitFor(() => expect(streams()).toHaveLength(count));
    act(() => streams()[count - 1]?.drop());
  }

  async function passMs(ms: number) {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(ms);
    });
  }

  it("asks again until the lane reaches the ledger, without showing the refusal, and then takes input", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      let missing = true;
      serveStarting(() => missing);
      openPage(here(`/planning?lane=${fresh}`), startedEntry(fresh));
      await screen.findByRole("heading", { level: 1, name: "Planning session" });
      const pane = screen.getByRole("region", { name: `Pane of lane ${fresh}` });
      for (let count = 1; count <= 3; count++) {
        await refuseNext(count);
        await waitFor(() => expect(within(pane).getByRole("status").textContent).toMatch(/starting/i));
        expect(within(pane).queryByRole("alert")).toBeNull();
        await passMs(5_000);
      }
      missing = false;
      await waitFor(() => expect(streams()).toHaveLength(4));
      act(() => streams()[3]?.send("screen", { lane_id: fresh, generation: 1, screen: "What is the plan for?" }));
      await waitFor(() => expect(within(pane).getByRole("status").textContent).toMatch(/generation 1/i));
      expect(within(pane).queryByRole("alert")).toBeNull();
      fireEvent.change(within(pane).getByRole("textbox", { name: "Type into the session" }), { target: { value: "Saved searches" } });
      expect((within(pane).getByRole("button", { name: "Send" }) as HTMLButtonElement).disabled).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });

  it("says the lane never started once the window passes, and stops asking", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      serveStarting(() => true);
      openPage(here(`/planning?lane=${fresh}`), startedEntry(fresh));
      await screen.findByRole("heading", { level: 1, name: "Planning session" });
      const pane = screen.getByRole("region", { name: `Pane of lane ${fresh}` });
      let count = 0;
      while (!within(pane).queryByRole("alert")) {
        count++;
        if (count > 100) {
          throw new Error("the pane asked more than 100 times and never said the lane did not start");
        }
        await refuseNext(count);
        await passMs(5_000);
      }
      expect(within(pane).getByRole("alert").textContent).toMatch(new RegExp(`lane ${fresh} has not started`, "i"));
      expect(within(pane).getByRole("alert").textContent).toMatch(/reload/i);
      const asked = streams().length;
      await passMs(60_000);
      expect(streams()).toHaveLength(asked);
    } finally {
      vi.useRealTimers();
    }
  });

  it("waits for a lane the start dialog started, because the move to its page says so", async () => {
    serve((call) => {
      if (call.method === "POST" && call.path.endsWith("/review")) {
        return { status: 200, body: { state: "ready" } };
      }
      if (call.method === "POST") {
        return { status: 200, body: { id: "wo", title: "Saved searches" } };
      }
      if (call.path === "/v1/views/in-progress" || call.path === "/v1/views/on-hold") {
        return { status: 200, body: { as_of: 1, revision: 1, items: [] } };
      }
      return call.method === "GET" && call.path.startsWith("/v1/panes/") ? refusal(404, "not_found", "lane not found") : undefined;
    });
    openPage(here("/planning"));
    const user = userEvent.setup();
    await user.click((await screen.findAllByRole("button", { name: "Start planning session" }))[0] as HTMLElement);
    const dialog = await screen.findByRole("dialog", { name: "Start planning session" });
    await user.type(within(dialog).getByLabelText("Goal"), "Saved searches v2");
    await waitFor(() => expect((within(dialog).getByLabelText("Worker") as HTMLSelectElement).value).toBe("worker-a"));
    await user.click(within(dialog).getByRole("button", { name: "Start planning session" }));
    await waitFor(() => expect(window.location.search).toMatch(/^\?lane=[0-9A-Z]{26}$/));
    const started = new URLSearchParams(window.location.search).get("lane") ?? "";
    const pane = await screen.findByRole("region", { name: `Pane of lane ${started}` });
    await waitFor(() => expect(FakeEvents.opened.some((stream) => stream.path === `/v1/panes/${started}`)).toBe(true));
    act(() => FakeEvents.opened.find((stream) => stream.path === `/v1/panes/${started}`)?.drop());
    await waitFor(() => expect(within(pane).getByRole("status").textContent).toMatch(/starting/i));
    expect(within(pane).queryByRole("alert")).toBeNull();
  });
});

// A lane the address names that was not just started, such as one that ended or a misspelled one, is one the
// ledger holds already or never will, so its pane says why it cannot open at once.
describe("a lane the address names that was not just started", () => {
  it("is refused at once in the API's words, and the page stops asking", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const gone = "lane-misspelled";
      serve((call) => (call.method === "GET" && call.path === `/v1/panes/${gone}` ? refusal(404, "not_found", `lane ${gone}: not found`) : undefined));
      openPage(here(`/planning?lane=${gone}`));
      await screen.findByRole("heading", { level: 1, name: "Planning session" });
      const pane = screen.getByRole("region", { name: `Pane of lane ${gone}` });
      const streams = () => FakeEvents.opened.filter((stream) => stream.path === `/v1/panes/${gone}`);
      await waitFor(() => expect(streams()).toHaveLength(1));
      act(() => streams()[0]?.drop());
      expect((await within(pane).findByRole("alert")).textContent).toBe(`lane ${gone}: not found`);
      expect(within(pane).getByRole("status").textContent).not.toMatch(/starting/i);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(10_000);
      });
      expect(streams()).toHaveLength(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it("is refused at once when the page is reached again long after the lane's start", async () => {
    const gone = "lane-long-ago";
    serve((call) => (call.method === "GET" && call.path === `/v1/panes/${gone}` ? refusal(404, "not_found", `lane ${gone}: not found`) : undefined));
    openPage(here(`/planning?lane=${gone}`), { ...startedEntry(gone), startedAt: Date.now() - 3 * 60_000 });
    await screen.findByRole("heading", { level: 1, name: "Planning session" });
    const pane = screen.getByRole("region", { name: `Pane of lane ${gone}` });
    await waitFor(() => expect(FakeEvents.opened.some((stream) => stream.path === `/v1/panes/${gone}`)).toBe(true));
    act(() => FakeEvents.opened.find((stream) => stream.path === `/v1/panes/${gone}`)?.drop());
    expect((await within(pane).findByRole("alert")).textContent).toBe(`lane ${gone}: not found`);
  });

  it("is refused at once when the start the history entry names is another lane's", async () => {
    const gone = "lane-ended";
    serve((call) => (call.method === "GET" && call.path === `/v1/panes/${gone}` ? refusal(404, "not_found", `lane ${gone}: not found`) : undefined));
    openPage(here(`/planning?lane=${gone}`), startedEntry("lane-other"));
    await screen.findByRole("heading", { level: 1, name: "Planning session" });
    const pane = screen.getByRole("region", { name: `Pane of lane ${gone}` });
    await waitFor(() => expect(FakeEvents.opened.some((stream) => stream.path === `/v1/panes/${gone}`)).toBe(true));
    act(() => FakeEvents.opened.find((stream) => stream.path === `/v1/panes/${gone}`)?.drop());
    expect((await within(pane).findByRole("alert")).textContent).toBe(`lane ${gone}: not found`);
  });
});

describe("the plan outline", () => {
  it("lists the documents changed or new, the milestones, the contracts with their counts, and the drafts", async () => {
    const sent = serve();
    openPage(here(`/planning?lane=${lane}`));
    const outline = await screen.findByRole("complementary", { name: "Plan in progress" });
    expect(await within(outline).findByText("docs/requirements.md")).toBeTruthy();
    expect(within(outline).getByText("plans/m4.md").closest("li")?.textContent).toContain("New");
    expect(within(outline).getByText("docs/requirements.md").closest("li")?.textContent).toContain("Changed");
    expect(within(outline).getByText(/Saved searches v2/).textContent).toContain("priority 4");
    expect(within(outline).getByText(/Saved search storage/).textContent).toContain("2 criteria · 1 work order");
    expect(within(outline).getByText("dd-10")).toBeTruthy();
    expect(sent.some((call) => call.path === `/v1/lanes/${lane}/outline`)).toBe(true);
  });

  it("names a question that blocks, and why the version is not submitted", async () => {
    serve();
    openPage(here(`/planning?lane=${lane}`));
    const outline = await screen.findByRole("complementary", { name: "Plan in progress" });
    const question = await within(outline).findByRole("link", { name: "Hourly or daily?" });
    expect(question.getAttribute("href")).toBe("/projects/default/questions?id=question-blocking");
    expect(question.closest("li")?.textContent).toContain("Blocking");
    expect(within(outline).getByText(/has not submitted this version for approval/)).toBeTruthy();
  });

  it("says a request stands for the tip, and leads to its approval", async () => {
    const outline = golden<PlanOutline>("outline");
    outline.not_submitted = undefined;
    outline.approval = { id: "approval-1", state: "pending", version: 5 };
    serve(undefined, outline);
    openPage(here(`/planning?lane=${lane}`));
    const link = await screen.findByRole("link", { name: "Review version 5" });
    expect(link.getAttribute("href")).toBe("/projects/default/plan?id=approval-1&version=5");
  });

  it("shows the estimates and forecasts of the version a request stands for, each band keeping its badge", async () => {
    const outline = golden<PlanOutline>("outline");
    outline.not_submitted = undefined;
    outline.approval = { id: "approval-1", state: "pending", plan_id: "plan-saved", version: 6 };
    const forecast = golden<PlanForecast>("forecast");
    const sent = serve(undefined, outline);
    openPage(here(`/planning?lane=${lane}`));
    const pane = await screen.findByRole("complementary", { name: "Plan in progress" });
    expect(await within(pane).findByRole("heading", { level: 3, name: "Estimates" })).toBeTruthy();
    await waitFor(() => expect(pane.querySelectorAll("[data-estimate]")).toHaveLength(forecast.estimate_set?.estimates.length ?? -1));
    expect(sent.some((call) => call.path === "/v1/plans/plan-saved/forecast?version=6")).toBe(true);
    expect(pane.querySelector("[data-forecast='plan']")).toBeTruthy();
    expect(pane.querySelector("details.estimate-more")).toBeTruthy();
  });

  it("says a milestone's forecast is pending until the forecast tick has made it, and still shows the estimates", async () => {
    const outline = golden<PlanOutline>("outline");
    outline.approval = { id: "approval-1", state: "pending", plan_id: "plan-saved", version: 6 };
    const pending: PlanForecast = { ...golden<PlanForecast>("forecast"), state: "pending", forecasts: [] };
    serve((call) => (call.method === "GET" && call.path.startsWith("/v1/plans/plan-saved/forecast") ? { status: 200, body: pending } : undefined), outline);
    openPage(here(`/planning?lane=${lane}`));
    const pane = await screen.findByRole("complementary", { name: "Plan in progress" });
    expect(await within(pane).findByText(/Forecast pending: the daemon's forecast tick has not forecast version 6 yet/)).toBeTruthy();
    expect(pane.querySelectorAll("[data-estimate]").length).toBeGreaterThan(0);
  });

  it("reads no forecast of a branch with no request standing for it, or of a request the ledger holds no plan for", async () => {
    const outline = golden<PlanOutline>("outline");
    outline.approval = { id: "approval-1", state: "pending", version: 1 };
    const sent = serve(undefined, outline);
    openPage(here(`/planning?lane=${lane}`));
    const pane = await screen.findByRole("complementary", { name: "Plan in progress" });
    await within(pane).findByRole("link", { name: "Review version 1" });
    expect(sent.some((call) => call.path.includes("/forecast"))).toBe(false);
    expect(within(pane).queryByRole("heading", { level: 3, name: "Estimates" })).toBeNull();
  });

  it("says why a branch cannot be read, and shows nothing else", async () => {
    serve(undefined, golden<PlanOutline>("outline-unread"));
    openPage(here(`/planning?lane=${lane}`));
    const outline = await screen.findByRole("complementary", { name: "Plan in progress" });
    expect(await within(outline).findByText(/the lane recorded no commit its branch began at/)).toBeTruthy();
    expect(within(outline).queryByText("Documents")).toBeNull();
  });

  it("keeps the text of a document or a question as text, whatever it says", async () => {
    const outline = golden<PlanOutline>("outline");
    outline.questions = [{ id: "q", text: '<img src=x onerror="alert(1)">', blocking: false }];
    serve(undefined, outline);
    openPage(here(`/planning?lane=${lane}`));
    expect(await screen.findByText('<img src=x onerror="alert(1)">')).toBeTruthy();
    expect(document.querySelector("img[src='x']")).toBeNull();
  });

  it("reads again when the ledger says something happened", async () => {
    const sent = serve();
    openPage(here(`/planning?lane=${lane}`));
    await screen.findByText("docs/requirements.md");
    const reads = () => sent.filter((call) => call.path === `/v1/lanes/${lane}/outline`).length;
    expect(reads()).toBe(1);
    const events = await openLedgerStream();
    act(() => events.emit());
    await waitFor(() => expect(reads()).toBe(2), { timeout: 3000 });
  });
});

describe("the planning pane's History tab", () => {
  const historyPath = "/v1/measures/history";

  it("sits beside the outline, reads the history only once chosen, and labels it as what the session sees", async () => {
    const sent = serve();
    openPage(here(`/planning?lane=${lane}`));
    const pane = await screen.findByRole("complementary", { name: "Plan in progress" });
    expect(within(pane).getByRole("tab", { name: "Outline", selected: true })).toBeTruthy();
    await within(pane).findByText("docs/requirements.md");
    expect(sent.some((call) => call.path === historyPath)).toBe(false);
    await userEvent.click(within(pane).getByRole("tab", { name: "History" }));
    expect(await within(pane).findByRole("heading", { level: 3, name: "What the planning session sees" })).toBeTruthy();
    expect(sent.some((call) => call.method === "GET" && call.path === historyPath && call.project === "default")).toBe(true);
    expect(pane.querySelectorAll("[data-group]").length).toBe(golden<{ by_kind_size: unknown[] }>("history").by_kind_size.length * 2);
    expect(within(pane).queryByText("docs/requirements.md")).toBeNull();
    await userEvent.click(within(pane).getByRole("tab", { name: "Outline" }));
    expect(await within(pane).findByText("docs/requirements.md")).toBeTruthy();
  });

  it("says a project with no finished work has none to show", async () => {
    serve((call) => (call.method === "GET" && call.path === historyPath ? { status: 200, body: golden("history-empty") } : undefined));
    openPage(here(`/planning?lane=${lane}`));
    const pane = await screen.findByRole("complementary", { name: "Plan in progress" });
    await userEvent.click(await within(pane).findByRole("tab", { name: "History" }));
    expect(await within(pane).findByText("No finished work yet. Work orders will have no estimate until comparable work finishes.")).toBeTruthy();
  });

  it("shows a refused read in the API's words and keeps the outline a tab away", async () => {
    serve((call) => (call.method === "GET" && call.path === historyPath ? refusal(500, "unavailable", "The history could not be read.") : undefined));
    openPage(here(`/planning?lane=${lane}`));
    const pane = await screen.findByRole("complementary", { name: "Plan in progress" });
    await userEvent.click(await within(pane).findByRole("tab", { name: "History" }));
    expect(await within(pane).findByText("The history could not be read.")).toBeTruthy();
    await userEvent.click(within(pane).getByRole("tab", { name: "Outline" }));
    expect(await within(pane).findByText("docs/requirements.md")).toBeTruthy();
  });
});

describe("a parked session", () => {
  it("shows the API's refusal, the last screen received labeled parked, and Resume", async () => {
    const sent = serve((call) => {
      if (call.method === "GET" && call.path === `/v1/panes/${parkedLane}`) {
        return refusal(409, "no_pane", "lane lane-wo-parked has no pane for generation 1 (state parked): it runs without a terminal.");
      }
      if (call.method === "POST" && call.path === "/v1/work-orders/wo-parked/lane-resume") {
        return { status: 204 };
      }
      return undefined;
    });
    openPage(here(`/planning?lane=${parkedLane}`));
    expect(await screen.findByRole("heading", { level: 1, name: "Adopt tidewater" })).toBeTruthy();
    await waitFor(() => paneStream(parkedLane));
    act(() => paneStream(parkedLane).send("screen", { lane_id: parkedLane, generation: 1, screen: "Waiting on question 1" }));
    act(() => paneStream(parkedLane).drop());
    expect(await screen.findByText(/has no pane for generation 1 \(state parked\)/)).toBeTruthy();
    expect(screen.getByRole("log", { name: `Screen of lane ${parkedLane}` }).textContent).toBe("Waiting on question 1");
    expect(screen.getByText("The last screen this page received.")).toBeTruthy();
    expect(screen.getByText(/The session is parked, so it has no process/)).toBeTruthy();
    expect(screen.queryByRole("textbox", { name: "Type into the session" })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Resume" }));
    await waitFor(() => expect(sent.some((call) => call.method === "POST" && call.path === "/v1/work-orders/wo-parked/lane-resume")).toBe(true));
  });

  it("shows a refused resume in the API's words", async () => {
    serve((call) => (call.method === "POST" && call.path.endsWith("/lane-resume") ? refusal(409, "conflict", "The fleet is paused.") : undefined));
    openPage(here(`/planning?lane=${parkedLane}`));
    await userEvent.click(await screen.findByRole("button", { name: "Resume" }));
    expect(await screen.findByText("The fleet is paused.")).toBeTruthy();
  });
});
