import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { PlanDetail, Plans, PlanView } from "../api/types";
import { openPage } from "../opening";
import { golden, here, serveViews } from "../testing";

function planned(): PlanDetail {
  const plan = golden<Plans>("plans").plans.find((row) => row.goal?.title === "Ship solo mode");
  if (!plan) {
    throw new Error("no plan stands under Ship solo mode");
  }
  return plan;
}

function serve(change: (view: Plans) => void = () => {}) {
  const view = golden<Plans>("plans");
  change(view);
  serveViews((call) => (call.path.startsWith("/v1/views/plans") ? { status: 200, body: view } : undefined));
}

// The factory floor has milestones, a shared contract and one that uses it, sizes, and a version that
// waits for approval.
const openFactory = async () => {
  serveViews();
  const plan = golden<Plans>("plans").plans.find((row) => row.plan.title === "The factory floor");
  openPage(`/plans?plan=${encodeURIComponent(plan?.plan.id ?? "")}`);
  await screen.findByRole("heading", { level: 1, name: "The factory floor" });
};

// factsOf reads each fact of a list as its name and its value together.
const factsOf = (within_: HTMLElement) =>
  within(within_)
    .getAllByRole("term")
    .map((term) => term.closest("div")?.textContent);

const openPlan = async () => {
  const plan = planned();
  openPage(`/plans?plan=${encodeURIComponent(plan.plan.id)}`);
  await screen.findByRole("heading", { level: 1, name: plan.plan.title });
};

describe("what a plan page says of the plan", () => {
  it("shows the goal's statement and the text of the version in force", async () => {
    const sent: string[] = [];
    serveViews((call) => {
      sent.push(call.path);
      return undefined;
    });
    const plan = golden<Plans>("plans").plans.find((row) => row.plan.title === "The factory floor");
    openPage(`/plans?plan=${encodeURIComponent(plan?.plan.id ?? "")}`);
    await screen.findByRole("heading", { level: 1, name: "The factory floor" });
    const goal = await screen.findByRole("region", { name: "Goal" });
    expect(within(goal).getByText("One person plans, and the factory builds.")).toBeTruthy();
    const text = await screen.findByRole("region", { name: "Plan text" });
    await within(text).findByText("How the factory floor is built.");
    expect(sent.some((path) => path === `/v1/plans/${encodeURIComponent(plan?.plan.id ?? "")}?version=5`)).toBe(true);
  });

  it("shows a text a repository wrote as text", async () => {
    const hostile = "<script>window.hit = 1</script>\n\n[open it](javascript:window.hit=2)\n\n<img src=x onerror=\"window.hit=3\">\n";
    serveViews((call) => {
      if (call.method === "GET" && /^\/v1\/plans\/[^/?]+(\?|$)/.test(call.path)) {
        const view = golden<PlanView>("plan");
        view.body = hostile;
        return { status: 200, body: view };
      }
      return undefined;
    });
    const plan = golden<Plans>("plans").plans.find((row) => row.plan.title === "The factory floor");
    openPage(`/plans?plan=${encodeURIComponent(plan?.plan.id ?? "")}`);
    const text = await screen.findByRole("region", { name: "Plan text" });
    await within(text).findByText(/window\.hit = 1/);
    expect(text.querySelector("script, img, iframe, object, embed, [onerror]")).toBeNull();
    expect(Array.from(text.querySelectorAll("a")).some((link) => /^\s*javascript:/i.test(link.getAttribute("href") ?? ""))).toBe(false);
    expect((window as unknown as { hit?: number }).hit).toBeUndefined();
  });

  it("says when the version recorded no text", async () => {
    serveViews((call) => {
      if (call.method === "GET" && /^\/v1\/plans\/[^/?]+(\?|$)/.test(call.path)) {
        const view = golden<PlanView>("plan");
        view.body = undefined;
        return { status: 200, body: view };
      }
      return undefined;
    });
    const plan = golden<Plans>("plans").plans.find((row) => row.plan.title === "The factory floor");
    openPage(`/plans?plan=${encodeURIComponent(plan?.plan.id ?? "")}`);
    const text = await screen.findByRole("region", { name: "Plan text" });
    await within(text).findByText("This version recorded no plan text.");
  });
});

