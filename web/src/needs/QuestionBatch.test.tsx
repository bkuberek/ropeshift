import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { Batch, QuestionItem } from "../api/types";
import { CurrentProject } from "../app/route";
import { refusal, serveApi, testProject } from "../testing";
import { QuestionBatch, startingDraft } from "./QuestionBatch";

// The batch of the mid-plan scenario: 18 questions, each with two options and the first as the
// recommendation, which an item states the reversibility of. The first three reach a one-way decision through
// their reason and the fourth through its category, and the item of each states it, as ADR-0030 gives it.
function midPlanBatch(): Batch {
  const items: QuestionItem[] = Array.from({ length: 18 }, (_, index) => ({
    id: `q${index + 1}`,
    text: `Question ${index + 1}`,
    options: ["A", "B"],
    recommendation: "A",
    recommendation_rationale: "A keeps the plan's direction.",
    escalation_reason: index < 3 ? "one_way" : "scope_change",
    reversibility: index < 4 ? "one_way" : "reversible",
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
  // A question that reaches a one-way decision through its category states it as reversibility, and starts with
  // nothing chosen, as one that reaches it through its reason does (G6).
  it("starts a one-way item with nothing chosen, whatever its reason", () => {
    const item: QuestionItem = { id: "q4", text: "Question 4", options: ["A", "B"], recommendation: "A", escalation_reason: "scope_change", reversibility: "one_way" };
    expect(startingDraft(item).choice).toBe("");
    expect(startingDraft({ ...item, escalation_reason: "one_way", reversibility: undefined }).choice).toBe("");
    expect(startingDraft({ ...item, reversibility: "reversible" }).choice).toBe("A");
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
      expect(answers(sent)[0]).toMatchObject({
        path: "/v1/batches/batch-1/answers",
        body: { answers: [{ question: "q-logo", id: "answer-q-logo", accepted: true, supersedes: "pd-p17" }] },
      });
    });

    it("sends no supersedes when a person replaces nothing, and reads no decisions until asked", async () => {
      const sent = serveDecisions();
      show(batchOf(logo));
      expect(sent.some((call) => call.path === "/v1/views/decisions")).toBe(false);
      await userEvent.click(screen.getByRole("button", { name: "Send this answer" }));
      await waitFor(() => expect(answers(sent)).toHaveLength(1));
      expect((answers(sent)[0]?.body as { answers: object[] }).answers[0]).not.toHaveProperty("supersedes");
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
      expect((answers(sent)[0]?.body as { answers: object[] }).answers[0]).not.toHaveProperty("supersedes");
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

  type Sent = { question: string; id: string; accepted?: boolean; answer?: string; rationale?: string };
  const sentAnswers = (sent: ReturnType<typeof serveApi>) => (sent[0]?.body as { answers: Sent[] }).answers;
  // The recommended option's label ends with its tag, which its name reads.
  const radio = (question: string, name: string) => within(screen.getByRole("group", { name: question })).getByRole("radio", { name: new RegExp(`^${name}( Recommended)?$`) });

  it("selects the reversible items only, and says how many one-way items wait for a choice", async () => {
    show(midPlanBatch());
    for (const reversible of [5, 6, 18]) {
      expect((radio(`Question ${reversible}`, "A") as HTMLInputElement).checked).toBe(true);
    }
    for (const oneWay of [1, 2, 3, 4]) {
      const group = screen.getByRole("group", { name: `Question ${oneWay}` });
      expect(within(group).getAllByRole("radio").filter((choice) => (choice as HTMLInputElement).checked)).toHaveLength(0);
      expect(within(group).getByText("One-way: choose for yourself")).toBeTruthy();
    }
    expect(screen.getByRole("status").textContent).toBe("4 one-way questions wait for your own choice, and are not sent until you choose.");
    expect(screen.getByRole("button", { name: "Send 14 of 18 answers" })).toBeTruthy();
  });

  it("sends every ready answer in one request, and the one-way items wait for it", async () => {
    const sent = serveApi(() => ({ status: 201, body: { decisions: [] } }));
    show(midPlanBatch());
    await userEvent.click(screen.getByRole("button", { name: "Send 14 of 18 answers" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({ method: "POST", path: "/v1/batches/batch-1/answers" });
    const answers = sentAnswers(sent);
    expect(answers).toHaveLength(14);
    expect(answers.map((answer) => answer.question)).toEqual(Array.from({ length: 14 }, (_, index) => `q${index + 5}`));
    expect(answers.every((answer) => answer.accepted === true && answer.answer === undefined && answer.rationale === undefined)).toBe(true);
    expect(answers[0]?.id).toBe("answer-q5");
  });

  it("sends a one-way item once a person chooses it, and an answer of their own with their reason", async () => {
    const sent = serveApi(() => ({ status: 201, body: { decisions: [] } }));
    show(midPlanBatch());
    await userEvent.click(radio("Question 2", "A"));
    await userEvent.click(radio("Question 7", "B"));
    expect(screen.getByRole("button", { name: "Send 14 of 18 answers" })).toBeTruthy();
    expect(screen.getByText("Say why, or this answer is not sent.")).toBeTruthy();
    await userEvent.type(screen.getByRole("textbox", { name: "Why" }), "B fits the schema.");
    expect(screen.getByRole("status").textContent).toBe("3 one-way questions wait for your own choice, and are not sent until you choose.");
    await userEvent.click(screen.getByRole("button", { name: "Send 15 of 18 answers" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    const answers = sentAnswers(sent);
    expect(answers).toHaveLength(15);
    expect(answers.find((answer) => answer.question === "q2")).toMatchObject({ accepted: true });
    expect(answers.find((answer) => answer.question === "q7")).toMatchObject({ answer: "B", rationale: "B fits the schema." });
    expect(answers.find((answer) => answer.question === "q7")).not.toHaveProperty("accepted");
  });

  it("holds back an answer of a person's own that says no why, and counts the rest", async () => {
    const sent = serveApi(() => ({ status: 201, body: { decisions: [] } }));
    show(midPlanBatch());
    await userEvent.click(radio("Question 9", "B"));
    expect(screen.getByRole("button", { name: "Send 13 of 18 answers" })).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Send 13 of 18 answers" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sentAnswers(sent).map((answer) => answer.question)).not.toContain("q9");
  });

  it("shows the refusal that names the first answer, and sends one request for it", async () => {
    const sent = serveApi(() => refusal(400, "invalid", "answer 3, for question q7: a member's own answer states its rationale"));
    show(midPlanBatch());
    await userEvent.click(screen.getByRole("button", { name: "Send 14 of 18 answers" }));
    expect(await screen.findByText(/answer 3, for question q7/)).toBeTruthy();
    expect(sent).toHaveLength(1);
  });

  it("answers a one-way item through the keyboard alone", async () => {
    const sent = serveApi(() => ({ status: 201, body: { decisions: [] } }));
    show(midPlanBatch());
    radio("Question 4", "A").focus();
    await userEvent.keyboard(" ");
    expect((radio("Question 4", "A") as HTMLInputElement).checked).toBe(true);
    const send = screen.getByRole("button", { name: "Send 15 of 18 answers" });
    send.focus();
    await userEvent.keyboard("{Enter}");
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sentAnswers(sent).find((answer) => answer.question === "q4")).toMatchObject({ accepted: true });
  });

  it("accepts every reversible recommendation and no one-way item, and says how many wait", async () => {
    show(midPlanBatch());
    await userEvent.click(radio("Question 5", "B"));
    await userEvent.click(radio("Question 2", "B"));
    expect((radio("Question 5", "A") as HTMLInputElement).checked).toBe(false);
    await userEvent.click(screen.getByRole("button", { name: "Accept all" }));
    for (const reversible of [5, 6, 18]) {
      expect((radio(`Question ${reversible}`, "A") as HTMLInputElement).checked).toBe(true);
    }
    // A one-way item a person chose stays as they chose it, and one they did not stays unchosen.
    expect((radio("Question 2", "B") as HTMLInputElement).checked).toBe(true);
    for (const oneWay of [1, 3, 4]) {
      const group = screen.getByRole("group", { name: `Question ${oneWay}` });
      expect(within(group).getAllByRole("radio").filter((choice) => (choice as HTMLInputElement).checked)).toHaveLength(0);
    }
    expect(screen.getByRole("status").textContent).toBe("3 one-way questions wait for your own choice, and are not sent until you choose.");
  });

  it("says one one-way question waits in the singular, and nothing when none does", async () => {
    const batch = midPlanBatch();
    batch.items = batch.items.slice(3, 6);
    show(batch);
    await userEvent.click(screen.getByRole("button", { name: "Accept all" }));
    expect(screen.getByRole("status").textContent).toBe("1 one-way question waits for your own choice, and is not sent until you choose.");
    await userEvent.click(radio("Question 4", "A"));
    expect(screen.queryByRole("status")).toBeNull();
    expect(screen.getByRole("button", { name: "Send 3 answers" })).toBeTruthy();
  });

  it("sends only the reversible recommendations after Accept all, and reaches the button by keyboard", async () => {
    const sent = serveApi(() => ({ status: 201, body: { decisions: [] } }));
    show(midPlanBatch());
    await userEvent.click(radio("Question 5", "B"));
    const accept = screen.getByRole("button", { name: "Accept all" });
    accept.focus();
    await userEvent.keyboard("{Enter}");
    expect((radio("Question 5", "A") as HTMLInputElement).checked).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: "Send 14 of 18 answers" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sentAnswers(sent)).toHaveLength(14);
    expect(sentAnswers(sent).every((answer) => answer.accepted === true)).toBe(true);
  });

  it("shows why a recommendation is the one to choose", () => {
    show(midPlanBatch());
    expect(within(screen.getByRole("group", { name: "Question 5" })).getByText("Why it is recommended: A keeps the plan's direction.")).toBeTruthy();
  });
});
