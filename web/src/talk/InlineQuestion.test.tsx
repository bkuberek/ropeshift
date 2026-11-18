import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { TalkEntry } from "../api/talk";
import { CurrentProject } from "../app/route";
import { serveApi, testProject } from "../testing";
import { InlineItem } from "./InlineItems";

const asking = "Which key signs the release?";

// entryOf is a question a conversation holds as it stands, which reached a member in a batch.
function entryOf(held: Partial<NonNullable<TalkEntry["question"]>> = {}): TalkEntry {
  return {
    id: "question-q-key",
    at: 1,
    kind: "question",
    question: {
      question: {
        id: "q-key",
        text: asking,
        status: "needs_human",
        options: ["Rotate it", "Keep it"],
        recommendation: "Rotate it",
        recommendation_rationale: "It left the machine.",
        escalation_reason: "scope_change",
      },
      batch: "batch-7",
      ...held,
    },
  };
}

function show(entry: TalkEntry) {
  return render(
    <CurrentProject.Provider value={testProject}>
      <InlineItem entry={entry} member="ada" after={async () => {}} />
    </CurrentProject.Provider>,
  );
}

describe("a question in a conversation", () => {
  it("answers through the batch route, in the conversation, with its recommendation chosen when its answer can be taken back", async () => {
    const sent = serveApi(() => ({ status: 201, body: { decisions: [] } }));
    show(entryOf({ reversibility: "reversible" }));
    const group = screen.getByRole("group", { name: asking });
    expect((within(group).getByRole("radio", { name: /^Rotate it/ }) as HTMLInputElement).checked).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: "Send this answer" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]).toMatchObject({
      method: "POST",
      path: "/v1/batches/batch-7/answers",
      body: { answers: [{ question: "q-key", id: "answer-q-key", accepted: true }], place: "conversation" },
    });
  });

  it("starts with nothing chosen when its answer is one-way, whatever its reason says", async () => {
    show(entryOf({ reversibility: "one_way" }));
    const group = screen.getByRole("group", { name: asking });
    for (const radio of within(group).getAllByRole("radio")) {
      expect((radio as HTMLInputElement).checked).toBe(false);
    }
    expect((screen.getByRole("button", { name: "Send 0 of 1 answer" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByRole("status").textContent).toBe("1 one-way question waits for your own choice, and is not sent until you choose.");
  });
});
