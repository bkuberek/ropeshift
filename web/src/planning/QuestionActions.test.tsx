import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { Question, Questions } from "../api/types";
import { PageActionsProvider } from "../app/pageActions";
import type { Entry } from "../app/ranking";
import { golden, serveApi } from "../testing";
import { QuestionActions } from "./QuestionActions";

// What an open question offers in an orchestrator's place: Merge only while a question waits to stay, a merge dialog
// that never opens by itself, and an assumption that a one-way question refuses and that states why.

const questionOf = (id: string): Question => {
  const found = golden<Questions>("questions").questions.find((question) => question.id === id);
  if (!found) {
    throw new Error(`no ${id} in the golden`);
  }
  return found;
};

function shown(question: Question, others: Question[]) {
  let entries: Entry[] = [];
  const view = (others: Question[]) => (
    <PageActionsProvider value={(listed) => (entries = listed)}>
      <QuestionActions question={question} others={others} />
    </PageActionsProvider>
  );
  const page = render(view(others));
  return { entries: () => entries, rerender: (others: Question[]) => page.rerender(view(others)) };
}

const merge = (entries: Entry[]) => entries.find((entry) => entry.title.startsWith("Merge question:"));

describe("Merge on an open question", () => {
  it("is not listed while no other question waits, and is once one does", () => {
    serveApi(() => undefined);
    const one = questionOf("question-1");
    const page = shown(one, [one]);
    expect(merge(page.entries())).toBeUndefined();
    page.rerender([one, questionOf("question-2")]);
    expect(merge(page.entries())).toBeTruthy();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("does not open by itself when a question to stay arrives after the last one went", async () => {
    serveApi(() => undefined);
    const one = questionOf("question-1");
    const two = questionOf("question-2");
    const page = shown(one, [one, two]);
    act(() => {
      void merge(page.entries())?.run?.();
    });
    expect(await screen.findByRole("dialog", { name: "Merge this question?" })).toBeTruthy();
    page.rerender([one]);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    page.rerender([one, two]);
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

describe("Assume on an open question", () => {
  it("refuses a question that reached a person for a one-way choice, whatever its stated reversibility", async () => {
    const sent = serveApi(() => undefined);
    const oneWay = { ...questionOf("question-1"), reversibility: undefined, escalation_reason: "one_way" };
    shown(oneWay, [oneWay]);
    await userEvent.click(screen.getByRole("button", { name: `Assume an answer to ${oneWay.text}` }));
    expect(screen.getByText(/A one-way question is answered by a person, never assumed/)).toBeTruthy();
    expect(sent.some((call) => call.method === "POST")).toBe(false);
  });

  it("sends an assumption only once it says why", async () => {
    serveApi(() => undefined);
    const one = questionOf("question-1");
    shown(one, [one]);
    await userEvent.click(screen.getByRole("button", { name: `Assume an answer to ${one.text}` }));
    const record = screen.getByRole("button", { name: "Record this assumption" });
    expect(record.hasAttribute("disabled")).toBe(true);
    expect(screen.getByText("Say why, or this assumption is not sent.")).toBeTruthy();
    await userEvent.type(screen.getByRole("textbox", { name: "Why it is assumed" }), "The budget is already approved.");
    expect(record.hasAttribute("disabled")).toBe(false);
  });
});

// Decide now takes a question to its card in Needs you with the primary button focused, and decides nothing itself. A
// question still on the ladder is moved there first, by a button; one that already reached a person is linked to.
describe("Decide now", () => {
  it("takes a question off the ladder and opens its card in Needs you", async () => {
    const sent = serveApi((call) =>
      call.method === "POST" && call.path === "/v1/questions/question-1/decide-now" ? { status: 200, body: { ...questionOf("question-1"), status: "needs_human", batch_id: "batch-9" } } : undefined,
    );
    const question = { ...questionOf("question-1"), status: "open", batch_id: undefined };
    shown(question, [question]);
    await userEvent.click(screen.getByRole("button", { name: `Decide now: ${question.text}` }));
    await waitFor(() => expect(sent.filter((call) => call.method === "POST")).toHaveLength(1));
    await waitFor(() => expect(`${window.location.pathname}${window.location.search}`).toContain("/needs-you?item=questions%3Abatch-9&focus=primary"));
  });

  it("is not a button once the question waits for a person", () => {
    serveApi(() => undefined);
    const question = { ...questionOf("question-1"), status: "needs_human", batch_id: "batch-9" };
    shown(question, [question]);
    expect(screen.queryByRole("button", { name: /^Decide now/ })).toBeNull();
  });

  it("opens the question's card in Needs you once it reached a person", () => {
    serveApi(() => undefined);
    const question = { ...questionOf("question-1"), status: "needs_human", batch_id: "batch-9" };
    shown(question, [question]);
    const link = screen.getByRole("link", { name: `Decide now: ${question.text}` });
    expect(link.getAttribute("href")).toContain("/needs-you?item=questions%3Abatch-9&focus=primary");
  });

  it("is not a link while the question is still on the ladder", () => {
    serveApi(() => undefined);
    const question = { ...questionOf("question-1"), status: "open", batch_id: undefined };
    shown(question, [question]);
    expect(screen.queryByRole("link", { name: /^Decide now/ })).toBeNull();
  });
});
