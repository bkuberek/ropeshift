import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { Approval, FieldChange, NeedItem, NeedsYou as View } from "../api/types";
import { CurrentProject } from "../app/route";
import { golden, here, refusal, serveApi, testProject } from "../testing";
import { NeedsYou } from "./NeedsYou";

type CardItem = Extract<NeedItem, { kind: "confirm" }>;

// The Needs you of a ledger where a delegate prepared four cards: a repository change, a roster change, keys
// for a lane's pane, and a cancel.
const cards = () => golden<View>("needs-you-cards");
const confirmed = () => golden<Approval>("card-confirmed");

function cardOf(view: View, operation: string): CardItem {
  const found = view.items.find((item): item is CardItem => item.kind === "confirm" && item.card.detail.operation === operation);
  if (!found) {
    throw new Error(`no card of ${operation}`);
  }
  return found;
}

function show(view: View, item: CardItem, after: () => Promise<void> = async () => {}) {
  window.history.replaceState(null, "", `${here("/")}?item=${encodeURIComponent(`confirm:${item.id}`)}`);
  const result = render(
    <CurrentProject.Provider value={testProject}>
      <NeedsYou view={view} readAt={Date.now()} after={after} />
    </CurrentProject.Provider>,
  );
  const rerender = (next: View) =>
    result.rerender(
      <CurrentProject.Provider value={testProject}>
        <NeedsYou view={next} readAt={Date.now()} after={after} />
      </CurrentProject.Provider>,
    );
  return { ...result, rerender };
}

const theCard = (title: string) => screen.getByRole("article", { name: title });

