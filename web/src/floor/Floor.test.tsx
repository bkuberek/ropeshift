import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { Completed, InProgress, Operations, ResourceReport, ShopFloor, Stuck } from "../api/types";
import { openPage } from "../opening";
import { golden, here, openLedgerStream, refusal, serveViews } from "../testing";

function serving<T>(name: string, change: (view: T) => void) {
  const view = golden<T>(name);
  change(view);
  return serveViews((call) => (call.path.startsWith(`/v1/views/${name}`) ? { status: 200, body: view } : undefined));
}

// row finds the table row that names a record, by its title or its ID.
async function row(name: string): Promise<HTMLElement> {
  return waitFor(
    () => {
    const found = screen.getAllByText(name).find((element) => element.closest("tr") && !element.closest("[hidden]"))?.closest("tr");
    if (!found) {
      throw new Error(`no row names ${name}`);
    }
    return found as HTMLElement;
    },
    { timeout: 3000 },
  );
}

const gib = 1024 ** 3;

// measured is what a worker reports of its machine: its CPU, memory, pressure, load, and each volume.
function measured(): ResourceReport {
  return {
    interval_ms: 15000,
    machine: {
      cores: 8,
      cpu_percent: 42.4,
      load_1: 1,
      load_5: 2.5,
      load_15: 3,
      memory_total_bytes: 32 * gib,
      memory_used_bytes: 12 * gib,
      memory_available_bytes: 20 * gib,
      memory_pressure_some: 1.5,
      volumes: [
        { roles: ["state"], free_bytes: 5 * gib, total_bytes: 100 * gib },
        { roles: ["worktrees"], free_bytes: 120 * gib, total_bytes: 500 * gib },
      ],
    },
    caps: { max_lanes: 7, max_lanes_set: false, max_lanes_per_sign_in: 0 },
    sessions: [],
  };
}

const cell = (tr: HTMLElement, column: string) => tr.querySelector<HTMLElement>(`[data-label="${column}"]`);

// card is the decision a work order waits on, beside its tabs.
const card = () => screen.findByRole("complementary", { name: "Decision and facts" }).then((side) => within(side).getByRole("article"));

// An ID stays on one line and ends in an ellipsis when its column is too narrow (`.id`), so the whole of
// it stands in its title, and in the page for a screen reader and a copy.
function heldWhole(scope: HTMLElement, id: string) {
  const shown = within(scope).getAllByText(id);
  for (const element of shown) {
    expect(element.classList.contains("id"), `${id} in <${element.tagName.toLowerCase()} class="${element.className}">`).toBe(true);
    expect(element.getAttribute("title")).toBe(id);
  }
}

describe("a long ID", () => {
  it("stays whole on one line among the work orders", async () => {
    serveViews();
    openPage("/work?group=in-progress");
    const tr = await row("Write the docs");
    for (const id of ["wo-running", "lane-wo-running"]) {
      heldWhole(tr, id);
    }
  });

  it("stays whole on one line on the floor", async () => {
    serveViews();
    openPage("/floor");
    const tr = await row("lane-wo-running");
    for (const id of ["lane-wo-running", "wo-running"]) {
      heldWhole(tr, id);
    }
  });

  it("stays whole on one line among the sources", async () => {
    serveViews();
    openPage("/measures");
    heldWhole(await screen.findByRole("region", { name: "Sources" }), "repository-1");
  });
});

