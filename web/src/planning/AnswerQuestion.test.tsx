import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { Questions } from "../api/types";
import { openPage } from "../opening";
import type { Sent } from "../testing";
import { golden, refusal, serveViews } from "../testing";

const budget = "Should the budget hold across restarts?";
const restart = "Does the plan cover a restart?";
const card = (name: string) => screen.findByRole("article", { name });
const answerButton = async (name: string) => within(await card(name)).getByRole("button", { name: `Answer ${name}` });
const changes = (sent: Sent[]) => sent.filter((call) => call.method === "POST");

// serving answers the open questions from their golden, changed as a test needs, and a change with what it says.
function serving({ view, post }: { view?: (view: Questions) => void; post?: (call: Sent) => { status: number; body?: unknown } | undefined } = {}) {
  const questions = golden<Questions>("questions");
  view?.(questions);
  return serveViews((call) => {
    if (call.method === "POST") {
      return post?.(call) ?? { status: 201, body: {} };
    }
    return call.path.startsWith("/v1/views/questions") ? { status: 200, body: questions } : undefined;
  });
}

describe("answering and dismissing from the command palette", () => {
  async function palette(typed: string) {
    openPage("/questions");
    await screen.findByRole("article", { name: budget });
    await userEvent.keyboard("{Control>}k{/Control}");
    await screen.findByRole("dialog", { name: "Command palette" });
    await userEvent.keyboard(typed);
  }

  it("opens the answer form of a question that waits, with its first choice focused, and sends nothing", async () => {
    const sent = serving();
    openPage("/questions");
    await palette("answer question budget");
    await userEvent.click(await screen.findByRole("option", { name: new RegExp(`^Answer question: ${budget}`) }));
    const group = await screen.findByRole("group", { name: budget });
    await waitFor(() => expect(group.contains(document.activeElement)).toBe(true));
    expect(changes(sent)).toEqual([]);
  });

  it("opens the dismissal of a question that waits, which asks before it sends", async () => {
    const sent = serving();
    await palette("dismiss question budget");
    await userEvent.click(await screen.findByRole("option", { name: new RegExp(`^Dismiss question: ${budget}`) }));
    expect(await screen.findByRole("dialog", { name: "Dismiss this question?" })).toBeTruthy();
    expect(changes(sent)).toEqual([]);
  });

  it("lists no answer or dismissal for a question that is closed", async () => {
    serving({
      view: (view) => {
        const last = view.questions[2];
        if (last) {
          last.status = "answered";
        }
      },
    });
    await palette("question");
    await screen.findByRole("listbox");
    expect(screen.queryByRole("option", { name: new RegExp(`^(Answer|Dismiss) question: ${restart}`) })).toBeNull();
  });
});