describe("a confirmation card", () => {
  it("is listed among the cards with the delegate that prepared it", () => {
    const view = cards();
    const item = cardOf(view, "repository.raise_merge_authority");
    show(view, item);
    const link = document.getElementById(`need-confirm:${item.id}`);
    expect(link?.textContent).toContain("Raise merge authority: harbor-web");
    expect(link?.textContent).toContain("planning assistant for member-1");
    expect(link?.querySelector(".item-meta svg")).not.toBeNull();
    expect(screen.getByRole("button", { name: /^Cards/ }).textContent).toContain("4");
  });

  it("says what it changes, why, the exact call, and what it is bound to", () => {
    const view = cards();
    const item = cardOf(view, "repository.raise_merge_authority");
    show(view, item);
    const card = theCard("Raise merge authority: harbor-web");
    expect(card.classList.contains("card-confirm")).toBe(true);
    expect(card.textContent).toContain("Confirmation card");
    expect(card.textContent).toContain("Prepared by planning assistant for member-1");
    expect(within(card).getByRole("region", { name: "What it changes" }).textContent).toMatch(/merge_authority: person_merges\s*→\s*to\s*person_approves/);
    expect(within(card).getByRole("region", { name: "Why the delegate proposes it" }).textContent).toContain(item.card.detail.why);
    const operation = card.querySelector("details.disclosure pre")?.textContent ?? "";
    expect(operation).toContain(`PATCH ${item.card.detail.path}`);
    expect(operation).toContain('"merge_authority": "person_approves"');
    expect(operation).toContain(`digest: ${item.card.approval.subject_digest}`);
    const digest = item.card.approval.subject_digest.replace("sha256:", "").slice(0, 7);
    expect(card.querySelector(".binding")?.textContent).toBe(`Bound to ${item.card.approval.subject} at ${digest}. If it changes before you confirm, this card is void.`);
    const buttons = within(card).getAllByRole("button").map((button) => button.textContent);
    expect(buttons).toEqual(["Decline", "Confirm"]);
  });

  it("shows a body it replaces as a difference", () => {
    const view = cards();
    const item = cardOf(view, "roster.change");
    show(view, item);
    const changes = within(theCard(item.card.approval.summary)).getByRole("region", { name: "What it changes" });
    expect(within(changes).getByRole("table", { name: "What the card changes" })).not.toBeNull();
    expect(changes.textContent).toContain("high");
  });

  it("says keys for a pane are bound to the lane, not to what the pane shows", () => {
    const view = cards();
    const item = cardOf(view, "pane.input");
    show(view, item);
    expect(theCard(item.card.approval.summary).textContent).toContain("These keys are bound to lane lane-w1, not to what its pane shows");
  });

  it("confirms with the digest it shows, and then says how it came out", async () => {
    const view = cards();
    const item = cardOf(view, "work_order.cancel");
    const sent = serveApi((call) => (call.method === "GET" ? { status: 200, body: confirmed() } : { status: 200, body: confirmed() }));
    const after = vi.fn(async () => {});
    const { rerender } = show(view, item, after);
    await userEvent.click(within(theCard("Cancel work: w1")).getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(after).toHaveBeenCalledOnce());
    expect(sent[0]).toMatchObject({ method: "POST", path: `/v1/approvals/${item.id}/decide`, project: testProject, body: { digest: item.card.approval.subject_digest, allow: true } });
    rerender({ ...view, items: view.items.filter((row) => row.id !== item.id) });
    const resolved = await screen.findByRole("status");
    expect(resolved.textContent).toContain("Confirmed");
    expect(resolved.textContent).toMatch(/by member-1 from the CLI at \d{2}:\d{2}/);
    expect(screen.queryByRole("button", { name: "Confirm" })).toBeNull();
    // The Confirm button left the page with the card's buttons, so focus goes to the outcome in their place.
    await waitFor(() => expect(document.activeElement).toBe(resolved));
  });

  it("declines with nothing run", async () => {
    const view = cards();
    const item = cardOf(view, "work_order.cancel");
    const sent = serveApi(() => ({ status: 200 }));
    show(view, item);
    await userEvent.click(within(theCard("Cancel work: w1")).getByRole("button", { name: "Decline" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.body).toMatchObject({ digest: item.card.approval.subject_digest, allow: false });
  });

  it("goes void when its subject moved, and offers no Confirm", async () => {
    const view = cards();
    const item = cardOf(view, "repository.raise_merge_authority");
    const voided = { ...item.card.approval, state: "void", reason: "The subject changed since the card was prepared." };
    serveApi((call) => (call.method === "GET" ? { status: 200, body: voided } : refusal(409, "stale_digest", "the digest does not match the request")));
    const { rerender } = show(view, item);
    await userEvent.click(within(theCard("Raise merge authority: harbor-web")).getByRole("button", { name: "Confirm" }));
    rerender({ ...view, items: view.items.filter((row) => row.id !== item.id) });
    await waitFor(() => expect(screen.getAllByText("The subject changed since the card was prepared. Ask the delegate to prepare it again.").length).toBeGreaterThan(0));
    expect(screen.getAllByText("Void").length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", { name: "Confirm" })).toBeNull();
  });

  it("keeps the API's refusal when its call failed to run", async () => {
    const view = cards();
    const item = cardOf(view, "work_order.cancel");
    const failed = { ...confirmed(), state: "failed", reason: "illegal_move: work order w1 is canceled" };
    serveApi(() => ({ status: 200, body: failed }));
    const { rerender } = show(view, item);
    rerender({ ...view, items: view.items.filter((row) => row.id !== item.id) });
    expect((await screen.findByRole("alert")).textContent).toContain("illegal_move: work order w1 is canceled");
    expect(screen.getAllByText("Failed to run").length).toBeGreaterThan(0);
  });
});

// A delegate's or the assistant's acknowledgement of a practices finding, or the withdrawal of one, is a card that names the
// finding, why its practice matters, and the reason, as the API prepares it.
describe("a practices card", () => {
  const practicesCard = (operation: "practices.acknowledge" | "practices.withdraw", label: string, summary: string, rows?: FieldChange[]): CardItem => {
    const base = cardOf(cards(), "repository.raise_merge_authority");
    // As the API prepares them: a withdrawal shows the reason that stops holding as the value it leaves, not one it takes.
    const reason = operation === "practices.acknowledge" ? { field: "reason", to: "the whole team owns every path" } : { field: "reason", from: "the whole team owns every path", to: null };
    const changes = rows ?? [{ field: "finding", to: "codeowners" }, { field: "why", to: "It names who reviews each path." }, reason];
    const path = operation === "practices.acknowledge" ? "/v1/projects/default/practices/acknowledgements" : "/v1/projects/default/practices/acknowledgements/withdraw";
    const detail = { operation, label, method: "POST", path, arguments: { repository: "repo-1", finding: "codeowners" }, why: "The plan names this step.", changes };
    return {
      ...base,
      id: operation,
      card: { ...base.card, approval: { ...base.card.approval, id: operation, summary, subject: "practices-finding:repo-1:codeowners", detail: JSON.stringify(detail) }, detail },
    };
  };

  it.each([
    ["practices.acknowledge", "Acknowledge a practices finding", "Acknowledge a practices finding: codeowners in widgets"],
    ["practices.withdraw", "Withdraw a finding's acknowledgement", "Withdraw a finding's acknowledgement: codeowners in widgets"],
  ] as const)("reads %s with the finding, why it matters, and the reason", (operation, label, summary) => {
    const view = cards();
    const item = practicesCard(operation, label, summary);
    view.items = [item, ...view.items];
    show(view, item);
    const card = theCard(summary);
    const changes = within(card).getByRole("region", { name: "What it changes" });
    expect(changes.textContent).toContain("codeowners");
    expect(changes.textContent).toContain("It names who reviews each path.");
    expect(changes.textContent).toContain("the whole team owns every path");
    expect(within(card).getByRole("button", { name: "Confirm" })).toBeTruthy();
  });

  // A card prepared before arguments were sent as {field, to} holds them as {field, from: null, to}: nothing was there to
  // move from, so the row names the value alone.
  it("reads an argument a card holds with a null from as the value alone", () => {
    const summary = "Acknowledge a practices finding: codeowners in widgets";
    const view = cards();
    const item = practicesCard("practices.acknowledge", "Acknowledge a practices finding", summary, [
      { field: "finding", from: null, to: "codeowners" },
      { field: "reason", from: null, to: "the whole team owns every path" },
    ]);
    view.items = [item, ...view.items];
    show(view, item);
    const rows = within(theCard(summary)).getByRole("region", { name: "What it changes" }).querySelectorAll("li");
    expect(Array.from(rows, (row) => row.textContent)).toEqual(["finding: codeowners", "reason: the whole team owns every path"]);
  });

  // The API sends a withdrawal's reason as the value that stops holding, "from" it "to" null.
  it("reads a field whose value goes away as removed, not as a move to null", () => {
    const summary = "Withdraw a finding's acknowledgement: codeowners in widgets";
    const view = cards();
    const item = practicesCard("practices.withdraw", "Withdraw a finding's acknowledgement", summary, [
      { field: "finding", from: null, to: "codeowners" },
      { field: "reason", from: "the whole team owns every path", to: null },
    ]);
    view.items = [item, ...view.items];
    show(view, item);
    const row = within(theCard(summary)).getByText("reason").closest("li");
    expect(row?.textContent).toMatch(/reason: the whole team owns every path\s*→\s*to\s*removed/);
    expect(row?.textContent).not.toContain("null");
  });
});

// The caps a card sets are counts where zero means no limit, which the API sends as a number in "from" and "to".
describe("a card that sets the fleet's caps", () => {
  const capsCard = (operation: string, changes: FieldChange[]): CardItem => {
    const base = cardOf(cards(), "repository.raise_merge_authority");
    const detail = { ...base.card.detail, operation, label: operation, method: "PUT", path: "/v1/fleet/caps", why: "The plan names this limit.", changes };
    return {
      ...base,
      id: operation,
      card: { ...base.card, approval: { ...base.card.approval, id: operation, summary: `Change ${operation}`, subject: `limit:${operation}`, detail: JSON.stringify(detail) }, detail },
    };
  };

  const rowsOf = (item: CardItem) => {
    const view = cards();
    view.items = [item, ...view.items];
    show(view, item);
    const region = within(theCard(item.card.approval.summary)).getByRole("region", { name: "What it changes" });
    return Array.from(region.querySelectorAll("li"), (row) => row.textContent);
  };

  it("reads a cap that was no limit as no limit", () => {
    expect(rowsOf(capsCard("fleet.caps", [{ field: "wip_limit", from: 0, to: 5 }]))).toEqual(["wip_limit: no limit →to 5"]);
  });

  it("reads a cap that goes to no limit as no limit", () => {
    expect(rowsOf(capsCard("fleet.caps", [{ field: "lanes_per_repository", from: 5, to: 0 }]))).toEqual(["lanes_per_repository: 5 →to no limit"]);
  });

  it("reads a cap that has a value on both sides as those values", () => {
    expect(rowsOf(capsCard("fleet.caps", [{ field: "lanes_per_worker", from: 3, to: 5 }]))).toEqual(["lanes_per_worker: 3 →to 5"]);
  });

  it("leaves a practices row with a zero as the value it holds", () => {
    expect(rowsOf(capsCard("practices.acknowledge", [{ field: "reason", from: 0, to: 5 }]))).toEqual(["reason: 0 →to 5"]);
  });
});
