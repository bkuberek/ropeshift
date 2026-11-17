import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { Batch, QuestionItem } from "../api/types";
import { CurrentProject } from "../app/route";
import { serveApi, testProject } from "../testing";
import { QuestionBatch, startingDraft } from "./QuestionBatch";

// The item of a batch as ADR-0030 gives it: it states the reversibility its answer will have, which the
// application's type does not carry yet.
type Stated = QuestionItem & { reversibility?: string };

// The batch of the mid-plan scenario: 18 questions, each with two options and the first as the
// recommendation. The fourth reaches a one-way decision through its category, not through its reason.
function midPlanBatch(): Batch {
  const items: Stated[] = Array.from({ length: 18 }, (_, index) => ({
    id: `q${index + 1}`,
    text: `Question ${index + 1}`,
    options: ["A", "B"],
    recommendation: "A",
    escalation_reason: "scope_change",
    ...(index === 3 ? { reversibility: "one_way" } : {}),
  }));
  return { batch: { id: "batch-1", status: "delivered", opened_at: 1, closes_at: 2, delivered_at: 2 }, items };
}

function show(batch: Batch) {
  return render(
    <CurrentProject.Provider value={testProject}>
      <QuestionBatch batch={batch} after={async () => {}} frame={{ title: "Questions" }} />
    </CurrentProject.Provider>,
  );
}

describe("a batch of 18 questions raised in the middle of a plan", () => {
  // GAP-MP-G6: Accept all records a one-way decision with no deliberate choice. A question that reaches a
  // one-way decision through its category states it as reversibility, and starts with nothing chosen. The
  // case fails today, and `it.fails` keeps it visible. The change that fixes the gap turns it into `it`.
  it.fails("starts a one-way item with nothing chosen, whatever its reason", () => {
    const item: Stated = { id: "q4", text: "Question 4", options: ["A", "B"], recommendation: "A", escalation_reason: "scope_change", reversibility: "one_way" };
    expect(startingDraft(item).choice).toBe("");
  });

  it("names what each answer binds, with the title of the record its scope names, or the scope alone", () => {
    const batch = midPlanBatch();
    const scoped = [{ scope: "plan:p1", scope_title: "Checkout retries" }, { scope: "wo:w2" }];
    batch.items = batch.items.map((item, index) => ({ ...item, ...scoped[index] }));
    show(batch);
    const scopeOf = (name: string) => [...screen.getByRole("group", { name }).querySelectorAll("p")].find((line) => line.textContent?.startsWith("Scope"))?.textContent;
    expect(scopeOf("Question 1")).toBe("Scope: plan:p1 · Checkout retries");
    expect(scopeOf("Question 2")).toBe("Scope: wo:w2");
    expect(scopeOf("Question 3")).toBeUndefined();
  });

  describe("an answer that replaces a decision", () => {
    const standing = { id: "pd-p17", status: "decided", statement: "Q: Which logo set? A: Monogram", lineage: [] };
    const ended = { id: "pd-old", status: "replaced", statement: "An older answer.", lineage: [] };
    const logo: QuestionItem = {
      id: "q-logo",
      text: "Which logo set should the site use?",
      options: ["Monogram", "Wordmark"],
      recommendation: "Wordmark",
      escalation_reason: "requirement_conflict",
    };
    const batchOf = (item: QuestionItem): Batch => ({ batch: { id: "batch-1", status: "delivered", opened_at: 1, closes_at: 2, delivered_at: 2 }, items: [item] });
    const serveDecisions = () =>
      serveApi((call) => {
        if (call.method === "GET" && call.path === "/v1/views/decisions") {
          return { status: 200, body: { decisions: [standing, ended] } };
        }
        return call.method === "POST" ? { status: 201 } : undefined;
      });
    const answers = (sent: ReturnType<typeof serveApi>) => sent.filter((call) => call.method === "POST");

    it("offers the decisions in force to replace, and sends the one a person picks", async () => {
      const sent = serveDecisions();
      show(batchOf(logo));
      await userEvent.click(screen.getByText("Replaces a decision"));
      const picker = await screen.findByRole("combobox", { name: "Replaces" });
      const options = within(picker).getAllByRole("option");
      expect(options.map((option) => option.textContent)).toEqual(["Nothing", "pd-p17: Q: Which logo set? A: Monogram"]);
      await userEvent.selectOptions(picker, "pd-p17");
      await userEvent.click(screen.getByRole("button", { name: "Send this answer" }));
      await waitFor(() => expect(answers(sent)).toHaveLength(1));
      expect(answers(sent)[0]).toMatchObject({ path: "/v1/questions/q-logo/answer", body: { answer: "Wordmark", supersedes: "pd-p17" } });
    });

    it("sends no supersedes when a person replaces nothing, and reads no decisions until asked", async () => {
      const sent = serveDecisions();
      show(batchOf(logo));
      expect(sent.some((call) => call.path === "/v1/views/decisions")).toBe(false);
      await userEvent.click(screen.getByRole("button", { name: "Send this answer" }));
      await waitFor(() => expect(answers(sent)).toHaveLength(1));
      expect(answers(sent)[0]?.body).not.toHaveProperty("supersedes");
    });

    it("sends nothing to replace after a person changes their mind", async () => {
      const sent = serveDecisions();
      show(batchOf(logo));
      await userEvent.click(screen.getByText("Replaces a decision"));
      const picker = await screen.findByRole("combobox", { name: "Replaces" });
      await userEvent.selectOptions(picker, "pd-p17");
      await userEvent.selectOptions(picker, "");
      await userEvent.click(screen.getByRole("button", { name: "Send this answer" }));
      await waitFor(() => expect(answers(sent)).toHaveLength(1));
      expect(answers(sent)[0]?.body).not.toHaveProperty("supersedes");
    });

    it("says so when a decision in force cannot be read, and still sends the answer", async () => {
      const sent = serveApi((call) => (call.method === "GET" ? { status: 500, body: { error: { code: "internal", message: "no" } } } : { status: 201 }));
      show(batchOf(logo));
      await userEvent.click(screen.getByText("Replaces a decision"));
      expect(await screen.findByText(/The decisions in force could not be read/)).toBeTruthy();
      await userEvent.click(screen.getByRole("button", { name: "Send this answer" }));
      await waitFor(() => expect(answers(sent)).toHaveLength(1));
    });
  });

  it("sends the recommendations and the one answer a person changed", async () => {
    const sent = serveApi(() => ({ status: 201 }));
    show(midPlanBatch());
    await userEvent.click(within(screen.getByRole("group", { name: "Question 7" })).getByRole("radio", { name: "B" }));
    await userEvent.click(screen.getByRole("button", { name: "Send 18 answers" }));
    await waitFor(() => expect(sent).toHaveLength(18));
    const answers = sent.map((call) => (call.body as { answer: string }).answer);
    expect(answers.filter((answer) => answer === "A")).toHaveLength(17);
    expect(answers[6]).toBe("B");
    expect(sent.map((call) => call.path)).toEqual(Array.from({ length: 18 }, (_, index) => `/v1/questions/q${index + 1}/answer`));
  });
});
