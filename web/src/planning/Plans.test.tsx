import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { PlanDetail, Plans } from "../api/types";
import { openPage } from "../opening";
import { golden, serveViews } from "../testing";

function planned(): PlanDetail {
  const plan = golden<Plans>("plans").plans.find((row) => row.goal?.title === "Ship solo mode");
  if (!plan) {
    throw new Error("no plan stands under Ship solo mode");
  }
  return plan;
}

function serve(change: (view: Plans) => void) {
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

describe("the plan list", () => {
  it("lists each plan by title and links to it", async () => {
    serveViews();
    openPage("/plans");
    const plan = planned();
    const link = await screen.findByRole("link", { name: plan.plan.title });
    expect(link.getAttribute("href")).toBe(`/plans?plan=${encodeURIComponent(plan.plan.id)}`);
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

  it("says so when there are no plans", async () => {
    serve((view) => {
      view.plans = [];
      view.unplanned = [];
    });
    openPage("/plans");
    expect(await screen.findByText("No plans yet.")).toBeTruthy();
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
    expect(within(section).getByRole("link", { name: "FR-RETRY-1" }).getAttribute("href")).toBe("/requirements?key=FR-RETRY-1");
    expect(within(section).getByRole("link", { name: "decision-2" }).getAttribute("href")).toBe("/decisions?id=decision-2");
  });

  it("shows a review's result: where it stands, what it read, and what it left open", async () => {
    serveViews();
    await openPlan();
    const review = screen.getByRole("article", { name: "Review Write the docs" });
    expect(within(review).getByText("Ready")).toBeTruthy();
    expect(within(review).getByText(/@3$/)).toBeTruthy();
    expect(within(review).getByRole("link", { name: "Does the plan cover a restart?" }).getAttribute("href")).toBe("/questions?id=question-review");
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
    const row = within(work).getByText("wo-running").closest(".row");
    expect(row?.textContent).toContain("Running");
  });

  it("names its goal, and leads back to the list", async () => {
    serveViews();
    await openPlan();
    expect(screen.getByText("Ship solo mode")).toBeTruthy();
    expect(screen.getByRole("link", { name: "All plans" }).getAttribute("href")).toBe("/plans");
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
    expect(within(second as HTMLElement).getByRole("link", { name: "FAC-3" }).getAttribute("href")).toBe("/requirements?key=FAC-3");
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
    const row = within(work).getByText("Build the planning core").closest(".row");
    expect(row?.textContent).toContain("Size medium");
    expect(row?.textContent).toContain("In milestone Build the core");
  });

  it("says which version waits for approval, and leads to Needs you", async () => {
    await openFactory();
    const waiting = screen.getByText(/^Version 6 waits for approval/);
    expect(waiting.textContent).toBe("Version 6 waits for approval in Needs you.");
    expect(within(waiting).getByRole("link", { name: "Needs you" }).getAttribute("href")).toBe("/");
  });
});
