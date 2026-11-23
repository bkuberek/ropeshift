import { screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { MessageList } from "../api/types";
import { openPage } from "../opening";
import { golden, here, refusal, serveViews } from "../testing";

// The Lane view lists what was sent to the lane, as `ropeshift message list <lane>` does: each message with its kind,
// the generation it is for, how far it got and why, and what the lane replied. Only a reply confirms a lane read it.

const lane = "lane-wo-1";

function serveMessages(read: () => { status: number; body?: unknown } = () => ({ status: 200, body: golden<MessageList>("lane-messages") })) {
  return serveViews((call) => (call.method === "GET" && call.path === `/v1/lanes/${lane}/messages` ? read() : undefined));
}

const cell = (row: HTMLElement, column: string) => row.querySelector<HTMLElement>(`[data-label="${column}"]`);
const flat = (element: Element | null | undefined) => (element?.textContent ?? "").replaceAll(/\s+/g, " ").trim();

async function table(): Promise<HTMLElement[]> {
  // The panel and the table that scrolls inside it share a name.
  const regions = await screen.findAllByRole("region", { name: "Messages" });
  const region = regions.find((one) => one.classList.contains("table-wrap"));
  if (!region) {
    throw new Error("no table of messages");
  }
  return waitFor(() => {
    const rows = within(region).getAllByRole("row").slice(1);
    if (rows.length === 0) {
      throw new Error("no message row");
    }
    return rows;
  });
}

const states: Record<string, string> = { queued: "Queued", at_worker: "At the worker", handed_off: "Handed off", admitted: "Admitted", replied: "Replied", refused: "Refused", expired: "Expired" };

describe("the messages on the Lane view", () => {
  const sent = golden<MessageList>("lane-messages").messages;

  it("lists what was sent to the lane, oldest first, from the route the lane's messages are read at", async () => {
    const calls = serveMessages();
    openPage(here(`/lane?id=${lane}`));
    const rows = await table();
    expect(rows.map((row) => flat(cell(row, "Message")))).toEqual(sent.map((message) => expect.stringContaining(message.id)));
    expect(calls.filter((call) => call.path === `/v1/lanes/${lane}/messages`).map((call) => call.method)).toEqual(["GET"]);
  });

  it("says each message's kind, its generation, and how far it got, with the reason a message ended where it did", async () => {
    serveMessages();
    openPage(here(`/lane?id=${lane}`));
    const rows = await table();
    sent.forEach((message, at) => {
      const row = rows[at] as HTMLElement;
      expect(flat(cell(row, "Kind")), message.id).toBe(message.kind[0]?.toUpperCase() + message.kind.slice(1));
      expect(flat(cell(row, "Generation")), message.id).toBe(String(message.to.generation));
      expect(flat(cell(row, "State")), message.id).toContain(states[message.state]);
      if (message.reason) {
        expect(flat(cell(row, "State")), message.id).toMatch(/Timed out/);
      }
    });
    expect(sent.map((message) => message.state)).toEqual(["expired", "replied", "admitted", "refused", "queued"]);
  });

  it("says in words why a message its session has no channel for was not delivered", async () => {
    const expired = sent.find((message) => message.state === "expired");
    if (!expired) {
      throw new Error("no expired message in the golden list");
    }
    serveMessages(() => ({ status: 200, body: { messages: [{ ...expired, reason: "undeliverable" }] } }));
    openPage(here(`/lane?id=${lane}`));
    const [row] = await table();
    const state = flat(cell(row as HTMLElement, "State"));
    expect(state).toContain("Expired");
    expect(state).toContain("Not delivered: its session takes no message on this agent CLI version, and the sender decides whether to send it again");
  });

  it("says in words that a session on a version that passed its checks had no channel for a message just then, and names no version", async () => {
    const expired = sent.find((message) => message.state === "expired");
    if (!expired) {
      throw new Error("no expired message in the golden list");
    }
    serveMessages(() => ({ status: 200, body: { messages: [{ ...expired, reason: "channel_unavailable" }] } }));
    openPage(here(`/lane?id=${lane}`));
    const [row] = await table();
    const state = flat(cell(row as HTMLElement, "State"));
    expect(state).toContain("Expired");
    expect(state).toContain("Not delivered: its session had no channel for it just then, because it had no socket yet or its state could not be read, and the sender decides whether to send it again");
    expect(state).not.toContain("agent CLI version");
  });

  it("shows the body of a message as text, and a lane's reply only on a message it replied to", async () => {
    serveMessages(() => ({
      status: 200,
      body: {
        messages: sent.map((message) => (message.id === "msg-queued" ? { ...message, body: "<img src=x onerror=alert(1)>" } : message)),
      },
    }));
    openPage(here(`/lane?id=${lane}`));
    const rows = await table();
    const queued = rows.find((row) => flat(row).includes("msg-queued")) as HTMLElement;
    expect(flat(cell(queued, "Body"))).toBe("<img src=x onerror=alert(1)>");
    expect(queued.querySelector("img")).toBeNull();
    for (const [at, message] of sent.entries()) {
      const reply = flat(cell(rows[at] as HTMLElement, "Reply"));
      expect(reply, message.id).toBe(message.reply ?? "");
    }
  });

  it("says nothing was sent when the lane was never written to", async () => {
    serveMessages(() => ({ status: 200, body: golden<MessageList>("lane-messages-empty") }));
    openPage(here(`/lane?id=${lane}`));
    expect(await screen.findByText("Nothing was sent to this lane")).toBeTruthy();
    expect(screen.queryAllByRole("region", { name: "Messages" }).filter((one) => one.classList.contains("table-wrap"))).toEqual([]);
  });

  it("says why when the read is refused", async () => {
    serveMessages(() => refusal(500, "internal", "the ledger could not read this lane's messages"));
    openPage(here(`/lane?id=${lane}`));
    expect(await screen.findByText(/the ledger could not read this lane's messages/)).toBeTruthy();
  });
});
