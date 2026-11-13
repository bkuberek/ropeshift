import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { InProgress, OnHold, PlanOutline } from "../api/types";
import { openPage } from "../opening";
import type { Sent } from "../testing";
import { FakeEvents, golden, here, refusal, serveViews } from "../testing";

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
    const events = FakeEvents.opened.find((stream) => stream.path.startsWith("/v1/events"));
    act(() => events?.emit());
    await waitFor(() => expect(reads()).toBe(2), { timeout: 3000 });
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
