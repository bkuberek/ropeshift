import { afterEach, describe, expect, it, vi } from "vitest";

import { PaneSender } from "./sender";

const lane = "lane-1";

// held stands for the daemon, and answers each input only when the test lets it, so a test sees what waits
// behind an input on its way. An input refused answers 409 with the message given.
function held(refuse = "") {
  const sent: unknown[] = [];
  const waiting: (() => void)[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url: string, init?: RequestInit) => {
      sent.push(JSON.parse(init?.body as string));
      if (refuse) {
        return new Response(JSON.stringify({ error: { code: "no_pane", message: refuse } }), { status: 409 });
      }
      await new Promise<void>((resolve) => waiting.push(resolve));
      return new Response(null, { status: 204 });
    }),
  );
  const answer = async () => {
    await waitUntil(() => waiting.length > 0);
    waiting.shift()?.();
  };
  return { sent, answer };
}

async function waitUntil(done: () => boolean) {
  for (let tries = 0; tries < 200 && !done(); tries++) {
    await new Promise((resolve) => setTimeout(resolve, 1));
  }
  expect(done()).toBe(true);
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("what a member sends to a pane", () => {
  it("goes one input after another, in order, and keys pressed meanwhile go together", async () => {
    const { sent, answer } = held();
    const failures: string[] = [];
    const sender = new PaneSender(lane, (message) => failures.push(message));
    sender.text("yes", true);
    sender.keys("Tab");
    sender.keys("Shift-Tab");
    sender.keys("Ctrl-o");
    await answer();
    await answer();
    expect(sent).toEqual([{ text: "yes", enter: true }, { key: "Tab Shift-Tab Ctrl-o" }]);
    expect(failures).toEqual([]);
  });

  it("sends no more chords at once than a sequence holds", async () => {
    const { sent, answer } = held();
    const sender = new PaneSender(lane, () => undefined);
    sender.text("a", false);
    for (let at = 0; at < 10; at++) {
      sender.keys("Down");
    }
    await answer();
    await answer();
    await answer();
    expect(sent).toEqual([{ text: "a", enter: false }, { key: Array(8).fill("Down").join(" ") }, { key: "Down Down" }]);
  });

  it("says why an input was refused, and drops what waited behind it", async () => {
    const { sent } = held("lane lane-1 has no pane");
    const failures: string[] = [];
    const sender = new PaneSender(lane, (message) => failures.push(message));
    sender.keys("Enter");
    sender.text("after", true);
    await waitUntil(() => failures.length === 1);
    await new Promise((resolve) => setTimeout(resolve, 5));
    expect(failures).toEqual(["lane lane-1 has no pane"]);
    expect(sent).toEqual([{ key: "Enter" }]);
  });
});