describe("the plan list", () => {
  it("lists each plan by title and links to it", async () => {
    serveViews();
    openPage("/plans");
    const plan = planned();
    const link = await screen.findByRole("link", { name: plan.plan.title });
    expect(link.getAttribute("href")).toBe(here(`/plans?plan=${encodeURIComponent(plan.plan.id)}`));
  });

  it("states what each plan holds", async () => {
    serveViews();
    openPage("/plans");
    const row = (await screen.findByRole("link", { name: "Write the docs" })).closest("li");
    expect(row?.textContent).toContain("1 contract");
    expect(row?.textContent).toContain("1 review");
    expect(row?.textContent).toContain("Running 1");
  });

  it("keeps work no plan holds where a person can find it", async () => {
    serveViews();
    openPage("/plans");
    const loose = await screen.findByRole("region", { name: "Work with no plan" });
    const row = within(loose).getByText("Not planned yet").closest("li");
    expect(row?.textContent).toContain("Intake");
    expect(row?.textContent).toContain("wo-loose");
  });

  it("lists the reviews that have not ended, each linked to the work it judges", async () => {
    serve((view) => {
      view.reviews = [{ id: "wo-review", kind: "review", state: "ready", title: "Review: Trim the log", repository_id: "repository-1", reviews: "wo-judged" }];
    });
    openPage("/plans");
    const open = await screen.findByRole("region", { name: "Reviews that have not ended" });
    const row = within(open).getByText("Review: Trim the log").closest("li") as HTMLElement;
    expect(row.textContent).toContain("Ready");
    expect(row.textContent).toContain("wo-review");
    expect(within(row).getByText("Review of")).toBeTruthy();
    expect(within(row).getByRole("link", { name: "wo-judged" }).getAttribute("href")).toBe(here("/work-order?id=wo-judged"));
  });

  it("shows no section of reviews when none is open", async () => {
    serveViews();
    openPage("/plans");
    await screen.findByRole("link", { name: "Write the docs" });
    expect(screen.queryByRole("region", { name: "Reviews that have not ended" })).toBeNull();
  });

  it("is not empty while a review is open", async () => {
    serve((view) => {
      view.plans = [];
      view.unplanned = [];
      view.reviews = [{ id: "wo-review", kind: "review", state: "ready", title: "Review: Trim the log", repository_id: "repository-1", reviews: "wo-judged" }];
    });
    openPage("/plans");
    expect(await screen.findByText("Review: Trim the log")).toBeTruthy();
    expect(screen.queryByText("No plans yet")).toBeNull();
  });

  it("says so when there are no plans", async () => {
    serve((view) => {
      view.plans = [];
      view.unplanned = [];
    });
    openPage("/plans");
    expect(await screen.findByText("No plans yet")).toBeTruthy();
    expect(screen.getAllByRole("button", { name: "Start planning session" }).length).toBeGreaterThan(0);
  });

  it("states each plan's version in force and progress, and offers to review a version that waits", async () => {
    serve((view) => {
      for (const row of view.plans) {
        if (row.plan.title === "The factory floor") {
          row.plan.id = "factory-plan";
        }
      }
    });
    openPage("/plans");
    const row = (await screen.findByRole("link", { name: "The factory floor" })).closest("li") as HTMLElement;
    expect(row.textContent).toContain("Version 5 approved · version 6 waits");
    expect(within(row).getByRole("progressbar", { name: "The factory floor: 1 of 5 done" })).toBeTruthy();
    expect(within(row).getByRole("link", { name: "Review version 6" }).getAttribute("href")).toBe(here("/plan?id=factory-plan&version=6"));
    const quiet = (await screen.findByRole("link", { name: "Write the docs" })).closest("li") as HTMLElement;
    expect(within(quiet).queryByRole("link", { name: /Review version/ })).toBeNull();
    expect(screen.getAllByRole("button", { name: "Start planning session" }).length).toBeGreaterThan(0);
  });
});

