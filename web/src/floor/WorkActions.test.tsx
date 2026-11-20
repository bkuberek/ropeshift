import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { NeedsYou, Plans, Search, ShopFloor, Stuck } from "../api/types";
import { openPage } from "../opening";
import type { Sent } from "../testing";
import { golden, refusal, serveViews } from "../testing";
import { recoverActions } from "./recoverSpecs";

type Served = {
  plans?: (view: Plans) => void;
  floor?: (view: ShopFloor) => void;
  stuck?: (view: Stuck) => void;
  needs?: (view: NeedsYou) => void;
  search?: Search["records"];
  post?: (call: Sent) => { status: number; body?: unknown } | undefined;
};

// serving serves the golden project with the changes a test needs, and what a change answers. A change
// nothing answers is a 200.
function serving({ plans, floor, stuck, needs, search, post }: Served = {}) {
  const view = golden<Plans>("plans");
  plans?.(view);
  const shop = golden<ShopFloor>("shop-floor");
  floor?.(shop);
  const stopped = golden<Stuck>("stuck");
  stuck?.(stopped);
  const waiting = golden<NeedsYou>("needs-you");
  needs?.(waiting);
  return serveViews((call) => {
    if (call.method === "POST") {
      return post?.(call) ?? { status: 200, body: {} };
    }
    if (call.path.startsWith("/v1/views/plans")) {
      return { status: 200, body: view };
    }
    if (call.path.startsWith("/v1/views/shop-floor")) {
      return { status: 200, body: shop };
    }
    if (call.path.startsWith("/v1/views/stuck")) {
      return { status: 200, body: stopped };
    }
    if (call.path === "/v1/needs-you" || call.path.startsWith("/v1/views/needs-you")) {
      return { status: 200, body: waiting };
    }
    if (search && call.path.startsWith("/v1/views/search")) {
      return { status: 200, body: { as_of: 1, revision: 1, records: search } };
    }
    if (call.path === "/v1/workers") {
      return { status: 200, body: { workers: [{ id: "worker-a", name: "solo", epoch: 1 }] } };
    }
    return undefined;
  });
}

const changes = (sent: Sent[]) => sent.filter((call) => call.method === "POST");

const dialog = (name: string | RegExp) => screen.findByRole("dialog", { name });

const heading = (id: string) => screen.findByRole("heading", { level: 1, name: new RegExp(`^${id}`) });

// openMenu opens the page's menu of more actions and lists what it offers.
async function openMenu() {
  await userEvent.click(await screen.findByRole("button", { name: "More actions" }));
  const menu = await screen.findByRole("menu", { name: "More actions" });
  return { menu, items: within(menu).getAllByRole("menuitem") };
}

const orderOf = (view: Plans, id: string) => [...view.plans.flatMap((plan) => plan.work_orders), ...view.unplanned].find((order) => order.id === id);

// claimed makes the ready work order a claimed one that a worker holds, as the server's claim leaves it.
const claimed = (view: Plans) => {
  const order = orderOf(view, "generated-28");
  if (order) {
    order.state = "claimed";
    order.claim_id = "claim-held";
    order.fence = 3;
    order.lease_worker = "worker-a";
    view.work_actions["generated-28"] = { actions: ["start_lane", "release", "cancel"] };
  }
};

// A ledger that holds a work order in every state lists every action the server offers, so a new action fails here
// until the page has words for it, and a page that words an action no state offers fails too.
describe("the actions the server lists", () => {
  it("are each worded here, and each worded action is one some state offers", () => {
    const listed = golden<{ views: { plans: Plans } }>("every-state").views.plans;
    const named = new Set(Object.values(listed.work_actions).flatMap((allowance) => allowance.actions));
    const known = new Set<string>(["claim", "start_lane", ...recoverActions]);
    expect([...named].filter((action) => !known.has(action))).toEqual([]);
    expect(recoverActions.filter((action) => !named.has(action))).toEqual([]);
  });
});

