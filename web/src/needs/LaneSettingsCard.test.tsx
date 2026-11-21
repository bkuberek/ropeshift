import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { LaneSettingsNeed, NeedItem, WorkOrder } from "../api/types";
import { CurrentProject } from "../app/route";
import { SettingsHeld } from "../lanes/SettingsHeld";
import { serveApi, testProject } from "../testing";
import { cardKindOf, cardKinds, contextOf, filterOf, headingOf, kindOf, settingsWaiting } from "./kinds";
import { LaneSettingsCard } from "./LaneSettingsCard";

const held: LaneSettingsNeed = {
  lane: "lane-1",
  work_order: { id: "wo-1", title: "Add the retry hook", state: "parked" } as WorkOrder,
  category: "security",
  change: {
    generation: 2,
    files: [".claude/settings.json"],
    keys: ["hooks", "permissions"],
    summary: [".claude/settings.json hooks.PermissionRequest: added", '.claude/settings.json permissions.allow: added "Bash(curl *)"'],
    digest: "sha256:held",
  },
  recommendation: "base",
  recommendation_rationale: "the lane wrote what its next session would run outside every rule ropeshift gives it",
  actions: ["settings_base", "settings_lane", "settings_stop"],
};

// scriptHeld is a lane held because it changed a script a hook of the repository runs, which no restore puts back.
const scriptHeld: LaneSettingsNeed = {
  ...held,
  change: {
    generation: 2,
    files: ["scripts/check.sh"],
    keys: ["hooks"],
    scripts: ["scripts/check.sh"],
    summary: ["scripts/check.sh: changed, and a hook in .claude/settings.json runs it"],
    digest: "sha256:script",
  },
  recommendation: "stop",
  recommendation_rationale: "the lane changed a script a hook runs, and a restore would revert its work",
  actions: ["settings_lane", "settings_stop"],
};

function show(after: () => Promise<void> = async () => {}, need: LaneSettingsNeed = held) {
  return render(
    <CurrentProject.Provider value={testProject}>
      <LaneSettingsCard need={need} after={after} frame={{ title: "Lane lane-1 changed its own hooks/permissions" }} />
    </CurrentProject.Provider>,
  );
}

describe("a lane held on its own settings", () => {
  it("is a security decision of its own kind, titled by the lane", () => {
    const item: NeedItem = { kind: "lane_settings", id: "lane-1", since: 0, lane_settings: held };
    expect(cardKinds).toContain("lane-settings");
    expect(cardKindOf(item)).toBe("lane-settings");
    expect(filterOf(item)).toBe("decisions");
    expect(kindOf(item).name).toBe("Security: lane settings");
    expect(headingOf(item)).toBe("Lane lane-1 changed its own hooks/permissions");
    expect(contextOf(item)).toBe("wo-1 · security · hooks, permissions");
  });

  it("says what it waits on, the keys, and each change, with the three choices", () => {
    show();
    expect(screen.getByText(/^Waiting on a member: this lane changed its own hooks\/permissions\./)).toBeTruthy();
    expect(screen.getByText("hooks, permissions")).toBeTruthy();
    expect(screen.getByText("Security, which only a member decides")).toBeTruthy();
    const changed = within(screen.getByRole("list", { name: "What changed" }))
      .getAllByRole("listitem")
      .map((item) => item.textContent);
    expect(changed).toEqual(held.change.summary);
    const choices = within(screen.getByRole("list", { name: "Choices" }))
      .getAllByRole("listitem")
      .map((item) => item.textContent ?? "");
    expect(choices[0]).toMatch(/^Run with the repository's settings \(base\): /);
    expect(choices[1]).toMatch(/^Run with this lane's settings: .*your decision/);
    expect(choices[2]).toMatch(/^Stop: /);
    expect(screen.getByText(/Recommended: Run with the repository's settings \(base\), because/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Run with the repository's settings (base)" }).className).toMatch(/primary/);
  });

  it.each([
    ["Run with the repository's settings (base)", "base"],
    ["Run with this lane's settings", "lane"],
    ["Stop", "stop"],
  ])("sends %s as the choice %s, bound to the change it shows", async (press, choice) => {
    const sent = serveApi((call) => (call.method === "POST" && call.path === "/v1/work-orders/wo-1/lane-settings" ? { status: 200, body: { work_order: "wo-1", state: "ready", choice } } : undefined));
    const after = vi.fn(async () => {});
    show(after);
    await userEvent.click(screen.getByRole("button", { name: press }));
    await waitFor(() => expect(after).toHaveBeenCalled());
    expect(sent.find((call) => call.method === "POST")?.body).toEqual({ lane: "lane-1", digest: "sha256:held", choice });
  });
});

describe("a lane held on a script a hook runs", () => {
  it("offers no restore, says why, and keeps the lane's version and stop", () => {
    show(async () => {}, scriptHeld);
    expect(screen.queryByRole("button", { name: "Run with the repository's settings (base)" })).toBeNull();
    expect(screen.getByRole("button", { name: "Run with this lane's settings" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Stop" })).toBeTruthy();
    const choices = within(screen.getByRole("list", { name: "Choices" }))
      .getAllByRole("listitem")
      .map((item) => item.textContent ?? "");
    expect(choices).toHaveLength(2);
    expect(choices.some((choice) => choice.startsWith("Run with the repository's settings"))).toBe(false);
    expect(screen.getByText(/No restore is offered: the lane changed scripts\/check\.sh, which a hook or a setting of the repository runs/)).toBeTruthy();
    expect(screen.getByText(/Recommended: Stop, because/)).toBeTruthy();
  });

  it("offers the restore for a hold from a settings file", () => {
    show();
    expect(screen.getByRole("button", { name: "Run with the repository's settings (base)" })).toBeTruthy();
    expect(screen.queryByText(/No restore is offered/)).toBeNull();
  });
});

describe("a held lane's waiting text", () => {
  it("names what it waits on and where to choose, and says nothing of a lane held on none", () => {
    const { container } = render(<SettingsHeld lane="lane-1" hold={held.change} />);
    expect(screen.getByRole("status").textContent).toBe(`${settingsWaiting} (hooks, permissions). Choose in Needs you`);
    expect(screen.getByRole("link", { name: "Choose in Needs you" }).getAttribute("href")).toMatch(/needs-you\?item=lane_settings%3Alane-1/);
    container.remove();
    const empty = render(<SettingsHeld lane="lane-2" />);
    expect(empty.container.textContent).toBe("");
  });
});