describe("the work orders", () => {
  it("opens on the open work, with a tab and a count for every group", async () => {
    serveViews();
    openPage("/work");
    const tabs = await screen.findByRole("navigation", { name: "Groups" });
    await waitFor(() => expect(within(tabs).getAllByRole("link").map((link) => link.textContent?.replace(/\d+$/, ""))).toEqual(["Open", "In progress", "On hold", "Stuck", "Review and merge", "Completed", "All"]));
    expect(within(tabs).getByRole("link", { name: /^Open/ }).getAttribute("aria-current")).toBe("page");
    await waitFor(() => expect(within(tabs).getByRole("link", { name: /^In progress/ }).textContent).toMatch(/\d+$/));
  });

  it("lists running work with its state, lane, and how long it has run", async () => {
    serveViews();
    openPage("/work?group=in-progress");
    const tr = await row("Write the docs");
    expect(within(tr).getByText("wo-running")).toBeTruthy();
    expect(within(tr).getByText("lane-wo-running")).toBeTruthy();
    expect(within(tr).getByText("Running")).toBeTruthy();
    const ran = await row("Add the retry budget");
    expect(cell(ran, "In state")?.textContent).toContain("10m");
    expect(within(ran).getByText("for 10 minutes")).toBeTruthy();
  });

  it("names each work order's repository by the name the project calls it, with its ID in the title", async () => {
    serveNamedRepository();
    openPage("/work?group=in-progress");
    const tr = await row("Write the docs");
    const repository = within(cell(tr, "Repository") as HTMLElement).getByText("orders-api");
    expect(repository.getAttribute("title")).toBe("repository-1");
    expect(cell(tr, "Repository")?.textContent).toBe("orders-api");
  });

  it("names a work order's repository in its header", async () => {
    serveNamedRepository();
    openPage("/work-order?id=wo-running");
    await screen.findByRole("heading", { level: 1, name: /Write the docs/ });
    await waitFor(() => expect(screen.getByText("orders-api").getAttribute("title")).toBe("repository-1"));
  });

  it("shows a repository's ID where the project names no such repository", async () => {
    serveViews((call) => (call.method === "GET" && call.path === "/v1/repositories" ? { status: 200, body: { repositories: [] } } : undefined));
    openPage("/work?group=in-progress");
    const tr = await row("Write the docs");
    heldWhole(cell(tr, "Repository") as HTMLElement, "repository-1");
  });

  it("names claimed work that has no lane yet", async () => {
    serving<InProgress>("in-progress", (view) => {
      const first = view.items[0];
      if (first) {
        first.work_order.state = "claimed";
        first.lane = undefined;
      }
    });
    openPage("/work?group=in-progress");
    const tr = await row("Add the retry budget");
    expect(within(tr).getByText("Claimed")).toBeTruthy();
    expect(cell(tr, "Lane")?.textContent).toBe("no lane");
  });

  it("says what each parked lane waits for, with a link to it", async () => {
    serveViews();
    openPage("/work?group=on-hold");
    const tr = await row("Wait for the check");
    expect(tr.textContent).toContain("When question question-1 is answered, or when a person resumes it");
    expect(within(tr).getByRole("link", { name: "question-1" }).getAttribute("href")).toBe(here("/questions?id=question-1"));
    expect(tr.textContent).not.toContain("any_of");
    expect(within(tr).getAllByText("Parked").length).toBeGreaterThan(0);
  });

  it("lists what stopped, and why", async () => {
    serveViews();
    openPage("/work?group=stuck");
    const blocked = await row("Move the archive");
    expect(within(blocked).getAllByText("Blocked").length).toBeGreaterThan(0);
    expect(within(blocked).getByText("wo-blocked")).toBeTruthy();
    expect(await row("Rotate the keys")).toBeTruthy();
  });

  it("lists every way work ended, each with its state", async () => {
    serveViews();
    openPage("/work?group=completed");
    expect(within(await row("Drop the old plan")).getByText("Canceled")).toBeTruthy();
    expect(within(await row("Try the impossible")).getAllByText("Failed").length).toBeGreaterThan(0);
    expect(within(await row("Ship the first cut")).getByText("Done")).toBeTruthy();
    expect(within(await row("Try the impossible")).getByText(/^It gave up on/)).toBeTruthy();
  });

  it("keeps work that waits for acceptance or a merge in its own group", async () => {
    serveViews();
    openPage("/work?group=review");
    expect(within(await row("Trim the log")).getByText("Waiting for acceptance")).toBeTruthy();
  });

  it("counts the attempt that ended it, and never past the limit", async () => {
    serveViews();
    openPage("/work?group=completed");
    const tr = await row("Try the impossible");
    expect(cell(tr, "Attempt")?.textContent).toBe("1 of 1");
  });

  it.each([
    ["open", "Nothing is open"],
    ["in-progress", "Nothing is in progress."],
    ["on-hold", "Nothing is on hold."],
    ["stuck", "Nothing is stuck."],
    ["review", "Nothing waits for acceptance or a merge."],
    ["completed", "Nothing has ended yet."],
  ])("says so when %s holds nothing", async (group, empty) => {
    serveViews((call) => {
      const view = /^\/v1\/views\/(plans|in-progress|on-hold|stuck|completed)/.exec(call.path)?.[1];
      if (!view) {
        return undefined;
      }
      return { status: 200, body: view === "plans" ? { as_of: 1, revision: 1, plans: [], unplanned: [] } : { as_of: 1, revision: 1, items: [] } };
    });
    openPage(`/work?group=${group}`);
    expect(await screen.findByText(new RegExp(`^${empty.replace(".", "\\.")}`))).toBeTruthy();
  });

  it("links each work order to its own page", async () => {
    serveViews();
    openPage("/work?group=in-progress");
    const link = within(await row("Write the docs")).getByRole("link", { name: "Write the docs" });
    expect(link.getAttribute("href")).toBe(here("/work-order?id=wo-running"));
  });
});

