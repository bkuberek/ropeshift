// @vitest-environment node
import type { Locator, Page } from "playwright-core";
import { describe, expect, it } from "vitest";

import { Keys } from "./keyboardTesting";

// A walk waits for its control to render before the first Tab, and a control that never renders fails by that name,
// not as a control Tab could not reach. These run with no browser, on stand-ins for the page and the control.

function stand(attach: () => Promise<void>) {
  const events: string[] = [];
  const control = {
    first: () => ({ waitFor: async () => { events.push("waiting"); await attach(); events.push("attached"); } }),
    evaluateAll: async () => { events.push("focused?"); return true; },
    toString: () => "getByRole('button', { name: 'Revoke' })",
  } as unknown as Locator;
  const page = {
    evaluate: async () => "/members",
    keyboard: { press: async (key: string) => { events.push(`press ${key}`); } },
  } as unknown as Page;
  return { events, control, keys: new Keys(page) };
}

describe("waiting for a control to render", () => {
  it("comes before the first check and the first Tab", async () => {
    const { events, control, keys } = stand(async () => {});
    await keys.tabTo(control);
    expect(events).toEqual(["waiting", "attached", "focused?"]);
  });

  it("fails by name and address, and presses no Tab, when the control never renders", async () => {
    const { events, control, keys } = stand(async () => {
      throw new Error("Timeout 20000ms exceeded");
    });
    await expect(keys.tabTo(control)).rejects.toThrow("The control getByRole('button', { name: 'Revoke' }) never rendered on /members");
    expect(events.filter((event) => event.startsWith("press"))).toEqual([]);
  });
});