describe("the actions a work order allows", () => {
  it("offers Cancel on work that runs, which the server never listed as stuck", async () => {
    document.cookie = "ropeshift_csrf=token-from-cookie";
    const sent = serving();
    openPage("/work-order?id=wo-running");
    await heading("wo-running");
    const { items } = await openMenu();
    expect(items.map((item) => item.textContent)).toEqual(["Cancel this work…"]);
    await userEvent.click(items[0] as HTMLElement);
    const box = await dialog("Cancel this work: Write the docs");
    expect(box.textContent).toContain("Work order wo-running is running.");
    const confirm = within(box).getByRole("button", { name: "Cancel this work" });
    expect((confirm as HTMLButtonElement).disabled).toBe(true);
    await userEvent.type(within(box).getByRole("textbox", { name: "Reason (required)" }), "The plan changed.");
    expect(changes(sent)).toHaveLength(0);
    await userEvent.click(confirm);
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ path: "/v1/work-orders/wo-running/cancel", project: "default", csrf: "token-from-cookie", body: { reason: "The plan changed." } });
    expect((await screen.findAllByText("Canceled Write the docs.")).length).toBeGreaterThan(0);
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Cancel this work: Write the docs" })).toBeNull());
  });

  // The menu holds what the plans view says, and nothing else. A decision card beside the page offers some
  // of the same, which the menu leaves to it.
  it.each([
    ["wo-running", ["Cancel this work…"]],
    ["wo-review", ["Send back to planning…", "Cancel this work…"]],
    ["generated-28", ["Cancel this work…"]],
    ["wo-done", ["Archive the worktree of lane lane-wo-done…"]],
    ["wo-blocked", ["Archive the worktree of lane lane-wo-blocked…"]],
    ["wo-failed", ["Archive the worktree of lane lane-wo-failed…"]],
    ["wo-canceled", []],
    ["wo-lapsed", []],
  ])("offers %s what its state allows, and no other action", async (id, labels) => {
    serving();
    openPage(`/work-order?id=${id}`);
    await heading(id);
    if (labels.length === 0) {
      expect(screen.queryByRole("button", { name: "More actions" })).toBeNull();
    } else {
      const { items } = await openMenu();
      expect(items.map((item) => item.textContent)).toEqual(labels);
    }
    cleanup();
  });

  it("follows the plans view, so no page decides by a state of its own", async () => {
    serving({
      plans: (view) => {
        // A running work order the server says may also be sent back, which no state rule of this page would offer.
        view.work_actions["wo-running"] = { actions: ["send_back", "cancel"] };
      },
    });
    openPage("/work-order?id=wo-running");
    await heading("wo-running");
    expect((await openMenu()).items.map((item) => item.textContent)).toEqual(["Send back to planning…", "Cancel this work…"]);
  });

  it("shows the API's words when an action is refused, and keeps the dialog", async () => {
    const sent = serving({ post: () => refusal(409, "illegal_move", "canceling done work: illegal move") });
    openPage("/work-order?id=wo-running");
    await heading("wo-running");
    await userEvent.click((await openMenu()).items[0] as HTMLElement);
    const box = await dialog("Cancel this work: Write the docs");
    await userEvent.type(within(box).getByRole("textbox", { name: "Reason (required)" }), "Why not.");
    await userEvent.click(within(box).getByRole("button", { name: "Cancel this work" }));
    expect((await within(box).findByRole("alert")).textContent).toBe("canceling done work: illegal move");
    expect(changes(sent)).toHaveLength(1);
    expect(box.contains(document.activeElement)).toBe(true);
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("is reached from the keyboard alone: the menu button, the arrow keys, Enter, and Escape", async () => {
    serving();
    openPage("/work-order?id=wo-review");
    await heading("wo-review");
    const button = await screen.findByRole("button", { name: "More actions" });
    expect(button.tagName).toBe("BUTTON");
    button.focus();
    await userEvent.keyboard("{Enter}");
    const menu = await screen.findByRole("menu", { name: "More actions" });
    const [first, second] = within(menu).getAllByRole("menuitem");
    expect(document.activeElement).toBe(first);
    await userEvent.keyboard("{ArrowDown}");
    expect(document.activeElement).toBe(second);
    await userEvent.keyboard("{Enter}");
    expect(await dialog("Cancel this work: Document the retry budget")).toBeTruthy();
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(document.activeElement).toBe(button);
  });
});

