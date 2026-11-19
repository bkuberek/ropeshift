import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { Questions } from "../api/types";
import { openPage } from "../opening";
import { golden, here, serveViews } from "../testing";

const filters = () => screen.findByRole("navigation", { name: "Filter by category" });

// scopeOf reads what a question's card says of its scope.
const scopeOf = (card: HTMLElement) => within(card).getByText("Scope").closest("div")?.querySelector("dd")?.textContent;

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
    expect(within(card).getByText("Asked by").closest("div")?.querySelector("dd")?.textContent).toBe("lane-asking");
  });

  it("says a question nobody has brought to a person is open", async () => {
    serveViews();
    openPage("/questions");
    const card = await screen.findByRole("article", { name: "Does the plan cover a restart?" });
    expect(within(card).getByText("Open")).toBeTruthy();
  });

  it("names what a question's answer binds, with the title of the record its scope names", async () => {
    const review = golden<Questions>("questions").questions.find((question) => question.id === "question-review");
    expect(review?.scope).toMatch(/^plan:/);
    expect(review?.scope_title).toBeTruthy();
    serveViews();
    openPage("/questions");
    const card = await screen.findByRole("article", { name: "Does the plan cover a restart?" });
    expect(scopeOf(card)).toBe(`${review?.scope} · ${review?.scope_title}`);
    const other = await screen.findByRole("article", { name: "Should the budget hold across restarts?" });
    expect(within(other).queryByText("Scope")).toBeNull();
  });

  it("names a question's scope by its ID alone when the read gave no title", async () => {
    const view = golden<Questions>("questions");
    for (const question of view.questions) {
      delete question.scope_title;
    }
    serveViews((call) => (call.path.startsWith("/v1/views/questions") ? { status: 200, body: view } : undefined));
    openPage("/questions");
    const card = await screen.findByRole("article", { name: "Does the plan cover a restart?" });
    expect(scopeOf(card)).toBe(view.questions.find((question) => question.id === "question-review")?.scope);
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

// A member works a question in an orchestrator's place (FR-QD-3): merging it into a duplicate that stays, or recording an
// assumption for a reversible one, which a one-way question refuses.
describe("a member in an orchestrator's place", () => {
  const answering = (status = 201) =>
    serveViews((call) => {
      if (call.method === "POST" && /\/questions\/[^/]+\/(merge|answer)$/.test(call.path)) {
        return status === 204 ? { status } : { status, body: { id: "decision-9", status: "assumed", statement: "Keep the budget" } };
      }
      return undefined;
    });

  it("merges a question into the one that stays, which it names", async () => {
    const sent = answering(204);
    openPage("/questions");
    await userEvent.click(await screen.findByRole("button", { name: "Merge Should the budget hold across restarts?" }));
    const dialog = await screen.findByRole("dialog", { name: "Merge this question?" });
    const merge = within(dialog).getByRole("button", { name: "Merge question" });
    expect(merge.hasAttribute("disabled")).toBe(true);
    await userEvent.click(within(dialog).getByRole("radio", { name: "Does the plan cover a restart?" }));
    await userEvent.click(merge);
    await waitFor(() => expect(sent.some((call) => call.method === "POST" && call.path === "/v1/questions/question-1/merge")).toBe(true));
    expect(sent.find((call) => call.path === "/v1/questions/question-1/merge")?.body).toEqual({ into: "question-review" });
  });

  it("offers no question as its own merge target", async () => {
    answering(204);
    openPage("/questions");
    await userEvent.click(await screen.findByRole("button", { name: "Merge Should the budget hold across restarts?" }));
    const dialog = await screen.findByRole("dialog", { name: "Merge this question?" });
    expect(within(dialog).queryByRole("radio", { name: "Should the budget hold across restarts?" })).toBeNull();
  });

  it("offers a planning lane's question only targets a person answers", async () => {
    const view = golden<Questions>("questions");
    const planner = view.questions.find((question) => question.id === "question-1");
    if (!planner) {
      throw new Error("no question-1 in the golden");
    }
    planner.escalation_reason = "planner_asked";
    serveViews((call) => (call.path.startsWith("/v1/views/questions") ? { status: 200, body: view } : undefined));
    openPage("/questions");
    await userEvent.click(await screen.findByRole("button", { name: "Merge Should the budget hold across restarts?" }));
    const dialog = await screen.findByRole("dialog", { name: "Merge this question?" });
    expect(within(dialog).getByRole("radio", { name: /^May the/ })).toBeTruthy();
    expect(within(dialog).queryByRole("radio", { name: "Does the plan cover a restart?" })).toBeNull();
  });

  it("records an assumption for a reversible question, with why", async () => {
    const sent = answering();
    openPage("/questions");
    await userEvent.click(await screen.findByRole("button", { name: "Assume an answer to Should the budget hold across restarts?" }));
    await userEvent.type(await screen.findByRole("textbox", { name: "Why it is assumed" }), "The budget is already approved.");
    await userEvent.click(screen.getByRole("button", { name: "Record this assumption" }));
    await waitFor(() => expect(sent.some((call) => call.method === "POST" && call.path === "/v1/questions/question-1/answer")).toBe(true));
    expect(sent.find((call) => call.path === "/v1/questions/question-1/answer")?.body).toEqual(
      expect.objectContaining({ answer: "Keep the budget", assumed: true, rationale: "The budget is already approved." }),
    );
  });

  it("refuses an assumption for a one-way question, and sends nothing", async () => {
    const sent = answering();
    openPage("/questions");
    const card = await screen.findByRole("article", { name: "Does the plan cover a restart?" });
    await userEvent.click(within(card).getByRole("button", { name: "Assume an answer to Does the plan cover a restart?" }));
    expect(within(card).getByText(/A one-way question is answered by a person, never assumed/)).toBeTruthy();
    expect(within(card).queryByRole("button", { name: "Record this assumption" })).toBeNull();
    expect(sent.some((call) => call.method === "POST")).toBe(false);
  });
});