describe("answering from the open questions", () => {
  it("sends the recommendation, which stands chosen, as the member's answer", async () => {
    document.cookie = "ropeshift_csrf=token-from-cookie";
    const sent = serving();
    openPage("/questions");
    await userEvent.click(await answerButton(budget));
    const group = await screen.findByRole("group", { name: budget });
    expect((within(group).getByRole("radio", { name: /^Keep the budget/ }) as HTMLInputElement).checked).toBe(true);
    expect(within(group).getByText("Recommended")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Send this answer" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ method: "POST", path: "/v1/questions/question-1/answer", project: "default", csrf: "token-from-cookie" });
    // The answer is named for its question, so a second send finds the first, and it names no place: the listener it came through is recorded.
    expect(changes(sent)[0]?.body).toEqual({ id: "answer-question-1", answer: "Keep the budget" });
    expect((await screen.findAllByText("Answered the question.")).length).toBeGreaterThan(0);
    await waitFor(() => expect(screen.queryByRole("group", { name: budget })).toBeNull());
  });

  it("leaves a question whose answer is one-way with nothing chosen, whatever its reason, until the member chooses", async () => {
    const sent = serving({
      view: (view) => {
        const first = view.questions[0];
        if (first) {
          first.reversibility = "one_way";
          first.escalation_reason = "scope_change";
        }
      },
    });
    openPage("/questions");
    await userEvent.click(await answerButton(budget));
    const group = await screen.findByRole("group", { name: budget });
    for (const radio of within(group).getAllByRole("radio")) {
      expect((radio as HTMLInputElement).checked).toBe(false);
    }
    expect((screen.getByRole("button", { name: "Send this answer" }) as HTMLButtonElement).disabled).toBe(true);
    await userEvent.click(within(group).getByRole("radio", { name: /^Keep the budget/ }));
    await userEvent.click(screen.getByRole("button", { name: "Send this answer" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.body).toEqual({ id: "answer-question-1", answer: "Keep the budget" });
  });

  it("sends another option when a member chooses it", async () => {
    const sent = serving();
    openPage("/questions");
    await userEvent.click(await answerButton(budget));
    await userEvent.click(within(await screen.findByRole("group", { name: budget })).getByRole("radio", { name: "Drop it" }));
    await userEvent.click(screen.getByRole("button", { name: "Send this answer" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.body).toMatchObject({ answer: "Drop it" });
  });

  it("sends a member's own words", async () => {
    const sent = serving();
    openPage("/questions");
    await userEvent.click(await answerButton(budget));
    const group = await screen.findByRole("group", { name: budget });
    await userEvent.click(within(group).getByRole("radio", { name: "Something else" }));
    await userEvent.type(within(group).getByRole("textbox", { name: "Your answer" }), "Hold it for a day.");
    await userEvent.click(screen.getByRole("button", { name: "Send this answer" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.body).toMatchObject({ answer: "Hold it for a day." });
  });

  it("takes a question with no options in a member's own words, and holds the send until they are written", async () => {
    const sent = serving();
    openPage("/questions");
    await userEvent.click(await answerButton(restart));
    const group = await screen.findByRole("group", { name: restart });
    expect(within(group).queryAllByRole("radio")).toHaveLength(0);
    const send = screen.getByRole("button", { name: "Send this answer" });
    expect((send as HTMLButtonElement).disabled).toBe(true);
    await userEvent.type(within(group).getByRole("textbox", { name: "Your answer" }), "It does.");
    expect((send as HTMLButtonElement).disabled).toBe(false);
    await userEvent.click(send);
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ path: "/v1/questions/question-review/answer", body: { id: "answer-question-review", answer: "It does." } });
  });

  it("leaves a one-way choice for a member to make, as Needs you does", async () => {
    const sent = serving({
      view: (view) => {
        const first = view.questions[0];
        if (first) {
          first.escalation_reason = "one_way";
          first.reversibility = "one_way";
        }
      },
    });
    openPage("/questions");
    await userEvent.click(await answerButton(budget));
    const group = await screen.findByRole("group", { name: budget });
    for (const radio of within(group).getAllByRole("radio")) {
      expect((radio as HTMLInputElement).checked).toBe(false);
    }
    expect((screen.getByRole("button", { name: "Send this answer" }) as HTMLButtonElement).disabled).toBe(true);
    await userEvent.click(within(group).getByRole("radio", { name: /^Keep the budget/ }));
    await userEvent.click(screen.getByRole("button", { name: "Send this answer" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
  });

  it("shows the daemon's refusal, keeps the answer, and sends the same decision ID again", async () => {
    let calls = 0;
    const sent = serving({
      post: () => {
        calls += 1;
        return calls === 1 ? refusal(409, "resolved", "This question already has its answer.") : { status: 201, body: {} };
      },
    });
    openPage("/questions");
    await userEvent.click(await answerButton(budget));
    await screen.findByRole("group", { name: budget });
    await userEvent.click(screen.getByRole("button", { name: "Send this answer" }));
    expect((await screen.findByRole("alert")).textContent).toContain("already has its answer");
    expect((within(screen.getByRole("group", { name: budget })).getByRole("radio", { name: /^Keep the budget/ }) as HTMLInputElement).checked).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: "Send this answer" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(2));
    expect(changes(sent).map((call) => (call.body as { id: string }).id)).toEqual(["answer-question-1", "answer-question-1"]);
  });

  it("closes the form with Cancel, and sends nothing", async () => {
    const sent = serving();
    openPage("/questions");
    await userEvent.click(await answerButton(budget));
    await screen.findByRole("group", { name: budget });
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("group", { name: budget })).toBeNull();
    expect(changes(sent)).toHaveLength(0);
  });

  it("keeps an answer being written when another question is looked at", async () => {
    serving();
    openPage("/questions");
    await userEvent.click(await answerButton(budget));
    const group = await screen.findByRole("group", { name: budget });
    await userEvent.click(within(group).getByRole("radio", { name: "Something else" }));
    await userEvent.type(within(group).getByRole("textbox", { name: "Your answer" }), "Hold it");
    await userEvent.click(await answerButton(restart));
    expect((within(screen.getByRole("group", { name: budget })).getByRole("textbox", { name: "Your answer" }) as HTMLTextAreaElement).value).toBe("Hold it");
    expect(screen.getByRole("group", { name: restart })).toBeTruthy();
  });

  it("is on the page of one question too, and offers no answer in Needs you any more", async () => {
    serving();
    openPage("/questions?id=question-review");
    await userEvent.click(await answerButton(restart));
    expect(await screen.findByRole("group", { name: restart })).toBeTruthy();
    expect(screen.queryByRole("link", { name: "Answer in Needs you" })).toBeNull();
  });

  it("offers an answer, and the dismissal, on every question that waits, and neither on one that is closed", async () => {
    serving({
      view: (view) => {
        const last = view.questions[2];
        if (last) {
          last.status = "answered";
        }
      },
    });
    openPage("/questions");
    expect(within(await card(budget)).getByRole("button", { name: `Dismiss ${budget}` })).toBeTruthy();
    expect(within(await card(budget)).getByRole("button", { name: `Answer ${budget}` })).toBeTruthy();
    expect(within(await card(restart)).queryByRole("button", { name: /^(Answer|Dismiss)/ })).toBeNull();
  });
});
