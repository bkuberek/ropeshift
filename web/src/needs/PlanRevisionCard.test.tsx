import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { NeedItem, NeedsYou as View, PlanRevisionView } from "../api/types";
import { CurrentProject } from "../app/route";
import { golden, here, refusal, serveApi, testProject } from "../testing";
import { kindOf, headingOf, contextOf, cardKindOf, filterOf } from "./kinds";
import { NeedsYou } from "./NeedsYou";

type RevisionItem = Extract<NeedItem, { kind: "plan_revision" }>;

const revision = (): PlanRevisionView => ({
  plan: "plan-1",
  title: "Retry budget",
  revision: { id: "r1", plan_id: "plan-1", approved_version: 2, decision_ids: ["d-logo", "d-ids"], finished: [{ work_order: "wo-built", decisions: ["d-logo"] }], opened_at: 1_790_000_650_000 },
  decisions: [
    { id: "d-logo", statement: "Use the wordmark.", scope: "plan:plan-1", reversibility: "reversible", status: "decided", decided_by: "member-1" },
    { id: "d-ids", statement: "Number work orders with ULIDs.", scope: "plan:plan-1", reversibility: "one_way", status: "decided", decided_by: "member-1" },
  ],
  finished: [{ work_order: "wo-built", title: "Add the budget", state: "done", decisions: ["d-logo"] }],
  waiting: [{ work_order: "wo-waiting", title: "Document the budget" }],
});

const itemOf = (need: PlanRevisionView): RevisionItem => ({ id: need.plan, kind: "plan_revision", since: 1_790_000_650_000, plan_revision: need });

function show(item: RevisionItem, after: () => Promise<void> = async () => {}) {
  const view: View = { ...golden<View>("needs-you"), items: [item] };
  window.history.replaceState(null, "", `${here("/")}?item=${encodeURIComponent(`plan_revision:${item.id}`)}`);
  return render(
    <CurrentProject.Provider value={testProject}>
      <NeedsYou view={view} readAt={Date.now()} after={after} />
    </CurrentProject.Provider>,
  );
}

const theCard = () => screen.getByRole("article", { name: "Retry budget needs revision" });

describe("the kind of a plan revision", () => {
  it("is a decision, titled by the plan, and counted by its decisions and the work they reached", () => {
    const item = itemOf(revision());
    expect(kindOf(item)).toEqual({ icon: "plan", name: "Plan revision" });
    expect(headingOf(item)).toBe("Retry budget needs revision");
    expect(contextOf(item)).toBe("2 decisions since version 2 · 1 finished work order reached");
    expect(cardKindOf(item)).toBe("plan-revision");
    expect(filterOf(item)).toBe("decisions");
  });

  it("leaves out the work it reached when it reached none", () => {
    const need = revision();
    need.finished = [];
    expect(contextOf(itemOf(need))).toBe("2 decisions since version 2");
  });
});