describe("one plan", () => {
  it("lists a contract's versions, the newest first and the rest folded", async () => {
    serveViews();
    await openPlan();
    const contract = screen.getByRole("article", { name: "Write the docs" });
    const versions = within(contract).getAllByRole("heading", { name: /^Version / });
    expect(versions.map((heading) => heading.textContent)).toEqual(["Version 3", "Version 2", "Version 1"]);
    expect(versions[0]?.closest("details")).toBeNull();
    expect(versions[1]?.closest("details")).not.toBeNull();
    expect(within(contract).getByText("2 earlier versions")).toBeTruthy();
  });

  it("keeps each version's own words, so a reader sees what changed", async () => {
    serveViews();
    await openPlan();
    expect(screen.getByText("Write the docs.")).toBeTruthy();
    expect(screen.getByText("Write the docs. Now with a budget.")).toBeTruthy();
  });

  it("states a version's criteria, digest, and source", async () => {
    serveViews();
    await openPlan();
    const newest = planned().contracts[0]?.versions.at(-1);
    const section = screen.getByRole("region", { name: "Version 3" });
    expect(within(section).getByText("It works")).toBeTruthy();
    expect(within(section).getByText(newest?.digest ?? "")).toBeTruthy();
    expect(within(section).getByText("plans/docs.md@abc123")).toBeTruthy();
  });

  it("links a contract to the requirements and decisions it names", async () => {
    serveViews();
    await openPlan();
    const section = screen.getByRole("region", { name: "Version 3" });
    expect(within(section).getByRole("link", { name: "FR-RETRY-1" }).getAttribute("href")).toBe(here("/documents?kind=requirements&key=FR-RETRY-1"));
    expect(within(section).getByRole("link", { name: "decision-2" }).getAttribute("href")).toBe(here("/decisions?id=decision-2"));
  });

  it("shows a review's result: where it stands, what it read, and what it left open", async () => {
    serveViews();
    await openPlan();
    const review = screen.getByRole("article", { name: "Review Write the docs" });
    expect(within(review).getByText("Ready")).toBeTruthy();
    expect(within(review).getByText(/@3$/)).toBeTruthy();
    expect(within(review).getByRole("link", { name: "Does the plan cover a restart?" }).getAttribute("href")).toBe(here("/questions?id=question-review"));
  });

  it("names what each question a review left open binds, with the title of the record its scope names", async () => {
    const left = planned().reviews[0]?.open_questions[0];
    expect(left?.scope).toMatch(/^plan:/);
    expect(left?.scope_title).toBeTruthy();
    serveViews();
    await openPlan();
    const review = screen.getByRole("article", { name: "Review Write the docs" });
    const item = within(review).getByRole("link", { name: "Does the plan cover a restart?" }).closest("li");
    expect(item?.querySelector("p")?.textContent).toBe(`Scope: ${left?.scope} · ${left?.scope_title}`);
  });

  it("says when a review left nothing open", async () => {
    const plan = planned();
    serve((view) => {
      const row = view.plans.find((candidate) => candidate.plan.id === plan.plan.id);
      if (row?.reviews[0]) {
        row.reviews[0].open_questions = [];
      }
    });
    await openPlan();
    expect(screen.getByText("It left nothing open.")).toBeTruthy();
  });

  it("says when a plan has had no review", async () => {
    const plan = planned();
    serve((view) => {
      const row = view.plans.find((candidate) => candidate.plan.id === plan.plan.id);
      if (row) {
        row.reviews = [];
      }
    });
    await openPlan();
    expect(screen.getByText("No review has run.")).toBeTruthy();
  });

  it("lists the plan's work with the state of each", async () => {
    serveViews();
    await openPlan();
    const work = screen.getByRole("region", { name: "Work" });
    const row = within(work).getByText("wo-running").closest(".list-row");
    expect(row?.textContent).toContain("Running");
  });

  it("names its goal, and leads back to the list", async () => {
    serveViews();
    await openPlan();
    const goal = await screen.findByRole("region", { name: "Goal" });
    expect(within(goal).getByText("Ship solo mode")).toBeTruthy();
    expect(within(goal).getByText("One machine runs the whole product.")).toBeTruthy();
    expect(screen.getByRole("link", { name: "All plans" }).getAttribute("href")).toBe(here("/plans"));
  });

  it("says so when no version of the plan waits for approval", async () => {
    serveViews();
    await openPlan();
    expect(screen.queryByText(/waits for approval/)).toBeNull();
  });

  it("says so when no plan has that ID", async () => {
    serveViews();
    openPage("/plans?plan=no-such-plan");
    expect(await screen.findByRole("heading", { level: 1, name: "No such plan" })).toBeTruthy();
    expect(screen.getByRole("link", { name: "All plans" })).toBeTruthy();
  });
});

