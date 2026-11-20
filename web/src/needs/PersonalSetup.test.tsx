import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { Lane, WorkNeed, WorkOrder } from "../api/types";
import { CurrentProject } from "../app/route";
import { testProject } from "../testing";
import { kindOf } from "./kinds";
import { WorkCard } from "./WorkCard";

const lane = {
  id: "lane-7",
  work_order: "wo-7",
  generation: 1,
  actual_state: "working",
  liveness: "alive",
  model_reach: "reachable",
  ledger_reach: "reachable",
  as_of: Date.now(),
  freshness: "observed",
  reported_by_epoch: 1,
} as Lane;

const leak: WorkNeed = {
  reason: "personal_setup",
  detail: "Your own MCP server github (user) and agent reviewer (~/.claude/agents/reviewer.md) loaded in lane lane-7.",
  lane,
  work_order: { id: "wo-7", title: "Leaky work", state: "running" } as WorkOrder,
  personal: [
    { kind: "mcp_server", name: "github", from: "user" },
    { kind: "agent", name: "reviewer", from: "~/.claude/agents/reviewer.md" },
  ],
  recommendation: "ignore",
  recommendation_rationale: "the lane's rules and hooks still hold every call it makes, but an MCP server or a plugin of yours can reach outside the lane",
  actions: [],
};

function show(work: WorkNeed) {
  return render(
    <CurrentProject.Provider value={testProject}>
      <WorkCard id="lane-7" work={work} after={async () => {}} frame={{ title: "Leaky work" }} />
    </CurrentProject.Provider>,
  );
}

describe("a lane that loaded the member's own setup", () => {
  it("names what leaked and in which lane, with the choices and the recommended one", () => {
    show(leak);
    expect(kindOf({ kind: "work", id: "lane-7", since: 0, work: leak }).name).toBe("Your CLI setup in a lane");
    expect(screen.getByText(/loaded in lane lane-7/)).toBeTruthy();
    const lines = within(screen.getByRole("list", { name: "Choices" }))
      .getAllByRole("listitem")
      .map((item) => item.textContent ?? "");
    expect(lines[0]).toMatch(/^Ignore it this time: leave the lane running\. This card goes when its session ends\./);
    expect(lines[1]).toMatch(/^Open the setting: /);
    expect(lines[2]).toMatch(/^Stop the lane: only Cancel this work stops a running lane, and it ends the work for good\./);
    expect(screen.getByText(/Recommended: ignore it this time and open the setting, because the lane's rules/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "Open the setting" }).getAttribute("href")).toMatch(/settings-and-agents\.md#your-own-agent-cli-setup$/);
    expect(screen.getByRole("link", { name: "Cancel this work on work order wo-7" }).getAttribute("href")).toMatch(/\/work-order\?id=wo-7/);
  });

  it("offers no button: leaving the lane running is ignoring it, and cancelling the work is too much for a leak", () => {
    show(leak);
    expect(screen.queryAllByRole("button")).toEqual([]);
  });
});