describe("a plan revision card", () => {
  it("lists the decisions since the approved version, the finished work they reached, and the work that waits", () => {
    show(itemOf(revision()));
    const card = theCard();
    expect(card.textContent).toContain("Plan revision");
    const decisions = within(card).getByRole("list", { name: "Decisions since the approved version" });
    expect(within(decisions).getAllByRole("listitem").map((row) => row.textContent)).toEqual([
      expect.stringContaining("Use the wordmark."),
      expect.stringContaining("Number work orders with ULIDs."),
    ]);
    expect(decisions.textContent).toContain("Reversible, binds plan:plan-1");
    expect(decisions.textContent).toContain("One-way, binds plan:plan-1");
    const finished = within(card).getByRole("list", { name: "Finished work these decisions reached" });
    expect(finished.textContent).toContain("Add the budget");
    expect(finished.textContent).toContain("decisions d-logo");
    expect(within(card).getByText("1 work order in contract review")).not.toBeNull();
    expect(within(card).getAllByRole("button").map((button) => button.textContent)).toEqual(["Create rework", "Revise the plan", "Confirm version 2"]);
    expect(card.querySelector(".binding")?.textContent).toBe("Version 2 stays approved. Approving a new version or confirming this one ends the wait.");
  });

  it("confirms the approved version, and reads the ledger again", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    const after = vi.fn(async () => {});
    show(itemOf(revision()), after);
    await userEvent.click(within(theCard()).getByRole("button", { name: "Confirm version 2" }));
    await waitFor(() => expect(after).toHaveBeenCalled());
    expect(sent.filter((call) => call.method === "POST").map((call) => call.path)).toEqual(["/v1/plans/plan-1/revision/confirm"]);
  });

  it("creates the rework of one finished work order for the decisions that reached it", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    show(itemOf(revision()));
    await userEvent.click(within(theCard()).getByRole("button", { name: "Create rework for Add the budget" }));
    await waitFor(() => expect(sent.some((call) => call.method === "POST")).toBe(true));
    const [call] = sent.filter((row) => row.method === "POST");
    expect(call?.path).toBe("/v1/work-orders/wo-built/decision-rework");
    expect(call?.body).toEqual({ decisions: ["d-logo"] });
  });

  it("sends only the decisions still in force, and not one overturned since", async () => {
    const sent = serveApi(() => ({ status: 200, body: {} }));
    const need = revision();
    need.decisions.push({ id: "d-old", statement: "Use the monogram.", scope: "plan:plan-1", reversibility: "reversible", status: "overturned", decided_by: "member-1" });
    need.finished = [{ work_order: "wo-built", title: "Add the budget", state: "done", decisions: ["d-old", "d-logo"] }];
    show(itemOf(need));
    await userEvent.click(within(theCard()).getByRole("button", { name: "Create rework for Add the budget" }));
    await waitFor(() => expect(sent.some((call) => call.method === "POST")).toBe(true));
    expect(sent.find((call) => call.method === "POST")?.body).toEqual({ decisions: ["d-logo"] });
  });

  it("offers no rework of work that only overturned decisions reached", () => {
    const need = revision();
    need.decisions = need.decisions.map((decision) => (decision.id === "d-logo" ? { ...decision, status: "overturned" } : decision));
    show(itemOf(need));
    const card = theCard();
    expect(within(card).queryByRole("button", { name: /Create rework/ })).toBeNull();
    expect(within(card).getByRole("list", { name: "Finished work these decisions reached" }).textContent).toContain("no decision in force reached it");
  });

  it("says a work order was reworked, and offers no second rework of it", () => {
    const need = revision();
    need.finished = [{ work_order: "wo-built", title: "Add the budget", state: "done", decisions: ["d-logo"], rework: "wo-redo" }];
    show(itemOf(need));
    const card = theCard();
    expect(within(card).queryByRole("button", { name: /Create rework/ })).toBeNull();
    expect(card.textContent).toContain("Reworked as");
    expect(card.textContent).toContain("wo-redo");
  });

  it("lists no finished work when no reversible decision reached any", () => {
    const need = revision();
    need.finished = [];
    show(itemOf(need));
    expect(within(theCard()).queryByRole("list", { name: "Finished work these decisions reached" })).toBeNull();
  });

  it("says why a request was refused, and keeps the card", async () => {
    serveApi(() => refusal(409, "invalid", "work order wo-built is accepted, and only finished work gets rework for a decision"));
    show(itemOf(revision()));
    await userEvent.click(within(theCard()).getByRole("button", { name: "Create rework for Add the budget" }));
    await waitFor(() => expect(within(theCard()).getByText(/only finished work gets rework/)).not.toBeNull());
  });

  it("opens the session that revises the plan, which asks for a repository and a worker and not for a goal", async () => {
    serveApi(() => ({ status: 200, body: { repositories: [], workers: [], sources: [] } }));
    show(itemOf(revision()));
    await userEvent.click(within(theCard()).getByRole("button", { name: "Revise the plan" }));
    const dialog = await screen.findByRole("dialog", { name: "Revise Retry budget" });
    expect(within(dialog).queryByLabelText("Goal")).toBeNull();
    expect(within(dialog).getByText(/Its brief lists the decisions made since the approved version of Retry budget/)).not.toBeNull();
    expect(within(dialog).getByRole("button", { name: "Start revision session" })).not.toBeNull();
  });
});