describe("a plan with milestones", () => {
  it("names the milestone a contract is in, and the shared contracts it uses", async () => {
    await openFactory();
    const core = screen.getByRole("article", { name: "The planning core" });
    const facts = factsOf(within(core).getByRole("region", { name: "Version 2" }));
    expect(facts).toContain("MilestoneBuild the core");
    expect(facts).toContain("UsesThe API client");
    const client = screen.getByRole("article", { name: "The API client" });
    expect(factsOf(within(client).getByRole("region", { name: "Version 1" }))).toContain("SharedOther contracts of this plan use it");
  });

  it("says which requirements each criterion serves", async () => {
    await openFactory();
    const page = screen.getByRole("article", { name: "The approval page" });
    const criteria = within(within(page).getByRole("region", { name: "Version 2" })).getByRole("list", { name: "Acceptance criteria" });
    const second = within(criteria).getByText(/The roadmap shows parallel work/).closest("li");
    expect(within(second as HTMLElement).getByRole("link", { name: "FAC-3" }).getAttribute("href")).toBe(here("/documents?kind=requirements&key=FAC-3"));
    expect(within(second as HTMLElement).queryByRole("link", { name: "FAC-1" })).toBeNull();
  });

  it("lists a contract's work orders with the size of each", async () => {
    await openFactory();
    const page = screen.getByRole("article", { name: "The approval page" });
    const work = within(page).getByRole("list", { name: "Work orders of The approval page" });
    const rows = within(work)
      .getAllByRole("listitem")
      .filter((item) => item.parentElement === work)
      .map((item) => item.textContent);
    expect(rows).toEqual(["Build the approval pageSize small", "Show the critical pathSize small"]);
  });

  it("gives each work order of the plan its size and milestone", async () => {
    await openFactory();
    const work = screen.getByRole("region", { name: "Work" });
    const row = within(work).getByText("Build the planning core").closest(".list-row");
    expect(row?.textContent).toContain("Size medium");
    expect(row?.textContent).toContain("In milestone Build the core");
  });

  it("says which version waits for approval, and leads to its approval page and to Needs you", async () => {
    await openFactory();
    const banner = screen.getByText("Version 6 waits for your approval.").closest(".banner") as HTMLElement;
    expect(within(banner).getByRole("link", { name: "Needs you" }).getAttribute("href")).toBe("/needs-you?kind=decisions");
    expect(within(banner).getByRole("link", { name: "Review version 6" }).getAttribute("href")).toMatch(/\/plan\?id=.+&version=6$/);
  });

  it("lists the plan's versions with where each request stands", async () => {
    await openFactory();
    const versions = await screen.findByRole("list", { name: "Versions of the plan" });
    const rows = within(versions).getAllByRole("listitem");
    expect(rows).toHaveLength(6);
    expect(rows[0]?.textContent).toContain("Version 6");
    expect(rows[0]?.textContent).toContain("Open");
    expect(rows[1]?.textContent).toContain("Approved");
    expect(within(rows[1] as HTMLElement).getByRole("link", { name: "Version 5" }).getAttribute("href")).toMatch(/\/plan\?id=.+&version=5$/);
  });

  it("shows how the newest version differs from the approved one, and the drafts it carries", async () => {
    await openFactory();
    const diff = await screen.findByRole("region", { name: "Version 6: difference from version 5" });
    expect(within(diff).getByText("Added")).toBeTruthy();
    expect(within(diff).getByRole("list", { name: "Inside contract:core" }).textContent).toContain("Criteria added: c2");
    const drafts = screen.getByRole("list", { name: "Draft decisions" });
    expect(within(drafts).getAllByRole("listitem")).toHaveLength(2);
  });
});
