import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { NeedItem, NeedsYou as View } from "../api/types";
import { CurrentProject } from "../app/route";
import { golden, here, testProject } from "../testing";
import { headingOf, keyOf } from "./kinds";
import { NeedsYou } from "./NeedsYou";

type PlanItem = Extract<NeedItem, { kind: "plan_approval" }>;

function show(view: View, item: NeedItem) {
  window.history.replaceState(null, "", `${here("/")}?item=${encodeURIComponent(keyOf(item))}`);
  return render(
    <CurrentProject.Provider value={testProject}>
      <NeedsYou view={view} readAt={Date.now()} after={async () => {}} />
    </CurrentProject.Provider>,
  );
}

const planItems = (view: View): PlanItem[] => view.items.filter((item): item is PlanItem => item.kind === "plan_approval");

describe("plan cards that share a version", () => {
  it("name the branch each one is about, in the heading and on the card", () => {
    const view = golden<View>("needs-you-shared-version");
    const [first, second] = planItems(view);
    if (!first || !second) {
      throw new Error("the golden view holds fewer than two plan approvals");
    }
    expect(headingOf(first)).toBe("Approve Retry budget, version 1, from plan/retry-a?");
    expect(headingOf(second)).toBe("Approve Retry budget, version 1, from plan/retry-b?");
    show(view, second);
    const card = screen.getByRole("article", { name: "Approve Retry budget, version 1, from plan/retry-b?" });
    const branch = within(card).getByRole("group", { name: "Branch" });
    expect(branch.textContent).toContain("plan/retry-b");
    expect(branch.textContent).toContain("wo-b");
    expect(branch.textContent).toContain("def4560");
    expect(within(card).getByText(/Another request asks about this version for another branch/)).toBeTruthy();
  });

  it("name no branch when no other request shares the version", () => {
    const view = golden<View>("needs-you");
    const [item] = planItems(view);
    if (!item) {
      throw new Error("no plan approval in the golden view");
    }
    expect(headingOf(item)).toMatch(/^Approve [^,]+, version \d+\?$/);
  });
});

describe("a plan request held back as a placeholder", () => {
  it("points to the branch that finishes it while that branch waits for acceptance beside it", () => {
    const view = golden<View>("needs-you-held-plan-finishing");
    const [held] = planItems(view);
    if (!held) {
      throw new Error("no plan approval in the golden view");
    }
    expect(view.items.some((item) => item.kind === "work" && item.id === "wo-plan")).toBe(true);
    show(view, held);
    const card = screen.getByRole("article", { name: /^Approve Harbor berth booking/ });
    const pointer = within(card).getByRole("group", { name: "Finished by" });
    expect(pointer.textContent).toContain("plan/harbor");
    expect(pointer.textContent).toContain("wo-plan");
    expect(pointer.textContent).toContain("abc1230");
    expect(within(pointer).getByRole("link", { name: "Open the acceptance of wo-plan" }).getAttribute("href")).toContain("item=work%3Awo-plan");
    expect(within(card).queryByRole("button", { name: "Approve this plan" })).toBeNull();
  });

  it("points to no branch when none finishes it", () => {
    const view = golden<View>("needs-you-held-plan-finishing");
    const [held] = planItems(view);
    if (!held) {
      throw new Error("no plan approval in the golden view");
    }
    delete held.plan_approval.finished_by;
    show(view, held);
    const card = screen.getByRole("article", { name: /^Approve Harbor berth booking/ });
    expect(within(card).queryByRole("group", { name: "Finished by" })).toBeNull();
  });
});