describe("failed work", () => {
  it("asks for a retry that adds an attempt, from its card, with a reason", async () => {
    const sent = serving();
    openPage("/work-order?id=wo-failed");
    await heading("wo-failed");
    const card = await screen.findByRole("article", { name: /Try the impossible/ });
    expect(card.textContent).toContain("Its lane failed, and nothing retries failed work on its own.");
    expect(card.textContent).toContain("It used every attempt it was given, so a retry adds one, which only a member can do.");
    await userEvent.click(within(card).getByRole("button", { name: "Retry with an extra attempt" }));
    expect(changes(sent)).toHaveLength(0);
    const confirm = within(card).getByRole("button", { name: "Retry with an extra attempt" });
    expect((confirm as HTMLButtonElement).disabled).toBe(false);
    const why = within(card).getByRole("textbox", { name: "Why (optional)" }) as HTMLTextAreaElement;
    expect(why.value).toBe("");
    await userEvent.type(why, "The network came back.");
    await userEvent.click(confirm);
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ path: "/v1/work-orders/wo-failed/retry", body: { reason: "The network came back." } });
    expect(JSON.stringify(changes(sent)[0]?.body)).not.toContain("It gave up on");
  });

  it("names the plain retry when an attempt is left", async () => {
    serving({
      stuck: (view) => {
        for (const item of view.items) {
          if (item.id === "wo-failed") {
            delete item.extra_attempt;
          }
        }
      },
    });
    openPage("/work-order?id=wo-failed");
    await heading("wo-failed");
    const card = await screen.findByRole("article", { name: /Try the impossible/ });
    expect(within(card).getByRole("button", { name: "Retry" })).toBeTruthy();
    expect(card.textContent).not.toContain("used every attempt");
  });

  it("offers Retry in the page's header, in a dialog, when no card stands beside it", async () => {
    // Nothing waits on it, as when a retry went through and another read has not caught up.
    const sent = serving({
      stuck: (view) => void (view.items = []),
      needs: (view) => void (view.items = view.items.filter((item) => item.id !== "wo-failed")),
    });
    openPage("/work-order?id=wo-failed");
    await heading("wo-failed");
    await userEvent.click(await screen.findByRole("button", { name: "Retry with an extra attempt" }));
    const box = await dialog("Retry with an extra attempt: Try the impossible");
    expect(box.textContent).toContain("used every attempt it was given, 1 of 1, so this adds one");
    // No read says what the last attempt's lane recorded, so nothing is quoted above the note.
    expect(within(box).queryByRole("figure", { name: "The lane recorded" })).toBeNull();
    const reason = within(box).getByRole("textbox", { name: "Reason (optional)" }) as HTMLTextAreaElement;
    expect(reason.value).toBe("");
    await userEvent.type(reason, "Again.");
    await userEvent.click(within(box).getByRole("button", { name: "Retry with an extra attempt" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ path: "/v1/work-orders/wo-failed/retry", body: { reason: "Again." } });
  });

  it("quotes what the lane recorded above the header's Retry note, and sends the fixed words for an empty note", async () => {
    // The card beside the page offers Retry, so the header's dialog opens from the work order's address.
    const sent = serving();
    openPage("/work-order?id=wo-failed&dialog=retry");
    await heading("wo-failed");
    const box = await dialog("Retry with an extra attempt: Try the impossible");
    const quoted = within(box).getByRole("figure", { name: "The lane recorded" });
    expect(quoted.textContent).toContain("It gave up on <img src=x onerror=alert(1)><script>alert(2)</script>");
    expect(box.querySelector("img, script")).toBeNull();
    const reason = within(box).getByRole("textbox", { name: "Reason (optional)" }) as HTMLTextAreaElement;
    expect(reason.value).toBe("");
    await userEvent.click(within(box).getByRole("button", { name: "Retry with an extra attempt" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ path: "/v1/work-orders/wo-failed/retry", body: { reason: "Retried after the recorded failure" } });
    expect(JSON.stringify(changes(sent)[0]?.body)).not.toContain("It gave up on");
  });
});

