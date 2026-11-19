import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { Question, Questions } from "../api/types";
import { openPage } from "../opening";
import { golden, here, serveViews } from "../testing";

// A member sees the whole ladder (FR-QD-13): who holds each question, every rung's judgment with why, the lanes on it,
// its batch, and, through a status filter, the questions closed each way with what closed them.

const budget = "Should the budget hold across restarts?";
const review = "Does the plan cover a restart?";

// factOf reads what a question's card says beside one fact's name.
const factOf = (card: HTMLElement, name: string) => within(card).getByText(name, { selector: "dt" }).closest("div")?.querySelector("dd");

// serveQuestions answers the questions view with the golden as change leaves it, and records each path it was read at.
function serveQuestions(change: (questions: Question[]) => void) {
  const view = golden<Questions>("questions");
  change(view.questions);
  return serveViews((call) => (call.path.startsWith("/v1/views/questions") ? { status: 200, body: view } : undefined));
}

const find = (questions: Question[], id: string) => {
  const found = questions.find((question) => question.id === id);
  if (!found) {
    throw new Error(`no ${id} in the golden`);
  }
  return found;
};

describe("who holds a question", () => {
  it("names the orchestrator that holds it by its name and its machine", async () => {
    serveQuestions((questions) => {
      find(questions, "question-1").holder = { id: "worker-a", orchestrator: "orchestrator-worker-a", machine: "solo", until: 1790000999001 };
    });
    openPage("/questions");
    const held = factOf(await screen.findByRole("article", { name: budget }), "Held by");
    expect(held?.textContent).toContain("Orchestrator on solo");
    expect(held?.textContent).toContain("orchestrator-worker-a");
  });

  it("names a member that holds it by ID", async () => {
    serveQuestions((questions) => {
      find(questions, "question-1").holder = { id: "member-1", until: 1790000999001 };
    });
    openPage("/questions");
    expect(factOf(await screen.findByRole("article", { name: budget }), "Held by")?.textContent).toBe("member-1");
  });

  it("says when nobody holds an open question", async () => {
    serveViews();
    openPage("/questions");
    expect(factOf(await screen.findByRole("article", { name: budget }), "Held by")?.textContent).toBe("Nobody holds it");
  });
});

describe("how a question got here", () => {
  it("shows each rung's judgment: who worked it, the decision, why, and what it cites", async () => {
    serveQuestions((questions) => {
      const question = find(questions, "question-1");
      question.machines = { "worker-a": "solo" };
      question.ladder_trace = [
        { rung: 1, outcome: "no match", candidates: ["decision:decision-1"], by: "worker-a" },
        { rung: 3, outcome: "decided", candidates: ["PAY-12"], by: "worker-a", decision: "decision-7", rationale: "The budget is already approved." },
        { rung: 4, outcome: "no_orchestrator" },
      ];
    });
    openPage("/questions");
    const card = await screen.findByRole("article", { name: budget });
    await userEvent.click(within(card).getByText("How it got here"));
    const [looked, judged, unworked] = within(within(card).getByRole("list", { name: "Rungs it passed" })).getAllByRole("listitem");
    if (!looked || !judged || !unworked) {
      throw new Error("the trace does not hold three rungs");
    }
    expect(looked.textContent).toContain("Orchestrator on solo");
    expect(looked.textContent).toContain("Considered");
    expect(judged.textContent).toContain("The budget is already approved.");
    expect(judged.textContent).toContain("Cites");
    expect(judged.textContent).toContain("PAY-12");
    expect(within(judged).getByRole("link", { name: "decision-7" }).getAttribute("href")).toBe(here("/decisions?id=decision-7"));
    expect(unworked.textContent).toContain("No orchestrator worked this rung");
    expect(unworked.textContent).not.toContain("Orchestrator on");
  });

  it("lists the lanes that asked it or wait on it", async () => {
    serveViews();
    openPage("/questions");
    const card = await screen.findByRole("article", { name: budget });
    await userEvent.click(within(card).getByText("How it got here"));
    const lanes = within(card).getByRole("list", { name: "Lanes on this question" });
    const lane = within(lanes).getByRole("link", { name: "lane-asking" });
    expect(lane.getAttribute("href")).toBe(here("/lane?id=lane-asking"));
    expect(lanes.textContent).toContain("blocking");
  });
});

describe("a question's batch", () => {
  it("names the batch that gathered it and where the batch stands", async () => {
    serveViews();
    openPage("/questions");
    const batch = factOf(await screen.findByRole("article", { name: budget }), "Batch");
    expect(batch?.textContent).toContain("generated-1");
    expect(batch?.textContent).toContain("Delivered");
  });

  it("names no batch for a question none gathered", async () => {
    serveQuestions((questions) => {
      const question = find(questions, "question-1");
      delete question.batch;
      delete question.batch_id;
    });
    openPage("/questions");
    expect(within(await screen.findByRole("article", { name: budget })).queryByText("Batch", { selector: "dt" })).toBeNull();
  });
});

