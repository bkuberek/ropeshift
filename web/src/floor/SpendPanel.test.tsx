import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { WorkOrderSpend } from "../api/types";
import { CurrentProject } from "../app/route";
import { refusal, serveApi, testProject } from "../testing";
import { SpendPanel } from "./SpendPanel";

const live = { heard: 0, checkSession: async () => {} };

const spend = (over: Partial<WorkOrderSpend> = {}, charged = 8): WorkOrderSpend => ({
  work_order: "wo-1",
  kind: "change",
  charged: { recorded_usd: charged, estimated_usd: 0, estimated_turns: 0, charged_usd: charged },
  label: "estimated, at list price, with 0 turns estimated by average",
  soft_limit: 12.5,
  hard_limit: 25,
  basis: "no_history",
  remaining: 25 - charged,
  policy_version: 1,
  ...over,
});

async function show(read: WorkOrderSpend) {
  const sent = serveApi(() => ({ status: 200, body: read }));
  render(
    <CurrentProject.Provider value={testProject}>
      <SpendPanel id="wo-1" live={live} />
    </CurrentProject.Provider>,
  );
  const panel = await screen.findByRole("region", { name: "Spend" });
  await within(panel).findByText("Charged");
  return { panel, sent };
}

const facts = (panel: HTMLElement) => panel.querySelector("dl")?.textContent ?? "";

describe("a work order's spend against its limits", () => {
  it("reads the work order's own spend route, as the command does", async () => {
    const { sent } = await show(spend());
    expect(sent.map((call) => `${call.method} ${call.path}`)).toEqual(["GET /v1/work-orders/wo-1/spend"]);
  });

  it("is under its soft limit: the charge, both limits, what remains, and where the limits came from", async () => {
    const { panel } = await show(spend());
    expect(facts(panel)).toBe(
      "Charged$8.00 estimated, at list price, with 0 turns estimated by averageSoft limit$12.50Hard limit$25.00Remaining$17.00Limits fromthe limit for its size, because it has no trusted cost band",
    );
    const meter = within(panel).getByRole("meter", { name: "Charged $8.00 of a hard limit of $25.00" });
    expect(meter.getAttribute("max")).toBe("25");
    expect(meter.getAttribute("value")).toBe("8");
    expect(panel.textContent).not.toContain("soft limit, which warns");
  });

  it("is past its soft limit, which warns and stops nothing", async () => {
    const { panel } = await show(spend({ conditions: ["over_estimate"], remaining: 10 }, 15));
    expect(panel.textContent).toContain("It is past its soft limit, which warns and stops nothing.");
    expect(facts(panel)).toContain("Remaining$10.00");
  });

  it("is at its hard limit, and a meter that never reads past it", async () => {
    const { panel } = await show(spend({ conditions: ["over_estimate", "spend_limit"], remaining: 0 }, 30));
    expect(panel.textContent).toContain("It is at its hard limit, so its lane waits for a raise or a person.");
    expect(within(panel).getByRole("meter").getAttribute("value")).toBe("25");
    expect(facts(panel)).toContain("Charged$30.00");
  });

  it("says a member raised the hard limit", async () => {
    const { panel } = await show(spend({ hard_limit: 50, raised_to: 50, remaining: 42 }));
    expect(facts(panel)).toContain("Hard limit$50.00 (raised by a member)");
  });

  it("has no limits when the policy leaves none, and shows only what is charged", async () => {
    const { panel } = await show(spend({ soft_limit: 0, hard_limit: 0, remaining: 0, basis: undefined }));
    expect(within(panel).queryByRole("meter")).toBeNull();
    expect(facts(panel)).toBe("Charged$8.00 estimated, at list price, with 0 turns estimated by averageSoft limitnoneHard limitnone");
  });

  it("says why it cannot read, in the server's words", async () => {
    serveApi(() => refusal(404, "not_found", "work order wo-1 not found"));
    render(
      <CurrentProject.Provider value={testProject}>
        <SpendPanel id="wo-1" live={live} />
      </CurrentProject.Provider>,
    );
    expect((await screen.findByText(/work order wo-1 not found/)).textContent).toContain("not found");
  });
});