describe("a claim", () => {
  it("is given back after a question that names the worker, with the claim and its fence", async () => {
    const sent = serving({ plans: claimed });
    openPage("/work-order?id=generated-28");
    await heading("generated-28");
    const { items } = await openMenu();
    expect(items.map((item) => item.textContent)).toEqual(["Release the claim…", "Cancel this work…"]);
    await userEvent.click(items[0] as HTMLElement);
    const box = await dialog("Release the claim: Write the guide");
    await waitFor(() => expect(box.textContent).toContain("solo holds the claim."));
    expect(within(box).queryByRole("textbox")).toBeNull();
    expect(changes(sent)).toHaveLength(0);
    await userEvent.click(within(box).getByRole("button", { name: "Release the claim" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ path: "/v1/work-orders/generated-28/claims/claim-held/release", body: { fence: 3 } });
    expect((await screen.findAllByText("Gave back the claim on Write the guide.")).length).toBeGreaterThan(0);
  });

  it("names the worker by its ID when the workers cannot be read", async () => {
    serveViews((call) => {
      if (call.path.startsWith("/v1/views/plans")) {
        const view = golden<Plans>("plans");
        claimed(view);
        return { status: 200, body: view };
      }
      return call.path === "/v1/workers" ? refusal(503, "unavailable", "nothing here") : undefined;
    });
    openPage("/work-order?id=generated-28&dialog=release");
    const box = await dialog("Release the claim: Write the guide");
    await waitFor(() => expect(box.textContent).toContain("worker-a holds the claim."));
  });
});

describe("a worktree", () => {
  it("is archived after the worker's refusal is read, and forced only on a second press", async () => {
    const sent = serving({
      post: (call) => {
        const forced = (call.body as { force?: boolean }).force;
        return call.path.endsWith("/lane-archive") && !forced ? refusal(409, "unpushed_work", "the worktree holds work no remote has: lane lane-wo-done") : undefined;
      },
    });
    openPage("/work-order?id=wo-done&tab=lanes");
    await heading("wo-done");
    const list = await screen.findByRole("list", { name: "Worktrees to remove" });
    await userEvent.click(within(list).getByRole("button", { name: "Archive the worktree of lane lane-wo-done" }));
    const box = await dialog("Archive the worktree: lane lane-wo-done");
    await userEvent.click(within(box).getByRole("button", { name: "Archive the worktree" }));
    expect((await within(box).findByRole("alert")).textContent).toBe("the worktree holds work no remote has: lane lane-wo-done");
    expect(changes(sent)).toHaveLength(1);
    expect(changes(sent)[0]?.body).toEqual({ lane_id: "lane-wo-done" });
    expect(within(box).queryByRole("button", { name: "Archive the worktree" })).toBeNull();
    await userEvent.click(within(box).getByRole("button", { name: "Archive anyway" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(2));
    expect(changes(sent)[1]).toMatchObject({ path: "/v1/work-orders/wo-done/lane-archive", body: { lane_id: "lane-wo-done", force: true } });
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Archive the worktree: lane lane-wo-done" })).toBeNull());
  });

  it("is archived in one press when nothing unpushed holds it", async () => {
    const sent = serving();
    openPage("/work-order?id=wo-done&tab=lanes");
    await heading("wo-done");
    await userEvent.click(await screen.findByRole("button", { name: "Archive the worktree of lane lane-wo-done" }));
    await userEvent.click(within(await dialog("Archive the worktree: lane lane-wo-done")).getByRole("button", { name: "Archive the worktree" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.body).toEqual({ lane_id: "lane-wo-done" });
  });

  it("is offered for no lane that still runs, or that the worker has removed", async () => {
    serving({
      floor: (view) => {
        for (const lane of view.lanes) {
          if (lane.id === "lane-wo-done") {
            lane.archivable = false;
          }
        }
      },
    });
    openPage("/work-order?id=wo-done&tab=lanes");
    await heading("wo-done");
    await screen.findByRole("heading", { name: "Lane lane-wo-done" });
    expect(screen.queryByRole("list", { name: "Worktrees to remove" })).toBeNull();
    expect(screen.queryByRole("button", { name: "More actions" })).toBeNull();
  });
});

describe("the command palette", () => {
  const palette = () => screen.findByRole("dialog", { name: "Command palette" });

  it("lists each action the work order in view allows, which opens its dialog", async () => {
    serving();
    openPage("/work-order?id=wo-running");
    await heading("wo-running");
    await userEvent.keyboard("{Control>}k{/Control}");
    await userEvent.click(await within(await palette()).findByRole("option", { name: /^Cancel this work: Write the docs/ }));
    expect(await dialog("Cancel this work: Write the docs")).toBeTruthy();
  });

  it("lists nothing the work order does not allow", async () => {
    serving();
    openPage("/work-order?id=wo-running");
    await heading("wo-running");
    await userEvent.keyboard("{Control>}k{/Control}");
    const options = within(await palette())
      .getAllByRole("option")
      .map((option) => option.textContent ?? "");
    expect(options.some((text) => /^(Retry|Release the claim|Send back to planning|Archive the worktree)/.test(text))).toBe(false);
    expect(options.some((text) => text.startsWith("Cancel this work"))).toBe(true);
  });

  it("keeps the page's actions beside the start of work", async () => {
    serving();
    openPage("/work-order?id=generated-28");
    await heading("generated-28");
    await userEvent.keyboard("{Control>}k{/Control}");
    const options = within(await palette())
      .getAllByRole("option")
      .map((option) => option.textContent ?? "");
    expect(options.some((text) => text.startsWith("Start work: Write the guide"))).toBe(true);
    expect(options.some((text) => text.startsWith("Cancel this work: Write the guide"))).toBe(true);
  });

  it("lists the actions of a work order the search found, which opens its page on that dialog", async () => {
    serving({
      search: [
        { kind: "work_order", id: "wo-failed", title: "Try the impossible", state: "failed", actions: ["retry", "archive_lane"] },
        { kind: "work_order", id: "wo-running", title: "Write the docs", state: "running", actions: ["cancel"] },
        { kind: "work_order", id: "wo-canceled", title: "Drop the old plan", state: "canceled" },
      ],
    });
    openPage("/needs-you");
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    await userEvent.keyboard("{Control>}k{/Control}");
    await userEvent.type(within(await palette()).getByRole("combobox"), "the");
    const found = await waitFor(() => {
      const options = within(screen.getByRole("dialog", { name: "Command palette" })).getAllByRole("option");
      if (!options.some((option) => option.textContent?.startsWith("Retry: Try the impossible"))) {
        throw new Error("no Retry entry");
      }
      return options.map((option) => option.textContent ?? "");
    });
    expect(found.filter((text) => text.startsWith("Retry:"))).toHaveLength(1);
    // A work order that allows no action offers none, and the worktree is chosen on the page, which says which.
    expect(found.some((text) => text.startsWith("Archive the worktree"))).toBe(false);
    expect(found.some((text) => /^[A-Z][^:]*: Drop the old plan/.test(text))).toBe(false);
    await userEvent.click(screen.getAllByRole("option").find((option) => option.textContent?.startsWith("Retry: Try the impossible")) as HTMLElement);
    expect(`${window.location.pathname}${window.location.search}`).toContain("/work-order?id=wo-failed&dialog=retry");
    expect(await dialog("Retry with an extra attempt: Try the impossible")).toBeTruthy();
  });

  it("opens a page on the dialog its address names, and on none that its state does not allow", async () => {
    serving();
    openPage("/work-order?id=wo-running&dialog=cancel");
    expect(await dialog("Cancel this work: Write the docs")).toBeTruthy();
    cleanup();
    openPage("/work-order?id=wo-running&dialog=retry");
    await heading("wo-running");
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

describe("the lanes on the floor", () => {
  const menuOf = async (lane: string) => {
    await userEvent.click(await screen.findByRole("button", { name: `Actions for lane ${lane}` }));
    return within(await screen.findByRole("menu")).getAllByRole("menuitem");
  };

  it("offers the work a lane runs the actions its state allows, Cancel among them whatever the state", async () => {
    const sent = serving();
    openPage("/floor");
    const items = await menuOf("lane-wo-running");
    expect(items.map((item) => item.textContent)).toEqual(["Cancel this work…"]);
    await userEvent.click(items[0] as HTMLElement);
    const box = await dialog("Cancel this work: Write the docs");
    await userEvent.type(within(box).getByRole("textbox", { name: "Reason (required)" }), "Not needed.");
    await userEvent.click(within(box).getByRole("button", { name: "Cancel this work" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ path: "/v1/work-orders/wo-running/cancel", body: { reason: "Not needed." } });
  });

  it("keeps what the server offers for the lane itself first, then the work's own", async () => {
    serving();
    openPage("/floor");
    expect((await menuOf("lane-wo-tripped")).map((item) => item.textContent)).toEqual(["Reset the breaker…", "Cancel this work…"]);
  });

  it("offers a lane that ended its worktree to archive, and the retry of the work that failed", async () => {
    const sent = serving();
    openPage("/floor");
    const items = await menuOf("lane-wo-failed");
    expect(items.map((item) => item.textContent)).toEqual(["Retry with an extra attempt…", "Archive the worktree…"]);
    await userEvent.click(items[1] as HTMLElement);
    await userEvent.click(within(await dialog("Archive the worktree: lane lane-wo-failed")).getByRole("button", { name: "Archive the worktree" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ path: "/v1/work-orders/wo-failed/lane-archive", body: { lane_id: "lane-wo-failed" } });
  });

  it("offers no worktree to archive for a lane that runs", async () => {
    serving();
    openPage("/floor");
    const items = await menuOf("lane-wo-stalled");
    expect(items.map((item) => item.textContent).some((text) => text?.startsWith("Archive"))).toBe(false);
  });
});
