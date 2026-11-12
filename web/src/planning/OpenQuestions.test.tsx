import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { Questions } from "../api/types";
import { openPage } from "../opening";
import { golden, here, serveViews } from "../testing";

const filters = () => screen.findByRole("navigation", { name: "Filter by category" });

// Filtering is the server's: a test that served the same list for every category would pass with no
// filter at all, so this serves only what the category holds.
function serveByCategory() {
  return serveViews((call) => {
    if (!call.path.startsWith("/v1/views/questions")) {
      return undefined;
    }
    const category = new URLSearchParams(call.path.split("?")[1]).get("category");
    const view = golden<Questions>("questions");
    view.questions = view.questions.filter((question) => !category || question.categories?.includes(category));
    return { status: 200, body: view };
  });
}

describe("the open questions", () => {
  it("shows each question and where it stands", async () => {
    serveViews();
    openPage("/questions");
    const card = await screen.findByRole("article", { name: "Should the budget hold across restarts?" });
    expect(within(card).getByText("Needs a person")).toBeTruthy();
    expect(within(card).getByText("Blocking")).toBeTruthy();
    expect(within(card).getByText("Product")).toBeTruthy();
    expect(within(card).getByText("Keep the budget")).toBeTruthy();
    expect(within(card).getByText("lane-asking")).toBeTruthy();
  });

  it("sends a question a person must answer to where it is answered", async () => {
    serveViews();
    openPage("/questions");
    const card = await screen.findByRole("article", { name: "Should the budget hold across restarts?" });
    expect(within(card).getByRole("link", { name: "Answer in Needs you" }).getAttribute("href")).toBe("/needs-you?kind=decisions");
  });

  it("offers no answer for a question nobody has brought to a person", async () => {
    serveViews();
    openPage("/questions");
    const card = await screen.findByRole("article", { name: "Does the plan cover a restart?" });
    expect(within(card).getByText("Open")).toBeTruthy();
    expect(within(card).queryByRole("link", { name: "Answer in Needs you" })).toBeNull();
  });

  it("shows how far a question climbed the ladder", async () => {
    serveViews();
    openPage("/questions");
    const card = await screen.findByRole("article", { name: "Should the budget hold across restarts?" });
    expect(within(card).getByText("Rung 5")).toBeTruthy();
    const trace = within(card).getByText("How it got here").closest("details");
    expect(trace?.textContent).toContain("Rung 1");
    expect(trace?.textContent).toContain("no match");
    expect(trace?.textContent).toContain("decision:decision-1");
  });

  it("opens one question from a link", async () => {
    serveViews();
    openPage("/questions?id=question-review");
    expect(await screen.findByRole("article", { name: "Does the plan cover a restart?" })).toBeTruthy();
    expect(screen.queryByRole("article", { name: "Should the budget hold across restarts?" })).toBeNull();
    expect(screen.getByRole("link", { name: "All open questions" }).getAttribute("href")).toBe(here("/questions"));
  });
});

describe("the category filter", () => {
  it("offers every category the server names, and all of them", async () => {
    serveViews();
    openPage("/questions");
    const links = within(await filters()).getAllByRole("link");
    expect(links.map((link) => link.textContent)).toEqual([
      "All", "Architecture", "Business", "Compliance", "Data", "Design", "Operations", "Product", "Security",
    ]);
  });

  it("marks the one in force", async () => {
    serveViews();
    openPage("/questions");
    expect(within(await filters()).getByRole("link", { name: "All" }).getAttribute("aria-current")).toBe("true");
  });

  it("asks the server for one category and shows what it holds", async () => {
    const sent = serveByCategory();
    openPage("/questions");
    await screen.findByRole("article", { name: "Should the budget hold across restarts?" });
    await userEvent.click(within(await filters()).getByRole("link", { name: "Security" }));
    expect(await screen.findByRole("article", { name: "Does the plan cover a restart?" })).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole("article", { name: "Should the budget hold across restarts?" })).toBeNull());
    expect(sent.some((call) => call.path === "/v1/views/questions?category=security")).toBe(true);
    expect(window.location.search).toBe("?category=security");
    expect(within(await filters()).getByRole("link", { name: "Security" }).getAttribute("aria-current")).toBe("true");
  });

  it("holds a filter in the address, so a link keeps it", async () => {
    const sent = serveByCategory();
    openPage("/questions?category=security");
    await screen.findByRole("article", { name: "Does the plan cover a restart?" });
    expect(sent.some((call) => call.path === "/v1/views/questions?category=security")).toBe(true);
  });

  it("clears back to all", async () => {
    serveByCategory();
    openPage("/questions?category=security");
    await screen.findByRole("article", { name: "Does the plan cover a restart?" });
    await userEvent.click(within(await filters()).getByRole("link", { name: "All" }));
    expect(await screen.findByRole("article", { name: "Should the budget hold across restarts?" })).toBeTruthy();
    expect(window.location.search).toBe("");
  });

  it("keeps the keyboard on the filter it pressed", async () => {
    serveByCategory();
    openPage("/questions");
    await screen.findByRole("article", { name: "Should the budget hold across restarts?" });
    const security = within(await filters()).getByRole("link", { name: "Security" });
    await userEvent.click(security);
    await screen.findByRole("article", { name: "Does the plan cover a restart?" });
    expect(document.activeElement?.textContent).toBe("Security");
  });

  it("says when a category holds nothing, and offers the way back", async () => {
    serveByCategory();
    openPage("/questions?category=data");
    expect(await screen.findByText("Nothing open in Data.")).toBeTruthy();
    expect(within(await filters()).getByRole("link", { name: "All" })).toBeTruthy();
  });

  it("names its count in the title", async () => {
    serveViews();
    openPage("/questions");
    expect(await screen.findByText("3 open")).toBeTruthy();
  });
});