describe("a work order", () => {
  it("heads with its ID, title, state, and lifecycle", async () => {
    serveViews();
    openPage("/work-order?id=wo-running");
    const title = await screen.findByRole("heading", { level: 1, name: /^wo-running/ });
    expect(title.textContent).toContain("Write the docs");
    const steps = screen.getByRole("list", { name: "Lifecycle" });
    expect(within(steps).getByText("Running").closest("[aria-current]")?.getAttribute("aria-current")).toBe("step");
    expect(within(steps).getByText("Ready").textContent).toBe("Ready");
  });

  it("says when no work order has the ID", async () => {
    serveViews();
    openPage("/work-order?id=wo-nowhere");
    expect(await screen.findByRole("heading", { level: 1, name: "No such work order" })).toBeTruthy();
    expect(screen.getByRole("link", { name: "All work orders" }).getAttribute("href")).toBe(here("/work"));
  });

  it("keeps what the review found with the work it judged", async () => {
    serveViews();
    openPage("/work-order?id=wo-accept");
    const review = within(await screen.findByRole("tabpanel")).getByRole("region", { name: "What the review found" });
    expect(within(review).getByText("Cannot judge")).toBeTruthy();
    expect(within(review).getByText("trim.go:30")).toBeTruthy();
  });

  it("states where each criterion stands, and asks nobody for anything once work has ended", async () => {
    serveViews();
    openPage("/work-order?id=wo-failed");
    expect((await screen.findByRole("list", { name: "Acceptance criteria" })).textContent).toContain("A check failed");
    expect(screen.queryByRole("complementary", { name: "Decision and facts" })?.querySelector("article")).toBeFalsy();
  });

  it("states each check's result and where it came from", async () => {
    serveViews();
    openPage("/work-order?id=wo-failed&tab=checks");
    const checks = await screen.findByRole("list", { name: "Checks" });
    expect(checks.textContent).toContain("go vet");
    expect(checks.textContent).toContain("Failed");
    expect(checks.textContent).toContain("Lane");
  });

  it("states its artifacts as text, whatever an address says", async () => {
    serveViews();
    openPage("/work-order?id=wo-done&tab=checks");
    const artifacts = await screen.findByRole("list", { name: "Artifacts" });
    expect(artifacts.textContent).toContain("lane/lane-wo-done");
    expect(artifacts.textContent).toContain("javascript:alert(3)");
    expect(artifacts.querySelector("a")).toBeNull();
  });

  it("states who approved each merge, at which level, what landed, and what a merge still waits on", async () => {
    serving<Completed>("completed", (view) => {
      const first = view.items[0];
      if (!first) {
        throw new Error("the golden view lists no completed work");
      }
      const waiting = structuredClone(first);
      waiting.work_order = { ...waiting.work_order, id: "wo-merging", title: "Merge the retry budget", state: "integrating" };
      waiting.standing = {
        ...waiting.standing,
        state: "integrating",
        why: "required checks are still running on 1111aaaa: CI result",
        integrations: [
          { id: "i1", work_order_id: "wo-merging", approved_commit: "1111aaaa2222", merge_authority: "review_approves", approved_by_kind: "review", approved_by_id: "lane-r/1", approved_at: 1 },
        ],
      };
      view.items = [waiting];
    });
    openPage("/work-order?id=wo-merging&tab=checks");
    const waiting = await screen.findByRole("list", { name: "Merge" });
    expect(waiting.textContent).toContain("1111aaaa");
    expect(waiting.textContent).toContain("lane-r/1");
    expect(waiting.textContent).toContain("The merge waits: required checks are still running on 1111aaaa: CI result.");
    expect(waiting.textContent).not.toContain("merged");
  });

  it("states what a merge landed, and who approved it", async () => {
    serving<Completed>("completed", (view) => {
      const first = view.items[0];
      if (!first) {
        throw new Error("the golden view lists no completed work");
      }
      const merged = structuredClone(first);
      merged.work_order = { ...merged.work_order, id: "wo-merged", title: "Merged by a member", state: "done" };
      merged.standing = {
        ...merged.standing,
        integrations: [
          { id: "i2", work_order_id: "wo-merged", approved_commit: "3333bbbb4444", merge_authority: "person_approves", approved_by_kind: "member", approved_by_id: "dana", approved_at: 1, merged_commit: "5555cccc6666", merged_at: 2 },
        ],
      };
      view.items = [merged];
    });
    openPage("/work-order?id=wo-merged&tab=checks");
    const merged = await screen.findByRole("list", { name: "Merge" });
    expect(merged.textContent).toContain("dana");
    expect(merged.textContent).toContain("merged 5555cccc");
    expect(merged.textContent).not.toContain("The merge waits");
  });

  it("says the diff and the audit are not served yet, rather than drawing nothing", async () => {
    serveViews();
    openPage("/work-order?id=wo-running&tab=changes");
    expect(await screen.findByText("The diff is not served to this page yet")).toBeTruthy();
    await userEvent.click(screen.getByRole("tab", { name: "Activity" }));
    expect(await screen.findByText("The audit is not served to this page yet")).toBeTruthy();
  });

  it("moves between its tabs with the arrow keys", async () => {
    serveViews();
    openPage("/work-order?id=wo-running");
    const review = await screen.findByRole("tab", { name: "Review" });
    review.focus();
    await userEvent.keyboard("{ArrowRight}");
    await waitFor(() => expect(screen.getByRole("tab", { name: "Changes" }).getAttribute("aria-selected")).toBe("true"));
    expect(document.activeElement).toBe(screen.getByRole("tab", { name: "Changes" }));
  });
});

