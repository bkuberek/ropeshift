import { describe, expect, it } from "vitest";

import type { KeyInput } from "./keys";
import { idle, readKey, shortcutList } from "./keys";

const press = (key: string, more: Partial<KeyInput> = {}): KeyInput => ({ key, metaKey: false, ctrlKey: false, altKey: false, inField: false, mac: true, ...more });
const on = { singleKeys: true, now: 1000 };

describe("the keyboard", () => {
  it("opens the palette with Mod+K, even in a text field", () => {
    expect(readKey(press("k", { metaKey: true, inField: true }), idle, on).action).toEqual({ type: "palette" });
    expect(readKey(press("K", { ctrlKey: true, mac: false }), idle, on).action).toEqual({ type: "palette" });
  });

  it("reads Mod as Command on a Mac and Control elsewhere", () => {
    expect(readKey(press("k", { ctrlKey: true, mac: true }), idle, on).action).toBeNull();
    expect(readKey(press("k", { metaKey: true, mac: false }), idle, on).action).toBeNull();
  });

  it("opens the palette with a slash and the sheet with a question mark", () => {
    expect(readKey(press("/"), idle, on).action).toEqual({ type: "palette" });
    expect(readKey(press("?"), idle, on).action).toEqual({ type: "sheet" });
  });

  it("goes to a view with G then its key", () => {
    const first = readKey(press("g"), idle, on);
    expect(first.action).toBeNull();
    expect(first.handled).toBe(true);
    expect(readKey(press("f"), first.state, { ...on, now: 1500 }).action).toEqual({ type: "go", view: "floor" });
    expect(readKey(press("N", { shiftKey: true }), first.state, { ...on, now: 1500 }).action).toEqual({ type: "go", view: "needs-you" });
  });

  it("forgets a G after a pause, and after a key that goes nowhere", () => {
    const first = readKey(press("g"), idle, on);
    expect(readKey(press("f"), first.state, { ...on, now: 5000 }).action).toBeNull();
    const nowhere = readKey(press("z"), first.state, { ...on, now: 1200 });
    expect(nowhere.action).toBeNull();
    expect(readKey(press("f"), nowhere.state, { ...on, now: 1300 }).action).toBeNull();
  });

  it.each([
    ["p", "projects"],
    [".", "dock"],
    ["j", "next"],
    ["k", "previous"],
    ["a", "primary"],
  ])("%s is %s", (key, type) => {
    expect(readKey(press(key), idle, on).action).toEqual({ type });
  });

  it("decides nothing with one key: A only moves focus", () => {
    expect(readKey(press("a"), idle, on).action).toEqual({ type: "primary" });
    expect(readKey(press("Enter"), idle, on).action).toBeNull();
  });

  it("takes no single key while a person types in a field", () => {
    for (const key of ["/", "?", "g", "p", ".", "j", "k", "a"]) {
      expect(readKey(press(key, { inField: true }), idle, on).action, key).toBeNull();
    }
  });

  it("takes no single key when a person turned them off, and still opens the palette with Mod+K", () => {
    const off = { singleKeys: false, now: 1000 };
    for (const key of ["/", "?", "g", "p", ".", "j", "k", "a"]) {
      expect(readKey(press(key), idle, off).action, key).toBeNull();
    }
    expect(readKey(press("k", { metaKey: true }), idle, off).action).toEqual({ type: "palette" });
  });

  it("leaves keys with a modifier to the browser", () => {
    expect(readKey(press("j", { altKey: true }), idle, on).action).toBeNull();
    expect(readKey(press("r", { metaKey: true }), idle, on).handled).toBe(false);
  });
});

describe("the shortcut sheet", () => {
  it("lists every shortcut the keyboard reads, with the Mac's Mod and another's", () => {
    const mac = shortcutList(true).flatMap((group) => group.rows.map((row) => row.keys.join(" ")));
    expect(mac).toContain("⌘ K");
    expect(mac).toContain("G F");
    const other = shortcutList(false).flatMap((group) => group.rows.map((row) => row.keys.join(" ")));
    expect(other).toContain("Ctrl K");
  });
});
