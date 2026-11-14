import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { Decisions } from "../api/types";
import { openPage } from "../opening";
import { golden, here, serveViews, withStyles } from "../testing";

const decision = (name: string) => screen.findByRole("article", { name });

const worker = "f518dab3196776ff0132570251797fb7";
const longId = "2ebe3638daa637d3312b07a26757baab";

// serveByOrchestrator adds a decision an orchestrator made, which records its worker as who decided it.
function serveByOrchestrator() {
  const view = golden<Decisions>("decisions");
  view.decisions.unshift({ id: longId, status: "decided", statement: "Retry once.", decided_by: worker, decided_by_name: "solo", lineage: [longId] });
  serveViews((call) => (call.path.startsWith("/v1/views/decisions") ? { status: 200, body: view } : undefined));
}

describe("the decisions", () => {
  it("states each decision, its status, and its rationale", async () => {
    serveViews();
    openPage("/decisions");
    const card = await decision("Retry twice.");
    expect(within(card).getByText("Replaced")).toBeTruthy();
    expect(within(card).getByText("It covers the outage.")).toBeTruthy();
    expect(within(card).getByText("retries")).toBeTruthy();
    expect(within(card).getByText("Reversible")).toBeTruthy();
    expect(within(card).getByText("member-1")).toBeTruthy();
  });

  it("keeps a decision's ID and who decided it on one line, whole in their titles", async () => {
    serveViews();
    openPage("/decisions");
    const card = await decision("Retry twice.");
    const side = card.querySelector(".record-kind") as HTMLElement;
    const decidedBy = within(card).getByText("Decided by").closest("div") as HTMLElement;
    for (const [scope, id] of [
      [side, "decision-1"],
      [decidedBy, "member-1"],
    ] as const) {
      const shown = within(scope).getByText(id);
      expect(shown.classList.contains("id"), id).toBe(true);
      expect(shown.getAttribute("title")).toBe(id);
    }
  });

  it("names the worker whose orchestrator decided, with the worker's ID in its title", async () => {
    serveByOrchestrator();
    openPage("/decisions");
    const card = await decision("Retry once.");
    const decidedBy = within(card).getByText("Decided by").closest("div") as HTMLElement;
    const name = within(decidedBy).getByText("solo");
    expect(name.getAttribute("title")).toBe(worker);
    expect(decidedBy.textContent).not.toContain(worker);
  });

  it("ends an ID too long for the side column in an ellipsis, so it never runs into the statement", async () => {
    withStyles();
    serveByOrchestrator();
    openPage("/decisions");
    const card = await decision("Retry once.");
    const id = within(card.querySelector(".record-kind") as HTMLElement).getByText(longId);
    const style = getComputedStyle(id);
    expect([style.overflow, style.textOverflow, style.whiteSpace]).toEqual(["hidden", "ellipsis", "nowrap"]);
    expect(id.getAttribute("title")).toBe(longId);
  });

  it("shows an ID in the side column as stored, not in capitals", async () => {
    withStyles();
    serveByOrchestrator();
    openPage("/decisions");
    const card = await decision("Retry once.");
    const id = within(card.querySelector(".record-kind") as HTMLElement).getByText(longId);
    expect(getComputedStyle(id).textTransform).toBe("none");
  });

  it("tells an assumption from a decision", async () => {
    serveViews();
    openPage("/decisions");
    expect(within(await decision("Retry until the budget runs out.")).getByText("Assumed")).toBeTruthy();
    expect(within(await decision("Retry three times.")).getByText("Replaced")).toBeTruthy();
  });

  it("names what a decision cites", async () => {
    serveViews();
    openPage("/decisions");
    expect(within(await decision("Retry until the budget runs out.")).getByText("requirement:FR-RETRY-1")).toBeTruthy();
  });

  it("walks a supersede chain from any link in it", async () => {
    serveViews();
    openPage("/decisions");
    const card = await decision("Retry three times.");
    const chain = within(card).getByRole("list", { name: "Lineage" });
    expect([...chain.querySelectorAll("li")].map((item) => item.textContent)).toEqual(["decision-1", "decision-2", "decision-3"]);
    expect(within(chain).getByRole("link", { name: "decision-1" }).getAttribute("href")).toBe(here("/decisions?id=decision-1"));
    expect(within(chain).getByRole("link", { name: "decision-3" }).getAttribute("href")).toBe(here("/decisions?id=decision-3"));
    expect(within(chain).queryByRole("link", { name: "decision-2" })).toBeNull();
    expect(within(chain).getByText("decision-2").getAttribute("aria-current")).toBe("true");
  });

  it("says what replaced a decision and what it replaced", async () => {
    serveViews();
    openPage("/decisions");
    const card = await decision("Retry three times.");
    const replaces = within(card).getByText("Replaces").closest("div");
    const replacedBy = within(card).getByText("Replaced by").closest("div");
    expect(replaces?.textContent).toContain("decision-1");
    expect(replacedBy?.textContent).toContain("decision-3");
  });

  it("opens one decision from a link, and leads back", async () => {
    serveViews();
    openPage("/decisions?id=decision-3");
    expect(await screen.findByRole("heading", { level: 1, name: "decision-3" })).toBeTruthy();
    expect(await decision("Retry until the budget runs out.")).toBeTruthy();
    expect(screen.queryByRole("article", { name: "Retry twice." })).toBeNull();
    expect(screen.getByRole("link", { name: "All decisions" }).getAttribute("href")).toBe(here("/decisions"));
  });

  it("follows a lineage link in place", async () => {
    serveViews();
    openPage("/decisions?id=decision-3");
    await decision("Retry until the budget runs out.");
    await userEvent.click(screen.getByRole("link", { name: "decision-1" }));
    expect(await decision("Retry twice.")).toBeTruthy();
    expect(window.location.search).toBe("?id=decision-1");
  });

  it("says so when no decision has that ID", async () => {
    serveViews();
    openPage("/decisions?id=no-such-decision");
    expect(await screen.findByRole("heading", { level: 1, name: "No such decision" })).toBeTruthy();
  });

  it("says so when there are no decisions", async () => {
    serveViews((call) => (call.path.startsWith("/v1/views/decisions") ? { status: 200, body: { as_of: 1, revision: 1, decisions: [] } } : undefined));
    openPage("/decisions");
    expect(await screen.findByText("No decisions yet.")).toBeTruthy();
  });
});
