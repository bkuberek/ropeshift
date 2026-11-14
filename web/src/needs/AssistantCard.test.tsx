import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { Approval, NeedItem, NeedsYou as View } from "../api/types";
import { openPage } from "../opening";
import { serveTalk, talking } from "../talk/serve";
import { FakeEvents, golden } from "../testing";

type CardItem = Extract<NeedItem, { kind: "confirm" }>;

// The cards a delegate prepared, with the first as the assistant's, from a conversation.
function assistantCards(deleted = false): View {
  const view = golden<View>("needs-you-cards");
  const first = view.items.find((item): item is CardItem => item.kind === "confirm");
  if (first) {
    first.card.label = "assistant";
    first.card.delegate_kind = "assistant";
    first.card.conversation = deleted ? { id: "conv-1", name: "", deleted: true } : { id: "conv-1", name: "harbor-web merges" };
  }
  return view;
}

const firstCard = (view: View) => view.items.find((item): item is CardItem => item.kind === "confirm") as CardItem;

describe("a card an outside delegate called the assistant prepared", () => {
  it("never shows the assistant's chip", async () => {
    const view = assistantCards();
    const card = firstCard(view);
    card.card.delegate_kind = "external";
    delete card.card.conversation;
    serveTalk({ ...talking(), answers: { "GET /v1/needs-you": () => ({ status: 200, body: view }) } });
    openPage(`/needs-you?kind=cards&item=confirm%3A${card.id}`);
    const article = await screen.findByRole("article", { name: card.card.approval.summary });
    expect(article.querySelector(".target-chip")).toBeNull();
    expect(article.querySelector(".decision-provenance")?.textContent).toContain("Prepared by assistant for member-1");
  });
});

describe("a card the assistant prepared, in Needs you", () => {
  it("says the assistant prepared it, for whom, and in which conversation", async () => {
    const view = assistantCards();
    const card = firstCard(view);
    const state = talking();
    serveTalk({ ...state, answers: { "GET /v1/needs-you": () => ({ status: 200, body: view }) } });
    openPage(`/needs-you?kind=cards&item=confirm%3A${card.id}`);
    const article = await screen.findByRole("article", { name: card.card.approval.summary });
    expect(article.querySelector(".decision-provenance .target-chip.t-assistant")?.textContent).toBe("Assistant");
    expect(article.querySelector(".decision-provenance")?.textContent).toMatch(/^Prepared by Assistant for member-1 in “harbor-web merges” · /);
    expect(article.textContent).not.toContain("Prepared by assistant");
  });

  it("names a deleted conversation as one", async () => {
    const view = assistantCards(true);
    const card = firstCard(view);
    serveTalk({ ...talking(), answers: { "GET /v1/needs-you": () => ({ status: 200, body: view }) } });
    openPage(`/needs-you?kind=cards&item=confirm%3A${card.id}`);
    const article = await screen.findByRole("article", { name: card.card.approval.summary });
    expect(article.querySelector(".decision-provenance")?.textContent).toContain("in a deleted conversation");
    expect(within(article).queryByRole("button", { name: "Open conversation" })).toBeNull();
  });

  it("opens its conversation in the dock, with the cursor in the message box", async () => {
    const view = assistantCards();
    const card = firstCard(view);
    serveTalk({ ...talking(), answers: { "GET /v1/needs-you": () => ({ status: 200, body: view }) } });
    openPage(`/needs-you?kind=cards&item=confirm%3A${card.id}`);
    const article = await screen.findByRole("article", { name: card.card.approval.summary });
    await userEvent.click(within(article).getByRole("button", { name: "Open conversation" }));
    const dock = await screen.findByRole("complementary", { name: "Conversation" });
    expect(await within(dock).findByRole("log", { name: "Messages with the assistant" })).toBeTruthy();
    await waitFor(() => expect(document.activeElement).toBe(within(dock).getByRole("textbox", { name: "Message to the assistant" })));
  });

  it("offers a void one the assistant again, in the conversation's composer, and sends nothing", async () => {
    const view = assistantCards();
    const card = firstCard(view);
    let listed = view;
    const sent = serveTalk({
      ...talking(),
      answers: {
        "GET /v1/needs-you": () => ({ status: 200, body: listed }),
        [`GET /v1/approvals/${card.card.approval.id}`]: () => ({ status: 200, body: { ...card.card.approval, state: "void" } satisfies Approval }),
      },
    });
    openPage(`/needs-you?kind=cards&item=confirm%3A${card.id}`);
    await screen.findByRole("article", { name: card.card.approval.summary });
    // The page hears of a change on its stream, which an effect opens after the render that shows the card, so
    // the change waits for that stream: an event sent before it opens reaches nobody.
    const stream = await waitFor(() => {
      const open = FakeEvents.opened.filter((opened) => !opened.closed);
      expect(open).toHaveLength(1);
      return open[0] as FakeEvents;
    });
    // The server stops listing a card that went void, and the page keeps it where it stood, with how it came out.
    listed = { ...view, items: view.items.filter((item) => item.id !== card.id) };
    stream.emit();
    const ask = await screen.findByRole("button", { name: "Ask the assistant again" });
    await userEvent.click(ask);
    const dock = await screen.findByRole("complementary", { name: "Conversation" });
    const box = (await within(dock).findByRole("textbox", { name: "Message to the assistant" })) as HTMLTextAreaElement;
    expect(box.value).toBe(`The card "${card.card.approval.summary}" went void because its subject changed. Prepare it again if it still applies.`);
    await waitFor(() => expect(document.activeElement).toBe(box));
    expect(sent.filter((call) => call.method !== "GET")).toEqual([]);
  });
});
