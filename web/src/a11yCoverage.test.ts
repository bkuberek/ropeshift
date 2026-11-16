import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";

import { describe, expect, it } from "vitest";

import { dialogStates, signInStates, views } from "./a11yStates";
import { settingsSections } from "./admin/Settings";
import { signInStateIds } from "./app/SignIn";
import { needsYouItems } from "./browserApp";
import { workOrderTabs } from "./floor/WorkOrders";
import { cardKindOf, cardKinds, keyOf } from "./needs/kinds";

// The accessibility checks hold every view to axe-core's WCAG 2.0, 2.1, and 2.2 A and AA rules in a browser. This holds
// the list of what they check to what the application has: a Settings section, a work order tab, a kind of Needs you
// card, or a dialog that no state of the checks shows fails here, so a view added without its axe state fails the build
// on any machine, with or without a browser.

const parsed = views.map((address) => new URL(address, "http://ropeshift.test"));
const root = import.meta.dirname;

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      return sources(path);
    }
    return /\.tsx?$/.test(entry.name) && !entry.name.includes(".test.") ? [path] : [];
  });
}

describe("the axe states", () => {
  it("show every Settings section", () => {
    const shown = new Set(parsed.filter((url) => url.pathname.endsWith("/settings")).map((url) => url.searchParams.get("section")));
    expect(settingsSections.filter((id) => !shown.has(id))).toEqual([]);
  });

  it("show every state of the Sign in page, which no address reaches", () => {
    expect(signInStateIds.filter((id) => !signInStates.includes(id)), "a state of the page that no check opens").toEqual([]);
    expect(signInStates.filter((id) => !signInStateIds.includes(id)), "a check of a state the page cannot show").toEqual([]);
    expect(new Set(signInStates).size).toBe(signInStates.length);
  });

  it("show every tab of a work order", () => {
    const shown = new Set(parsed.filter((url) => url.pathname.endsWith("/work-order")).map((url) => url.searchParams.get("tab") ?? "review"));
    expect(workOrderTabs.filter((id) => !shown.has(id))).toEqual([]);
  });

  it("show every kind of card in Needs you", () => {
    const served = needsYouItems();
    const named = parsed.filter((url) => url.pathname === "/needs-you" && url.searchParams.has("item")).map((url) => url.searchParams.get("item") ?? "");
    const unserved = named.filter((item) => !served.some((row) => keyOf(row) === item));
    expect(unserved, "a state that names an item the checks' Needs you does not serve").toEqual([]);
    const shown = new Set(named.map((item) => cardKindOf(served.find((row) => keyOf(row) === item)!)));
    expect(cardKinds.filter((kind) => !shown.has(kind))).toEqual([]);
  });

  it("serve no card of a kind that kinds.ts does not name", () => {
    expect(needsYouItems().filter((item) => !cardKinds.includes(cardKindOf(item))).map(keyOf)).toEqual([]);
  });

  it("show every dialog, which the shared Dialog component marks", () => {
    const used = new Map<string, number>();
    for (const path of sources(root)) {
      const count = readFileSync(path, "utf8").match(/<Dialog\b/g)?.length ?? 0;
      if (count > 0) {
        used.set(relative(root, path), count);
      }
    }
    const without = [...used].filter(([file, count]) => dialogStates.filter((row) => row.file === file).length < count).map(([file, count]) => `${file} opens ${count} dialog(s)`);
    expect(without, "a file that opens a Dialog with no axe state for it").toEqual([]);
    const stale = [...new Set(dialogStates.map((row) => row.file))].filter((file) => !used.has(file));
    expect(stale, "an axe state for a file that opens no Dialog").toEqual([]);
  });
});
