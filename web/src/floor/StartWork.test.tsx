import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { Autonomy, WorkerRecord } from "../api/admin";
import type { InProgress, Plans, Search, ShopFloor } from "../api/types";
import { openPage } from "../opening";
import type { Sent } from "../testing";
import { golden, groupHealth, here, refusal, serveViews } from "../testing";

// The golden project's one ready work order, which a plan holds, and its one worker.
const ready = "generated-28";
const title = "Write the guide";

type Served = {
  plans?: (view: Plans) => void;
  floor?: (view: ShopFloor) => void;
  running?: (view: InProgress) => void;
  workers?: WorkerRecord[];
  search?: Search["records"];
  group?: boolean;
  post?: (call: Sent) => { status: number; body?: unknown } | undefined;
};

const solo: WorkerRecord = { id: "worker-a", name: "solo", epoch: 1, manifest: { capacity: { max_lanes: 4 } } };
const duo: WorkerRecord = { id: "worker-b", name: "duo", epoch: 1, manifest: { capacity: { max_lanes: 2 } } };

const autonomy = (worker: string): Autonomy => ({ as_of: 1, worker_id: worker, sign_in: "", level: "attended", risk_acknowledged: true, may_claim: true });

// serving serves the golden project, with the changes a test needs: the plans' work orders, the shop
// floor, the workers the installation holds, and what a change answers. A change nothing answers is a 200.
function serving({ plans, floor, running, workers = [solo], search, group = false, post }: Served = {}) {
  const view = golden<Plans>("plans");
  plans?.(view);
  const shop = golden<ShopFloor>("shop-floor");
  floor?.(shop);
  const lanes = golden<InProgress>("in-progress");
  running?.(lanes);
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
    if (call.path.startsWith("/v1/views/in-progress")) {
      return { status: 200, body: lanes };
    }
    if (search && call.path.startsWith("/v1/views/search")) {
      return { status: 200, body: { as_of: 1, revision: 1, records: search } };
    }
    if (group && call.method === "GET" && call.path === "/v1/health") {
      return { status: 200, body: groupHealth };
    }
    if (call.path === "/v1/workers") {
      return { status: 200, body: { workers } };
    }
    const asked = /^\/v1\/workers\/([^/]+)\/autonomy$/.exec(call.path);
    if (asked) {
      return { status: 200, body: autonomy(decodeURIComponent(asked[1] ?? "")) };
    }
    return undefined;
  });
}

const orderOf = (view: Plans, id: string) => [...view.plans.flatMap((plan) => plan.work_orders), ...view.unplanned].find((order) => order.id === id);

// claimed makes the ready work order a claimed one, as a claim that succeeded and a lane start that did
// not leaves it. The plans view says what a claimed work order allows, as the server does.
const claimed = (view: Plans) => {
  const order = orderOf(view, ready);
  if (order) {
    order.state = "claimed";
    order.lease_worker = "worker-a";
    view.work_actions[ready] = { actions: ["start_lane", "release", "cancel"] };
  }
};

const changes = (sent: Sent[]) => sent.filter((call) => call.method === "POST");

const dialog = (name: string) => screen.findByRole("dialog", { name });

// The page's own button, which opens the dialog. Its name is exactly Start, or Start lane.
const pageButton = async (name: string) => (await screen.findByRole("heading", { level: 1, name: new RegExp(`^${ready}`) }), screen.findByRole("button", { name }));