describe("the status filter", () => {
  const statuses = () => screen.findByRole("navigation", { name: "Filter by status" });

  it("offers the open questions, each way a question closes, and every question", async () => {
    serveViews();
    openPage("/questions");
    const links = within(await statuses()).getAllByRole("link");
    expect(links.map((link) => link.textContent)).toEqual(["Open", "Answered", "Merged", "Dismissed", "All"]);
    expect(within(await statuses()).getByRole("link", { name: "Open" }).getAttribute("aria-current")).toBe("true");
  });

  it("asks the server for a status and keeps it in the address", async () => {
    const sent = serveViews();
    openPage("/questions");
    await screen.findByRole("article", { name: budget });
    await userEvent.click(within(await statuses()).getByRole("link", { name: "Answered" }));
    await waitFor(() => expect(sent.some((call) => call.path === "/v1/views/questions?status=answered")).toBe(true));
    expect(window.location.search).toBe("?status=answered");
    expect(within(await statuses()).getByRole("link", { name: "Answered" }).getAttribute("aria-current")).toBe("true");
    expect(await screen.findByRole("heading", { level: 1, name: "Questions" })).toBeTruthy();
  });

  it("shows an answered question with the decision that closed it", async () => {
    serveQuestions((questions) => {
      const question = find(questions, "question-1");
      question.status = "answered";
      question.decision_id = "decision-9";
      question.machines = { "worker-a": "solo" };
      question.closed_by = { id: "decision-9", status: "assumed", statement: "Keep the budget", rationale: "The budget is already approved.", decided_by: "worker-a" };
    });
    openPage("/questions?status=answered");
    const card = await screen.findByRole("article", { name: budget });
    expect(within(card).getByText("Answered")).toBeTruthy();
    const closed = factOf(card, "Closed by");
    expect(closed?.textContent).toContain("Keep the budget");
    expect(closed?.textContent).toContain("Assumed");
    expect(closed?.textContent).toContain("The budget is already approved.");
    expect(closed?.textContent).toContain("Orchestrator on solo");
    expect(within(closed as HTMLElement).getByRole("link", { name: "decision-9" }).getAttribute("href")).toBe(here("/decisions?id=decision-9"));
    expect(within(card).queryByText("Held by", { selector: "dt" })).toBeNull();
    expect(within(card).queryByRole("button", { name: `Answer ${budget}` })).toBeNull();
  });

  it("shows a merged question with the one that took its place", async () => {
    serveQuestions((questions) => {
      const question = find(questions, "question-1");
      question.status = "dismissed";
      question.merged_into = "question-review";
    });
    openPage("/questions?status=merged");
    const card = await screen.findByRole("article", { name: budget });
    expect(within(card).getByText("Merged")).toBeTruthy();
    const into = within(factOf(card, "Merged into") as HTMLElement).getByRole("link", { name: "question-review" });
    expect(into.getAttribute("href")).toBe(here("/questions?id=question-review&status=all"));
  });

  it("says when a status holds nothing", async () => {
    serveQuestions((questions) => questions.splice(0));
    openPage("/questions?status=dismissed");
    expect(await screen.findByText("No question is dismissed.")).toBeTruthy();
  });

  it("reaches every question from the command palette", async () => {
    const sent = serveViews();
    openPage("/questions");
    await screen.findByRole("article", { name: budget });
    await userEvent.keyboard("{Control>}k{/Control}");
    await screen.findByRole("dialog", { name: "Command palette" });
    await userEvent.keyboard("every status");
    await userEvent.click(await screen.findByRole("option", { name: /^Questions: every status/ }));
    await waitFor(() => expect(window.location.search).toBe("?status=all"));
    await waitFor(() => expect(sent.some((call) => call.path === "/v1/views/questions?status=all")).toBe(true));
  });

  it("opens a closed question from a link, with every status", async () => {
    serveViews();
    openPage("/questions?id=question-review&status=all");
    expect(await screen.findByRole("article", { name: review })).toBeTruthy();
    expect(screen.getByRole("link", { name: "All questions" }).getAttribute("href")).toBe(here("/questions?status=all"));
  });
});

// Every status reads in a person's words wherever a sentence names it: the title's count, the sentence for a status
// that holds nothing, and the word beside each question.
describe("the words for each status", () => {
  const statuses = [
    { status: "open", phrase: "open", side: "Open" },
    { status: "triage", phrase: "in triage", side: "In triage" },
    { status: "needs_human", phrase: "waiting for a person", side: "Needs a person" },
    { status: "answered", phrase: "answered", side: "Answered" },
    { status: "dismissed", phrase: "dismissed", side: "Dismissed" },
  ];

  it.each(statuses)("counts the questions $status as $phrase in the title", async ({ status, phrase }) => {
    serveQuestions((questions) => {
      for (const question of questions) {
        question.status = status;
      }
    });
    openPage(`/questions?status=${status}`);
    expect(await screen.findByText(`3 ${phrase}`)).toBeTruthy();
    expect(document.body.textContent).not.toContain("needs_human");
  });

  it.each(statuses)("says no question is $phrase when status $status holds none", async ({ status, phrase }) => {
    serveQuestions((questions) => questions.splice(0));
    openPage(`/questions?status=${status}`);
    expect(await screen.findByText(`No question is ${phrase}.`)).toBeTruthy();
    expect(await screen.findByText(`0 ${phrase}`)).toBeTruthy();
    expect(document.body.textContent).not.toContain("needs_human");
  });

  it("says the same within a category", async () => {
    serveQuestions((questions) => questions.splice(0));
    openPage("/questions?status=needs_human&category=data");
    expect(await screen.findByText("No question in Data is waiting for a person.")).toBeTruthy();
  });

  it.each(statuses)("names a question $status as $side beside it", async ({ status, side }) => {
    serveQuestions((questions) => {
      find(questions, "question-1").status = status;
    });
    openPage("/questions?status=all");
    const card = await screen.findByRole("article", { name: budget });
    expect(card.querySelector(".wait-word")?.textContent).toBe(side);
  });

  it("names a merged question as merged, in the count and beside it", async () => {
    serveQuestions((questions) => {
      for (const question of questions) {
        question.status = "dismissed";
        question.merged_into = "question-other";
      }
    });
    openPage("/questions?status=merged");
    expect(await screen.findByText("3 merged")).toBeTruthy();
    expect((await screen.findByRole("article", { name: budget })).querySelector(".wait-word")?.textContent).toBe("Merged");
  });
});