describe("what stuck work waits on", () => {
  it("takes an action through the API, as the member the session names", async () => {
    document.cookie = "ropeshift_csrf=token-from-cookie";
    const sent = serveViews((call) => (call.method === "POST" ? { status: 204 } : undefined));
    openPage("/work-order?id=wo-tripped");
    await userEvent.click(within(await card()).getByRole("button", { name: "Reset the breaker" }));
    await waitFor(() => expect(sent.some((call) => call.method === "POST")).toBe(true));
    const change = sent.find((call) => call.method === "POST");
    expect(change?.path).toBe("/v1/work-orders/wo-tripped/lane-reset");
    expect(change?.csrf).toBe("token-from-cookie");
  });

  it("asks why before it changes who owns work", async () => {
    const sent = serveViews((call) => (call.method === "POST" ? { status: 204 } : undefined));
    openPage("/work-order?id=wo-lapsed");
    const decision = await card();
    await userEvent.click(within(decision).getByRole("button", { name: "End this claim" }));
    expect(sent.some((call) => call.method === "POST")).toBe(false);
    await userEvent.type(within(decision).getByRole("textbox", { name: "Why (required)" }), "The worker is gone.");
    await userEvent.click(within(decision).getByRole("button", { name: "End this claim" }));
    await waitFor(() => expect(sent.find((call) => call.method === "POST")?.body).toEqual({ reason: "The worker is gone." }));
  });

  it("reads again after an action", async () => {
    const sent = serveViews((call) => (call.method === "POST" ? { status: 204 } : undefined));
    openPage("/work-order?id=wo-tripped");
    const decision = await card();
    const reads = sent.filter((call) => call.path === "/v1/needs-you").length;
    await userEvent.click(within(decision).getByRole("button", { name: "Reset the breaker" }));
    await waitFor(() => expect(sent.filter((call) => call.path === "/v1/needs-you").length).toBeGreaterThan(reads));
  });

  it("says what the server refused, and leaves the card standing", async () => {
    serveViews((call) => (call.method === "POST" ? refusal(409, "conflict", "A worker took it first.") : undefined));
    openPage("/work-order?id=wo-tripped");
    const decision = await card();
    await userEvent.click(within(decision).getByRole("button", { name: "Reset the breaker" }));
    expect((await within(decision).findByRole("alert")).textContent).toBe("A worker took it first.");
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
    openPage("/work-order?id=wo-tripped");
    await userEvent.click(within(await card()).getByRole("button", { name: "Reset the breaker" }));
    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeTruthy();
    expect(sent.filter((call) => call.method === "POST")).toHaveLength(1);
  });

  it("offers no action where the server offers none", async () => {
    serving<Stuck>("stuck", (view) => {
      for (const item of view.items) {
        item.actions = [];
      }
    });
    openPage("/work-order?id=wo-lapsed");
    await screen.findByRole("heading", { level: 1, name: /^wo-lapsed/ });
    const decision = await card();
    expect(within(decision).queryByRole("button")).toBeNull();
  });

  it("names a lane parked too long, the fifth of the design's list, and what it waits for", async () => {
    serving<Stuck>("stuck", (view) => {
      const first = view.items[0];
      if (first) {
        first.reason = "long_parked";
        first.actions = ["resume", "cancel"];
        first.wakes = [{ id: "wake-1", expression: `{"any_of":[{"all_of":[{"question_answered":"q-signing-key"}]},{"human_resume":true}]}` }];
      }
    });
    openPage("/work-order?id=wo-lapsed");
    const decision = await card();
    expect(within(decision).getByText("Long parked")).toBeTruthy();
    expect(within(decision).getByText(/^Its lane has stayed parked/).textContent).toBe(
      "Its lane has stayed parked for a long time. It waits until question q-signing-key is answered, or until a person resumes it.",
    );
    expect(within(decision).getByRole("link", { name: "q-signing-key" })).toBeTruthy();
    expect(within(decision).getByRole("button", { name: "Resume" })).toBeTruthy();
  });
});

