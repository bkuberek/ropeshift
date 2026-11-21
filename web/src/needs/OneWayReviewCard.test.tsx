import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { NeedItem, NeedsYou as View } from "../api/types";
import { CurrentProject } from "../app/route";
import { needsYouItems } from "../browserApp";
import { here, serveApi, testProject } from "../testing";
import { cardKindOf, cardKinds, contextOf, filterOf, headingOf, keyOf, kindOf } from "./kinds";
import { NeedsYou } from "./NeedsYou";

type ReviewItem = Extract<NeedItem, { kind: "one_way_review" }>;

// The one-way decision the checks' Needs you holds: one an unattended orchestrator made, which waits for review.
const review = (): ReviewItem => {
  const found = needsYouItems().find((item): item is ReviewItem => item.kind === "one_way_review");
  if (!found) {
    throw new Error("the fixture holds no one-way decision to review");
  }
  return structuredClone(found);
};

function show(item: ReviewItem, after: () => Promise<void> = async () => {}) {
  const view: View = { as_of: 1790000700000, revision: 1, items: [item] };
  window.history.replaceState(null, "", `${here("/")}?item=${encodeURIComponent(keyOf(item))}`);
  return render(
    <CurrentProject.Provider value={testProject}>
      <NeedsYou view={view} readAt={Date.now()} after={after} />
    </CurrentProject.Provider>,
  );
}

const theCard = () => screen.getByRole("article", { name: "Keep one repository per service." });

describe("a one-way decision an unattended orchestrator made, in Needs you", () => {
  it("is a card kind of its own, under Decisions, titled by the decision", () => {
    const item = review();
    expect(cardKinds).toContain("one-way-review");
    expect(cardKindOf(item)).toBe("one-way-review");
    expect(filterOf(item)).toBe("decisions");
    expect(kindOf(item)).toEqual({ icon: "decision", name: "One-way decision to review" });
    expect(headingOf(item)).toBe("Keep one repository per service.");
    expect(contextOf(item)).toBe("Decided by the orchestrator on worker-a · d-repositories");
  });

  it("shows what was decided and why, says no work waits on it, and puts Keep first among its actions", () => {
    serveApi(() => ({ status: 200, body: {} }));
    show(review());
    const card = theCard();
    expect(card.textContent).toContain("How many repositories does the product have?");
    expect(card.textContent).toContain("The plan fixes one repository per service, and the lane recommends it.");
    expect(card.textContent).toContain("No work waits on your review.");
    expect(within(card).getByRole("button", { name: "Keep" }).className).toContain("btn-primary");
    expect(within(card).getByRole("button", { name: "Overturn d-repositories" })).toBeTruthy();
  });

  it("keeps the decision by recording the same decision as the member's in its place", async () => {
    const sent = serveApi((call) => (call.method === "POST" ? { status: 200, body: { ...review().one_way_review.decision, id: "kept-d-repositories" } } : { status: 200, body: {} }));
    const later = vi.fn(async () => {});
    show(review(), later);
    await userEvent.click(within(theCard()).getByRole("button", { name: "Keep" }));
    await waitFor(() => expect(later).toHaveBeenCalled());
    const posted = sent.filter((call) => call.method === "POST");
    expect(posted.map((call) => call.path)).toEqual(["/v1/decisions"]);
    expect(posted[0]?.body).toEqual({
      id: "kept-d-repositories",
      status: "decided",
      statement: "Keep one repository per service.",
      rationale: "The plan fixes one repository per service, and the lane recommends it.",
      citations: ["wo:wo-running", "requirement:FAC-2"],
      scope: "wo:wo-running",
      reversibility: "one_way",
      categories: ["architecture"],
      supersedes: "d-repositories",
    });
  });

  it("keeps a decision of another project in that project", async () => {
    const sent = serveApi((call) => (call.method === "POST" ? { status: 200, body: { ...review().one_way_review.decision, id: "kept-d-repositories" } } : { status: 200, body: {} }));
    const later = vi.fn(async () => {});
    show({ ...review(), project: { id: "p-harbor", name: "harbor" } }, later);
    await userEvent.click(within(theCard()).getByRole("button", { name: "Keep" }));
    await waitFor(() => expect(later).toHaveBeenCalled());
    const posted = sent.filter((call) => call.method === "POST");
    expect(posted.map((call) => [call.project, call.path])).toEqual([["harbor", "/v1/decisions"]]);
  });

  it("overturns the decision from the dialog Decisions uses", async () => {
    const decision = review().one_way_review.decision;
    const sent = serveApi((call) => (call.method === "POST" ? { status: 200, body: { old: { ...decision, status: "overturned" }, new: { ...decision, id: "d-new", statement: "One repository." } } } : { status: 200, body: {} }));
    show(review());
    await userEvent.click(within(theCard()).getByRole("button", { name: "Overturn d-repositories" }));
    const dialog = await screen.findByRole("dialog", { name: "Overturn d-repositories" });
    await userEvent.type(within(dialog).getByLabelText(/New statement/), "One repository.");
    await userEvent.click(within(dialog).getByRole("button", { name: "Overturn decision" }));
    await waitFor(() => expect(sent.filter((call) => call.method === "POST").map((call) => call.path)).toEqual(["/v1/decisions/d-repositories/overturn"]));
  });
});
