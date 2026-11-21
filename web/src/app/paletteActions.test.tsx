import { readFileSync } from "node:fs";
import { join } from "node:path";

import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { aConversation, serveTalk, talking } from "../talk/serve";
import { golden, groupHealth, serveViews } from "../testing";
import { openPage } from "../opening";
import type { PaletteAction } from "./paletteActions";
import { paletteActions } from "./paletteActions";

// Every changing route with a control has an action in the command palette (FR-UI-9). parity.test.ts holds the table
// to the routes. This opens the palette where each action stands, and looks for it, as a member would.

// A project that is archived, beside the one that is active, which Restore and Remove belong to.
const projects = {
  status: 200,
  body: {
    projects: [
      { id: "default", name: "default", state: "active", created_at: 1, repositories: [] },
      { id: "old", name: "old", state: "archived", created_at: 1, repositories: [] },
    ],
  },
};

// The search's records, with a claim to release on one running work order and a lane to start on another, which no
// golden holds.
function search() {
  const found = golden<{ records: { id: string; kind: string; actions?: string[] }[] }>("search");
  const more: Record<string, string[]> = { "wo-running": ["cancel", "release"], "wo-effect": ["cancel", "start_lane"] };
  return { ...found, records: found.records.map((record) => (record.kind === "work_order" && more[record.id] ? { ...record, actions: more[record.id] } : record)) };
}

const unique = [...new Map(Object.values(paletteActions).map((action) => [`${action.at} ${action.title} ${action.query ?? ""}`, action])).values()];
const wanted = unique.filter((action) => !action.elsewhere);

describe("the command palette", () => {
  it.each(unique.filter((action) => action.elsewhere).map((action) => [action.title.trim(), action] as const))("lists %s, which a test of its own opens", (_title, action) => {
    const source = readFileSync(join(import.meta.dirname, "..", action.elsewhere ?? ""), "utf8");
    expect(source).toContain(action.title);
  });

  // The actions that stand on one page are looked for in one visit, each typed in turn. A conversation's actions stand
  // where its surface does, with a session running, or stopped for Resume.
  const grouped = new Map<string, PaletteAction[]>();
  for (const action of wanted) {
    const visit = `${action.at}${action.title.startsWith("Resume") ? " with the session stopped" : ""}${action.group ? " in group mode" : ""}`;
    grouped.set(visit, [...(grouped.get(visit) ?? []), action]);
  }
  const visits = [...grouped];

  it.each(visits)("lists, at %s, each of its actions", async (_name, actions) => {
    const first = actions[0];
    if (first?.at.endsWith("/conversations")) {
      const state = talking();
      if (first.title.startsWith("Resume")) {
        state.conversations = [aConversation({ actual_state: "stopped", desired_state: "stopped" })];
      }
      serveTalk(state);
    } else {
      serveViews(
        (call) => {
          if (call.path.startsWith("/v1/views/search")) {
            return { status: 200, body: search() };
          }
          return first?.group && call.method === "GET" && call.path === "/v1/health" ? { status: 200, body: groupHealth } : undefined;
        },
        { projects },
      );
    }
    openPage(first?.at ?? "/needs-you");
    await screen.findByRole("heading", { level: 1 });
    await userEvent.keyboard("{Control>}k{/Control}");
    // Needs you has an Order select of its own, which is a combobox too.
    const field = await screen.findByRole("combobox", { name: "Search views, records, and actions" });
    for (const action of actions) {
      await userEvent.clear(field);
      await userEvent.type(field, action.query ?? ">");
      await waitFor(
        () => {
          const titles = screen.queryAllByRole("option").map((option) => option.textContent ?? "");
          expect(titles.some((text) => text.includes(action.title)), `an action that starts with "${action.title}" among ${titles.length} listed`).toBe(true);
        },
        { timeout: 4000 },
      );
    }
    cleanup();
  }, 60_000);
});
