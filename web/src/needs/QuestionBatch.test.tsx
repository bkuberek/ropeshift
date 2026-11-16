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