describe("starting a ready work order", () => {
  it("claims it for the worker, then starts its lane, in that order, and says so", async () => {
    document.cookie = "ropeshift_csrf=token-from-cookie";
    const sent = serving();
    openPage(`/work-order?id=${ready}`);
    await userEvent.click(await pageButton("Start"));
    const box = await dialog(`Start ${title}`);
    const worker = await within(box).findByRole("combobox", { name: "Worker" });
    await waitFor(() => expect((worker as HTMLSelectElement).value).toBe("worker-a"));
    expect(changes(sent)).toHaveLength(0);
    await userEvent.click(within(box).getByRole("button", { name: "Start" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(2));
    const [claim, lane] = changes(sent);
    expect(claim?.path).toBe(`/v1/work-orders/${ready}/claims`);
    expect(claim?.project).toBe("default");
    expect(claim?.csrf).toBe("token-from-cookie");
    expect(claim?.body).toMatchObject({ worker_id: "worker-a" });
    expect((claim?.body as { claim_id: string }).claim_id).toMatch(/^[0-9A-Z]{26}$/);
    expect(lane?.path).toBe(`/v1/work-orders/${ready}/lanes`);
    expect((lane?.body as { lane_id: string }).lane_id).toMatch(/^[0-9A-Z]{26}$/);
    expect((await screen.findAllByText(`Claimed ${title} on solo. Its lane is starting.`)).length).toBeGreaterThan(0);
    await waitFor(() => expect(screen.queryByRole("dialog", { name: `Start ${title}` })).toBeNull());
  });

  it("names the lane the member chose, and lets the ledger name one otherwise", async () => {
    const sent = serving();
    openPage(`/work-order?id=${ready}`);
    await userEvent.click(await pageButton("Start"));
    const box = await dialog(`Start ${title}`);
    await userEvent.type(await within(box).findByRole("textbox", { name: /^Lane ID/ }), "lane-guide-1");
    await userEvent.click(within(box).getByRole("button", { name: "Start" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(2));
    expect(changes(sent)[1]?.body).toEqual({ lane_id: "lane-guide-1" });
  });

  it("lists every worker with its lanes against its cap and its level, the ones the daemon sees first", async () => {
    serving({
      workers: [solo, duo],
      floor: (view) => {
        const [first] = view.workers;
        if (first) {
          view.workers = [
            { ...first, freshness: "last_reported" },
            { ...first, worker: { ...first.worker, id: "worker-b", name: "duo" }, freshness: "observed" },
          ];
        }
        view.lanes = view.lanes.map((lane, at) => ({ ...lane, worker_id: at < 2 ? "worker-b" : "worker-a", actual_state: at < 2 ? "working" : "ended" }));
      },
    });
    openPage(`/work-order?id=${ready}`);
    await userEvent.click(await pageButton("Start"));
    const worker = await within(await dialog(`Start ${title}`)).findByRole("combobox", { name: "Worker" });
    await waitFor(() => expect(within(worker).getAllByRole("option").length).toBeGreaterThan(2));
    const options = within(worker)
      .getAllByRole("option")
      .map((option) => option.textContent);
    expect(options).toEqual(["Choose a worker", "duo · connected · 2 of 2 lanes · attended", "solo · last reported · 0 of 4 lanes · attended"]);
    expect((worker as HTMLSelectElement).value).toBe("");
  });

  it("claims for the worker a member chose when several are registered, and asks for a choice first", async () => {
    const sent = serving({ workers: [solo, duo] });
    openPage(`/work-order?id=${ready}`);
    await userEvent.click(await pageButton("Start"));
    const box = await dialog(`Start ${title}`);
    const start = within(box).getByRole("button", { name: "Start" });
    await waitFor(() => expect(within(box).getAllByRole("option").length).toBe(3));
    expect((start as HTMLButtonElement).disabled).toBe(true);
    expect(box.textContent).toContain("Choose the worker that runs it.");
    await userEvent.selectOptions(within(box).getByRole("combobox", { name: "Worker" }), "worker-b");
    expect((start as HTMLButtonElement).disabled).toBe(false);
    await userEvent.click(start);
    await waitFor(() => expect(changes(sent)).toHaveLength(2));
    expect(changes(sent)[0]?.body).toMatchObject({ worker_id: "worker-b" });
  });

  it("disables Start and says why when no worker has joined", async () => {
    const sent = serving({ workers: [] });
    openPage(`/work-order?id=${ready}`);
    await userEvent.click(await pageButton("Start"));
    const box = await dialog(`Start ${title}`);
    await waitFor(() => expect(box.textContent).toContain("No worker has joined."));
    const start = within(box).getByRole("button", { name: "Start" });
    expect((start as HTMLButtonElement).disabled).toBe(true);
    expect(start.getAttribute("aria-describedby")).toBeTruthy();
    await userEvent.click(start);
    expect(changes(sent)).toHaveLength(0);
  });

  it("names no join token when no worker has joined a solo installation", async () => {
    serving({ workers: [] });
    openPage(`/work-order?id=${ready}`);
    await userEvent.click(await pageButton("Start"));
    const box = await dialog(`Start ${title}`);
    await waitFor(() => expect(box.textContent).toContain("No worker has joined."));
    expect(box.textContent).not.toMatch(/join token/);
  });

  it("names the join token when no worker has joined a group installation", async () => {
    serving({ workers: [], group: true });
    openPage(`/work-order?id=${ready}`);
    await userEvent.click(await pageButton("Start"));
    const box = await dialog(`Start ${title}`);
    await waitFor(() => expect(box.textContent).toContain("with its join token."));
  });

  it("shows the API's words when the claim is refused, and starts no lane", async () => {
    const sent = serving({ post: (call) => (call.path.endsWith("/claims") ? refusal(409, "ineligible", "The fleet is paused.") : undefined) });
    openPage(`/work-order?id=${ready}`);
    await userEvent.click(await pageButton("Start"));
    const box = await dialog(`Start ${title}`);
    await waitFor(() => expect((within(box).getByRole("combobox", { name: "Worker" }) as HTMLSelectElement).value).toBe("worker-a"));
    await userEvent.click(within(box).getByRole("button", { name: "Start" }));
    expect((await within(box).findByRole("alert")).textContent).toBe("The fleet is paused.");
    expect(changes(sent)).toHaveLength(1);
    expect(within(box).getByRole("button", { name: "Start" })).toBeTruthy();
  });

  it("keeps focus in the dialog after a refusal, so Escape still closes it", async () => {
    serving({ post: (call) => (call.path.endsWith("/claims") ? refusal(409, "ineligible", "The fleet is paused.") : undefined) });
    openPage(`/work-order?id=${ready}`);
    await userEvent.click(await pageButton("Start"));
    const box = await dialog(`Start ${title}`);
    await waitFor(() => expect((within(box).getByRole("combobox", { name: "Worker" }) as HTMLSelectElement).value).toBe("worker-a"));
    const start = within(box).getByRole("button", { name: "Start" });
    await userEvent.click(start);
    await within(box).findByRole("alert");
    expect((start as HTMLButtonElement).disabled).toBe(false);
    expect(box.contains(document.activeElement)).toBe(true);
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: `Start ${title}` })).toBeNull());
  });

  it("leaves the work claimed, and offers Start lane, when the lane start is refused", async () => {
    let refuse = true;
    const sent = serving({
      post: (call) => {
        if (call.path.endsWith("/lanes") && refuse) {
          refuse = false;
          return refusal(409, "at_cap", "The worker is at its lane cap.");
        }
        return undefined;
      },
    });
    openPage(`/work-order?id=${ready}`);
    await userEvent.click(await pageButton("Start"));
    const box = await dialog(`Start ${title}`);
    await waitFor(() => expect((within(box).getByRole("combobox", { name: "Worker" }) as HTMLSelectElement).value).toBe("worker-a"));
    await userEvent.type(within(box).getByRole("textbox", { name: /^Lane ID/ }), "lane-guide-1");
    await userEvent.click(within(box).getByRole("button", { name: "Start" }));
    expect((await within(box).findByRole("alert")).textContent).toBe("The worker is at its lane cap.");
    expect(box.textContent).toContain("The work is claimed on solo, and no lane has started.");
    expect(within(box).queryByRole("button", { name: "Start" })).toBeNull();
    await userEvent.click(within(box).getByRole("button", { name: "Start lane" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(3));
    // The claim is not sent again, and the lane is the one the member named.
    expect(changes(sent).map((call) => call.path)).toEqual([`/v1/work-orders/${ready}/claims`, `/v1/work-orders/${ready}/lanes`, `/v1/work-orders/${ready}/lanes`]);
    expect(changes(sent)[2]?.body).toEqual({ lane_id: "lane-guide-1" });
    await waitFor(() => expect(screen.queryByRole("dialog", { name: `Start ${title}` })).toBeNull());
  });

  it("is a native button a keyboard reaches and presses", async () => {
    serving();
    openPage(`/work-order?id=${ready}`);
    const button = await pageButton("Start");
    // A native button is in the tab order, takes focus, and answers Enter and Space with a click.
    expect(button.tagName).toBe("BUTTON");
    expect(button.getAttribute("tabindex")).toBeNull();
    expect((button as HTMLButtonElement).disabled).toBe(false);
    button.focus();
    expect(document.activeElement).toBe(button);
    await userEvent.keyboard("{Enter}");
    expect(await dialog(`Start ${title}`)).toBeTruthy();
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: `Start ${title}` })).toBeNull());
    expect(document.activeElement).toBe(button);
    await userEvent.keyboard(" ");
    expect(await dialog(`Start ${title}`)).toBeTruthy();
  });

  it("opens the dialog when the address asks for it", async () => {
    serving();
    openPage(`/work-order?id=${ready}&dialog=start`);
    expect(await dialog(`Start ${title}`)).toBeTruthy();
  });
});