// serveNamedRepository answers the project's repositories with a name for the one the work orders are in.
function serveNamedRepository() {
  const list = golden<{ repositories: { id: string; name?: string }[] }>("repositories");
  list.repositories.push({ ...list.repositories[0], id: "repository-1", name: "orders-api" });
  return serveViews((call) => (call.method === "GET" && call.path === "/v1/repositories" ? { status: 200, body: list } : undefined));
}

// statusOf finds a machine's segment of the status bar, which stands once the member is signed in.
const statusOf = (name: string) => waitFor(() => within(screen.getByRole("contentinfo", { name: "Status" })).getByRole("link", { name: new RegExp(`^${name}`) }));

describe("the status bar", () => {
  it("shows each machine's CPU, memory, and free disk, as its worker last measured them", async () => {
    serving<ShopFloor>("shop-floor", (view) => {
      for (const line of view.workers) {
        line.worker.report = measured();
      }
    });
    openPage("/floor");
    const machine = await statusOf("solo");
    expect(machine.textContent).toBe("soloCPU42%Mem38%Disk5.0 GiB");
  });

  it("says a machine whose worker has not reported has not", async () => {
    serveViews();
    openPage("/floor");
    const machine = await statusOf("solo");
    expect(machine.textContent).toBe("solonot reported");
  });
});

describe("the floor", () => {
  it("shows one panel per machine, with its orchestrator and how fresh its word is", async () => {
    serveViews();
    openPage("/floor");
    const machine = await screen.findByRole("region", { name: "solo" });
    expect(within(machine).getByText(/^Orchestrator/)).toBeTruthy();
    expect(within(machine).getAllByText(/^Last reported/).length).toBeGreaterThan(0);
  });

  it("says a machine whose worker has not reported has not, and never draws a measure as zero", async () => {
    serveViews();
    openPage("/floor");
    const machine = await screen.findByRole("region", { name: "solo" });
    for (const measure of ["CPU", "Memory", "Memory pressure", "Disk free", "Load per core"]) {
      expect(within(machine).getByText(measure).parentElement?.textContent).toBe(`${measure}not reported`);
    }
    expect(machine.textContent).not.toMatch(/\b0%/);
  });

  it("shows what the machine's worker last measured, as Workers and resources does", async () => {
    serving<ShopFloor>("shop-floor", (view) => {
      for (const line of view.workers) {
        line.worker.report = measured();
      }
    });
    openPage("/floor");
    const machine = await screen.findByRole("region", { name: "solo" });
    for (const [measure, value] of [
      ["CPU", "42%"],
      ["Memory", "12.0 GiB of 32.0 GiB"],
      ["Memory pressure", "1.5%"],
      ["Disk free", "5.0 GiB"],
      ["Load per core", "0.31"],
    ]) {
      expect(within(machine).getByText(measure as string).parentElement?.textContent).toBe(`${measure}${value}`);
    }
  });

  it("says a measure the machine cannot provide is unavailable", async () => {
    serving<ShopFloor>("shop-floor", (view) => {
      for (const line of view.workers) {
        line.worker.report = { ...measured(), machine: { cores: 8, volumes: [] } };
      }
    });
    openPage("/floor");
    const machine = await screen.findByRole("region", { name: "solo" });
    for (const measure of ["CPU", "Memory", "Memory pressure", "Disk free", "Load per core"]) {
      expect(within(machine).getByText(measure).parentElement?.textContent).toBe(`${measure}unavailable`);
    }
  });

  it("names the work each lane runs, with its state, liveness, and freshness", async () => {
    serveViews();
    openPage("/floor");
    const tr = await row("lane-wo-running");
    expect(tr.textContent).toContain("Write the docs");
    expect(tr.textContent).toContain("wo-running");
    expect(cell(tr, "Freshness")?.textContent).toMatch(/^Last reported.*4m ago/);
    expect(cell(tr, "Generation")?.textContent).toBe("1");
  });

  it("marks a last reported lane so it cannot pass for a live one", async () => {
    serveViews();
    openPage("/floor");
    expect((await row("lane-wo-running")).className).toContain("row-reported");
  });

  it("states an orchestrator's state in its chip", async () => {
    serving<ShopFloor>("shop-floor", (view) => {
      const orchestrator = view.workers[0]?.orchestrator;
      if (orchestrator) {
        orchestrator.actual_state = "working";
        orchestrator.current_generation = 5;
      }
    });
    openPage("/floor");
    const machine = await screen.findByRole("region", { name: "solo" });
    expect(within(machine).getByText(/^Orchestrator/).textContent).toBe("Orchestrator · working · gen 5");
  });

  it("says when a worker runs no orchestrator", async () => {
    serving<ShopFloor>("shop-floor", (view) => {
      if (view.workers[0]) {
        view.workers[0].orchestrator = undefined;
      }
    });
    openPage("/floor");
    expect(await screen.findByText("No orchestrator")).toBeTruthy();
  });

  it("offers a lane's act through a dialog that says what it does", async () => {
    const sent = serveViews((call) => (call.method === "POST" ? { status: 204 } : undefined));
    openPage("/floor");
    const tripped = golden<Stuck>("stuck").items.find((item) => item.reason === "tripped");
    if (!tripped?.lane) {
      throw new Error("the golden stuck view holds no tripped lane");
    }
    const tr = await row(tripped.lane.id);
    await userEvent.click(within(tr).getByRole("button", { name: `Actions for lane ${tripped.lane.id}` }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Reset the breaker…" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog.textContent).toContain("Closes the breaker");
    await userEvent.click(within(dialog).getByRole("button", { name: "Reset the breaker" }));
    await waitFor(() => expect(sent.find((call) => call.method === "POST")?.path).toBe(`/v1/work-orders/${tripped.work_order?.id}/lane-reset`));
  });

  it("says so when no worker has joined", async () => {
    serving<ShopFloor>("shop-floor", (view) => {
      view.workers = [];
      view.lanes = [];
    });
    openPage("/floor");
    expect(await screen.findByText("No worker has joined")).toBeTruthy();
  });
});

describe("the lane", () => {
  it("states its facts, freshness, and generation beside its pane", async () => {
    serveViews((call) => (call.path.startsWith("/v1/panes/") ? refusal(404, "no_pane", "no pane") : undefined));
    openPage("/lane?id=lane-wo-running");
    const facts = await screen.findByRole("complementary", { name: "Lane facts" });
    await waitFor(() => expect(within(facts).getByText("Generation").closest(".kv-row")?.textContent).toContain("1"));
    expect(within(facts).getByText("Seen").closest(".kv-row")?.textContent).toMatch(/Last reported/);
    expect(within(facts).getByRole("link", { name: "wo-running" }).getAttribute("href")).toBe(here("/work-order?id=wo-running"));
  });
});

describe("operations", () => {
  it("states the fleet's mode and its caps", async () => {
    serveViews();
    openPage("/measures");
    const fleet = await screen.findByRole("region", { name: "Fleet" });
    expect(within(fleet).getByText("Mode").closest("div")?.textContent).toContain("Run");
    expect(within(fleet).getByText("Work in progress").closest("div")?.textContent).toContain("No limit");
  });

  it("states a cap that is set", async () => {
    serving<Operations>("operations", (view) => {
      view.fleet.wip_limit = 3;
      view.fleet.fleet_mode = "drain";
    });
    openPage("/measures");
    const fleet = await screen.findByRole("region", { name: "Fleet" });
    expect(within(fleet).getByText("Work in progress").closest("div")?.textContent).toContain("3");
    expect(within(fleet).getByText("Mode").closest("div")?.textContent).toContain("Drain");
  });

  it("counts what waits for a person, and leads there", async () => {
    serveViews();
    openPage("/measures");
    const fleet = await screen.findByRole("region", { name: "Fleet" });
    const waiting = within(fleet).getByText("Waiting for a person").closest("div");
    expect(waiting?.textContent).toContain("11");
    expect(within(waiting as HTMLElement).getByRole("link").getAttribute("href")).toBe("/needs-you");
  });

  it("counts workers against those it has heard from", async () => {
    serveViews();
    openPage("/measures");
    const fleet = await screen.findByRole("region", { name: "Fleet" });
    expect(within(fleet).getByText("Workers").closest("div")?.textContent).toContain("0 of 1 observed");
  });

  it("states a zero where a state holds nothing", async () => {
    serveViews();
    openPage("/measures");
    const table = await screen.findByRole("table", { name: "Work orders by state" });
    expect(within(table).getByRole("row", { name: "Claimed 0" })).toBeTruthy();
    expect(within(table).getByRole("row", { name: "Running 5" })).toBeTruthy();
  });

  it("says a lane's count is last word, and where its freshness stands", async () => {
    serveViews();
    openPage("/measures");
    const note = await screen.findByText(/each lane's last state/);
    expect(within(note).getByRole("link", { name: "the floor" }).getAttribute("href")).toBe(here("/floor"));
  });

  it("keeps a state this build has no words for in the count", async () => {
    serving<Operations>("operations", (view) => {
      view.work_orders.brand_new = 2;
    });
    openPage("/measures");
    const table = await screen.findByRole("table", { name: "Work orders by state" });
    expect(within(table).getByRole("row", { name: "brand_new 2" })).toBeTruthy();
  });

  it("states each source, its health, and what it could not read", async () => {
    serveViews();
    openPage("/measures");
    const sources = await screen.findByRole("region", { name: "Sources" });
    expect(sources.textContent).toContain("repofiles");
    expect(sources.textContent).toContain("repository-1");
    expect(sources.textContent).toContain("Ok");
    expect(sources.textContent).toContain("plans/bad.md");
  });

  it("states each worker's gh sign-in for a repository that reviews through pull requests", async () => {
    serving<Operations>("operations", (view) => {
      view.forges = [
        { worker: "worker-1", worker_name: "solo", repository: "repo-1", repository_name: "widgets", state: "signed_out", detail: "the token in keyring is invalid", checked_at: 1 },
        { worker: "worker-1", worker_name: "solo", repository: "repo-2", state: "ok", checked_at: 1 },
      ];
    });
    openPage("/measures");
    const forge = await screen.findByRole("region", { name: "Forge sign-in" });
    const rows = within(forge).getAllByRole("listitem").filter((item) => item.classList.contains("list-row"));
    expect(rows).toHaveLength(2);
    const [signedOut, ok] = rows.map((row) => row.textContent ?? "");
    expect(signedOut).toContain("widgets");
    expect(signedOut).toContain("Signed out");
    expect(signedOut).toContain("solo");
    expect(signedOut).toContain("the token in keyring is invalid");
    expect(ok).toContain("repo-2");
    expect(ok).toContain("Ok");
  });

  it("leaves the forge out where no repository needs it", async () => {
    serveViews();
    openPage("/measures");
    await screen.findByRole("region", { name: "Sources" });
    expect(screen.queryByRole("region", { name: "Forge sign-in" })).toBeNull();
  });

  it("shows the north star beside the overturn rate and rework", async () => {
    serveViews();
    openPage("/measures");
    const measures = await screen.findByRole("region", { name: "Product measures" });
    const star = within(measures).getByRole("group", { name: "The north star and its counterweights" });
    const names = within(star)
      .getAllByRole("term")
      .map((term) => term.textContent);
    expect(names).toEqual(["Interruptions per completed work order", "Overturn rate", "Rework", "Autonomous decision rate"]);
    expect(within(star).getByText("Interruptions per completed work order").closest("div")?.textContent).toContain("1 (2 over 2)");
    expect(within(star).getByText("Overturn rate").closest("div")?.textContent).toContain("0% (0 of 1)");
    expect(measures.textContent).toContain("over the last 7d");
  });

  it("says a rate of nothing has no data, rather than zero", async () => {
    serving<Operations>("operations", (view) => {
      view.measures.interruptions_per_completed = { numerator: 3, denominator: 0, value: null };
    });
    openPage("/measures");
    const measures = await screen.findByRole("region", { name: "Product measures" });
    expect(within(measures).getByText("Interruptions per completed work order").closest("div")?.textContent).toContain("No data yet");
    expect(within(measures).getByText("Autonomous decision rate").closest("div")?.textContent).toContain("No data yet");
  });

  it("counts a batch of questions as one interruption, and names each kind", async () => {
    serving<Operations>("operations", (view) => {
      view.measures.interruptions = 4;
      view.measures.interruptions_by_kind = { batch: 2, approval: 1, forced: 1 };
    });
    openPage("/measures");
    const measures = await screen.findByRole("region", { name: "Product measures" });
    expect(within(measures).getByText("Interruptions").closest("div")?.textContent).toBe(
      "Interruptions2 batches of questions, 1 approval, 1 forced action",
    );
  });

  it("says one of each kind of interruption in the singular", async () => {
    serving<Operations>("operations", (view) => {
      view.measures.interruptions = 1;
      view.measures.interruptions_by_kind = { batch: 1 };
    });
    openPage("/measures");
    const measures = await screen.findByRole("region", { name: "Product measures" });
    expect(within(measures).getByText("Interruptions").closest("div")?.textContent).toBe("Interruptions1 batch of questions, 0 approvals, 0 forced actions");
  });

  it("shows review agreement for each repository beside the merge authority in words", async () => {
    serving<Operations>("operations", (view) => {
      const period = (decisions: number, agreed: number, disagreed: number, overturned: number, later: number, overrode: number) => ({
        decisions,
        agreed,
        disagreed,
        unknown: decisions - agreed - disagreed,
        agreement_rate: { numerator: agreed, denominator: agreed + disagreed, value: agreed + disagreed ? agreed / (agreed + disagreed) : null },
        approvals_overturned: overturned,
        later_defects: later,
        failures_overridden: overrode,
      });
      view.measures.review_agreement = {
        workspace: { last_30_days: period(8, 4, 3, 2, 1, 1), all_time: period(10, 5, 4, 3, 1, 1) },
        repositories: [
          { repository_id: "repository-1", merge_authority: "person_approves", last_30_days: period(8, 4, 3, 2, 1, 1), all_time: period(10, 5, 4, 3, 1, 1) },
          { repository_id: "repository-2", merge_authority: "review_approves", last_30_days: period(0, 0, 0, 0, 0, 0), all_time: period(0, 0, 0, 0, 0, 0) },
        ],
      };
    });
    openPage("/measures");
    const section = await screen.findByRole("region", { name: "Review agreement" });
    const first = within(section).getByRole("group", { name: "Review agreement for repository-1" });
    expect(first.textContent).toContain("Merge authority: person approves, you approve each change and ropeshift lands it");
    expect(within(first).getByText("Decisions after a review").closest("div")?.textContent).toContain("8 in the last 30 days, 10 over all time");
    expect(within(first).getByText("You agreed with the review").closest("div")?.textContent).toContain("57.1% (4 of 7) in the last 30 days, 55.6% (5 of 9) over all time");
    expect(within(first).getByText("Approvals you overturned").closest("div")?.textContent).toContain(
      "2 in the last 30 days (1 of them a later defect), 3 over all time (1 of them a later defect)",
    );
    expect(within(first).getByText("Failures you overrode").closest("div")?.textContent).toContain("1 in the last 30 days, 1 over all time");
    const second = within(section).getByRole("group", { name: "Review agreement for repository-2" });
    expect(second.textContent).toContain("Merge authority: review approves, the review's approval stands for yours once every criterion passed");
    expect(second.textContent).toContain("No decision after a review yet");
    expect(within(section).getByRole("group", { name: "Review agreement for the whole workspace" }).textContent).toContain("10 over all time");
  });

  it("reads review agreement from the ledger's own fixture", async () => {
    serveViews();
    openPage("/measures");
    const section = await screen.findByRole("region", { name: "Review agreement" });
    const repository = within(section).getByRole("group", { name: "Review agreement for repository-1" });
    expect(repository.textContent).toContain("Merge authority: person approves");
    expect(within(repository).getByText("Decisions after a review").closest("div")?.textContent).toContain("1 in the last 30 days, 1 over all time");
    expect(within(repository).getByText("You agreed with the review").closest("div")?.textContent).toContain("100% (1 of 1)");
  });

  it("states cost as a lower bound when some of it went unreported", async () => {
    serving<Operations>("operations", (view) => {
      view.measures.cost_per_outcome = { change: { total_usd: 3, done: 2, per_done_usd: 1.5, lower_bound: true } };
    });
    openPage("/measures");
    const table = await screen.findByRole("table", { name: "Cost per outcome" });
    expect(within(table).getByRole("row").textContent).toContain("at least $1.50 each, $3.00 over 2 done");
  });

  it("states money in dollars and cents, and a cost under a cent as under a cent", async () => {
    serving<Operations>("operations", (view) => {
      view.measures.cost_per_outcome = {
        change: { total_usd: 2.6, done: 2, per_done_usd: 1.3, lower_bound: false },
        plan: { total_usd: 0.004, done: 1, per_done_usd: 0.004, lower_bound: false },
      };
    });
    openPage("/measures");
    const table = await screen.findByRole("table", { name: "Cost per outcome" });
    const [change, plan] = within(table).getAllByRole("row");
    expect(change?.textContent).toBe("Change$1.30 each, $2.60 over 2 done");
    expect(plan?.textContent).toBe("Planunder $0.01 each, under $0.01 over 1 done");
  });

  it("states park time by trigger, at the median and the 95th percentile", async () => {
    serving<Operations>("operations", (view) => {
      view.measures.park_time = { answer: { count: 3, median_ms: 1_800_000, p95_ms: 7_200_000 } };
    });
    openPage("/measures");
    const table = await screen.findByRole("table", { name: "Park time by trigger" });
    expect(within(table).getByRole("row").textContent).toContain("median 30m, 95th percentile 2h, of 3");
  });
});

describe("a work view on the stream", () => {
  it("reads again when something happens", async () => {
    const sent = serveViews();
    openPage("/work?group=stuck");
    await row("Fix the flaky test");
    const stream = await openLedgerStream();
    const reads = sent.filter((call) => call.path === "/v1/views/stuck").length;
    stream.emit();
    await waitFor(() => expect(sent.filter((call) => call.path === "/v1/views/stuck").length).toBe(reads + 1));
  });
});