describe("work that cannot start yet", () => {
  it("shows Start disabled, with the reason, until the work is ready", async () => {
    const sent = serving();
    openPage("/work-order?id=generated-26");
    await screen.findByRole("heading", { level: 1, name: /^generated-26/ });
    const start = await screen.findByRole("button", { name: "Start" });
    expect((start as HTMLButtonElement).disabled).toBe(true);
    const why = document.getElementById(start.getAttribute("aria-describedby") ?? "");
    expect(why?.textContent).toBe("Only ready work can start. This work is in contract review.");
    await userEvent.click(start);
    expect(screen.queryByRole("dialog", { name: /^Start/ })).toBeNull();
    expect(changes(sent)).toHaveLength(0);
  });

  it("offers nothing on work that is running or has ended", async () => {
    serving();
    openPage("/work-order?id=wo-running");
    await screen.findByRole("heading", { level: 1, name: /^wo-running/ });
    expect(screen.queryByRole("button", { name: /^Start/ })).toBeNull();
    cleanup();
    openPage("/work-order?id=wo-done");
    await screen.findByRole("heading", { level: 1, name: /^wo-done/ });
    expect(screen.queryByRole("button", { name: /^Start/ })).toBeNull();
  });
});

describe("a claimed work order with no lane", () => {
  it("offers Start lane, which sends the lane start alone", async () => {
    const sent = serving({ plans: claimed });
    openPage(`/work-order?id=${ready}`);
    expect(screen.queryByRole("button", { name: "Start" })).toBeNull();
    await userEvent.click(await pageButton("Start lane"));
    const box = await dialog(`Start lane: ${title}`);
    expect(box.textContent).toContain("The work is claimed on solo.");
    expect(within(box).queryByRole("combobox")).toBeNull();
    await userEvent.click(within(box).getByRole("button", { name: "Start lane" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.path).toBe(`/v1/work-orders/${ready}/lanes`);
    expect((changes(sent)[0]?.body as { lane_id: string }).lane_id).toMatch(/^[0-9A-Z]{26}$/);
    expect((await screen.findAllByText(`Started the lane of ${title} on solo.`)).length).toBeGreaterThan(0);
  });

  it("offers nothing once a lane was asked for, because the server then allows no start", async () => {
    serving({
      plans: (view) => {
        claimed(view);
        view.work_actions[ready] = { actions: ["release", "cancel"] };
      },
      running: (view) => {
        view.items.push({ work_order: { id: ready, state: "claimed", kind: "change", title }, lane: { id: "lane-guide", work_order: ready, generation: 1, actual_state: "pending", liveness: "alive", freshness: "observed", as_of: 1 }, since: 1 });
      },
    });
    openPage(`/work-order?id=${ready}`);
    await screen.findByRole("heading", { level: 1, name: new RegExp(`^${ready}`) });
    expect(screen.queryByRole("button", { name: /^Start/ })).toBeNull();
  });
});

describe("ready work on the floor", () => {
  it("lists what waits for a lane, and starts one from its row through the same dialog", async () => {
    const sent = serving();
    openPage("/floor");
    const list = await screen.findByRole("region", { name: "Ready to start" });
    const item = within(list).getByText(title).closest("li") as HTMLElement;
    expect(within(item).getByRole("link", { name: title }).getAttribute("href")).toBe(here(`/work-order?id=${ready}`));
    await userEvent.click(within(item).getByRole("button", { name: `Start ${title}` }));
    const box = await dialog(`Start ${title}`);
    await waitFor(() => expect((within(box).getByRole("combobox", { name: "Worker" }) as HTMLSelectElement).value).toBe("worker-a"));
    await userEvent.click(within(box).getByRole("button", { name: "Start" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(2));
    expect(changes(sent).map((call) => call.path)).toEqual([`/v1/work-orders/${ready}/claims`, `/v1/work-orders/${ready}/lanes`]);
    expect(changes(sent)[0]?.body).toMatchObject({ worker_id: "worker-a" });
  });

  it("offers Start lane on claimed work", async () => {
    const sent = serving({ plans: claimed });
    openPage("/floor");
    const list = await screen.findByRole("region", { name: "Ready to start" });
    await userEvent.click(within(list).getByRole("button", { name: `Start lane ${title}` }));
    await userEvent.click(within(await dialog(`Start lane: ${title}`)).getByRole("button", { name: "Start lane" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.path).toBe(`/v1/work-orders/${ready}/lanes`);
  });

  it("lists nothing when no work waits for a lane", async () => {
    serving({
      plans: (view) => {
        const order = orderOf(view, ready);
        if (order) {
          order.state = "running";
          view.work_actions[ready] = { actions: ["cancel"] };
        }
        for (const plan of view.plans) {
          plan.reviews = [];
        }
      },
    });
    openPage("/floor");
    await screen.findByRole("heading", { level: 1, name: "Floor" });
    await screen.findAllByText("solo");
    expect(screen.queryByRole("region", { name: "Ready to start" })).toBeNull();
  });
});

describe("the command palette", () => {
  const palette = () => screen.findByRole("dialog", { name: "Command palette" });

  it("lists Start work for a ready work order in view, which opens the dialog", async () => {
    serving();
    openPage(`/work-order?id=${ready}`);
    await screen.findByRole("heading", { level: 1, name: new RegExp(`^${ready}`) });
    await userEvent.keyboard("{Control>}k{/Control}");
    await userEvent.click(await within(await palette()).findByRole("option", { name: new RegExp(`^Start work: ${title}`) }));
    expect(await dialog(`Start ${title}`)).toBeTruthy();
  });

  it("lists Start lane for a claimed work order in view, and no start for running work", async () => {
    serving({ plans: claimed });
    openPage(`/work-order?id=${ready}`);
    await screen.findByRole("heading", { level: 1, name: new RegExp(`^${ready}`) });
    await userEvent.keyboard("{Control>}k{/Control}");
    const options = within(await palette()).getAllByRole("option");
    expect(options.some((option) => option.textContent?.startsWith("Start work"))).toBe(false);
    await userEvent.click(await within(await palette()).findByRole("option", { name: new RegExp(`^Start lane: ${title}`) }));
    expect(await dialog(`Start lane: ${title}`)).toBeTruthy();
    cleanup();
    serving();
    openPage("/work-order?id=wo-running");
    await screen.findByRole("heading", { level: 1, name: /^wo-running/ });
    await userEvent.keyboard("{Control>}k{/Control}");
    expect(within(await palette()).queryByRole("option", { name: /^Start (work|lane)/ })).toBeNull();
  });

  it("lists Start work for a ready work order the search finds, and opens its page on the dialog", async () => {
    serving({
      search: [
        { kind: "work_order", id: ready, title, state: "ready", actions: ["claim", "cancel"] },
        { kind: "work_order", id: "wo-running", title: "Write the docs", state: "running", actions: ["cancel"] },
      ],
    });
    openPage("/needs-you");
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    await userEvent.keyboard("{Control>}k{/Control}");
    await userEvent.type(within(await palette()).getByRole("combobox"), "write");
    const options = await waitFor(() => {
      const found = within(screen.getByRole("dialog", { name: "Command palette" })).getAllByRole("option");
      if (!found.some((option) => option.textContent?.startsWith("Start work"))) {
        throw new Error("no Start work entry");
      }
      return found;
    });
    expect(options.filter((option) => option.textContent?.startsWith("Start work"))).toHaveLength(1);
    await userEvent.click(options.find((option) => option.textContent?.startsWith("Start work")) as HTMLElement);
    expect(await dialog(`Start ${title}`)).toBeTruthy();
    expect(`${window.location.pathname}${window.location.search}`).toContain(`/work-order?id=${ready}`);
  });
});

// A review of a finished lane is ready work that no plan lists, so the plans view lists it apart, with the
// work it judges. A member starts it as any ready work: the same claim, then the same lane start.
describe("a ready review", () => {
  const review = "review-of-guide";
  const reviewTitle = `Review: ${title}`;
  const withReview = (view: Plans) => {
    view.reviews = [{ id: review, kind: "review", state: "ready", title: reviewTitle, repository_id: "repository-1", reviews: ready }];
    view.work_actions[review] = { actions: ["claim", "cancel"] };
  };

  it("is listed on the floor as a review of the work it judges, and starts through the same dialog", async () => {
    const sent = serving({ plans: withReview });
    openPage("/floor");
    const list = await screen.findByRole("region", { name: "Ready to start" });
    const item = within(list).getByText(reviewTitle).closest("li") as HTMLElement;
    expect(within(item).getByText("Review of")).toBeTruthy();
    expect(within(item).getByRole("link", { name: ready }).getAttribute("href")).toBe(here(`/work-order?id=${ready}`));
    await userEvent.click(within(item).getByRole("button", { name: `Start ${reviewTitle}` }));
    const box = await dialog(`Start ${reviewTitle}`);
    await waitFor(() => expect((within(box).getByRole("combobox", { name: "Worker" }) as HTMLSelectElement).value).toBe("worker-a"));
    await userEvent.click(within(box).getByRole("button", { name: "Start" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(2));
    expect(changes(sent).map((call) => call.path)).toEqual([`/v1/work-orders/${review}/claims`, `/v1/work-orders/${review}/lanes`]);
    expect(changes(sent)[0]?.body).toMatchObject({ worker_id: "worker-a" });
  });

  it("is a row of the work orders list, labelled with the work it judges", async () => {
    serving({ plans: withReview });
    openPage("/work");
    const row = (await screen.findByRole("link", { name: reviewTitle })).closest("tr") as HTMLElement;
    expect(within(row).getByText("Review of")).toBeTruthy();
    expect(within(row).getByRole("link", { name: ready }).getAttribute("href")).toBe(here(`/work-order?id=${ready}`));
  });

  it("starts from its own page, and the command palette lists the start", async () => {
    const sent = serving({ plans: withReview });
    openPage(`/work-order?id=${review}`);
    await screen.findByRole("heading", { level: 1, name: new RegExp(`^${review}`) });
    await userEvent.keyboard("{Control>}k{/Control}");
    await userEvent.click(await within(await screen.findByRole("dialog", { name: "Command palette" })).findByRole("option", { name: new RegExp(`^Start work: ${reviewTitle}`) }));
    const box = await dialog(`Start ${reviewTitle}`);
    await waitFor(() => expect((within(box).getByRole("combobox", { name: "Worker" }) as HTMLSelectElement).value).toBe("worker-a"));
    await userEvent.click(within(box).getByRole("button", { name: "Start" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(2));
    expect(changes(sent)[0]?.path).toBe(`/v1/work-orders/${review}/claims`);
  });
});

// A contract review stands in its plan's reviews, not among the plan's work, and a member starts it as any
// ready work: the same claim, then the same lane start.
describe("a ready contract review", () => {
  const review = "review-1";
  const reviewTitle = "Review Write the docs";

  it("is listed on the floor, and starts through the same dialog", async () => {
    const sent = serving();
    openPage("/floor");
    const list = await screen.findByRole("region", { name: "Ready to start" });
    const item = within(list).getByText(reviewTitle).closest("li") as HTMLElement;
    await userEvent.click(within(item).getByRole("button", { name: `Start ${reviewTitle}` }));
    const box = await dialog(`Start ${reviewTitle}`);
    await waitFor(() => expect((within(box).getByRole("combobox", { name: "Worker" }) as HTMLSelectElement).value).toBe("worker-a"));
    await userEvent.click(within(box).getByRole("button", { name: "Start" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(2));
    expect(changes(sent).map((call) => call.path)).toEqual([`/v1/work-orders/${review}/claims`, `/v1/work-orders/${review}/lanes`]);
  });

  it("is a row of the work orders list", async () => {
    serving();
    openPage("/work");
    expect((await screen.findByRole("link", { name: reviewTitle })).closest("tr")).toBeTruthy();
  });

  it("starts from its own page", async () => {
    serving();
    openPage(`/work-order?id=${review}`);
    expect(await screen.findByRole("heading", { level: 1, name: new RegExp(`^${review}`) })).toBeTruthy();
    expect(await screen.findByRole("button", { name: "Start" })).toBeTruthy();
  });
});
