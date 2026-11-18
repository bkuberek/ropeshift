import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { PolicyList, SpendView } from "../api/admin";
import { openPage } from "../opening";
import edits from "../../testdata/spend-edits.json";
import type { Sent } from "../testing";
import { golden, here, refusal, serveViews } from "../testing";
import { policies } from "./logic";

type Answer = { status: number; body?: unknown };

// What the Spend sections read, from what a Go test wrote of a real ledger: the project's spend policy at version 2, with
// a daily limit of $10 that its lanes are past and a weekly limit of $50 they are under, the installation's at version 2,
// and the notify policy that sends a hard stop to the desktop.
function serveSpend(answer?: (sent: Sent) => Answer | undefined, spend: SpendView = golden<SpendView>("spend"), projects?: Answer) {
  return serveViews(
    (call) => {
      const own = answer?.(call);
      if (own) {
        return own;
      }
      const reads: Record<string, unknown> = {
        "/v1/spend": spend,
        "/v1/policies?kind=spend": golden("policies-spend"),
        "/v1/policies?kind=installation_spend": golden("policies-installation-spend"),
        "/v1/policies?kind=notify": golden("policies-notify"),
      };
      return call.method === "GET" && call.path in reads ? { status: 200, body: reads[call.path] } : undefined;
    },
    projects ? { projects } : {},
  );
}

const posted = (sent: Sent[]) => sent.filter((call) => call.method === "POST" && call.path === "/v1/policies");
const published = (version = 3) => ({ status: 201, body: { version } });
const bodyOf = (sent: Sent[]) => (posted(sent)[0]?.body as { body: string }).body;

// stateOf makes the spend a project reads from what each window and one work order were charged against their limits.
function stateOf(day: [spent: number, soft: number, hard: number], week: [spent: number, soft: number, hard: number], order: [spent: number, soft: number, hard: number]): SpendView {
  const view = golden<SpendView>("spend");
  const window = (kind: "day" | "week", [spent, soft, hard]: [number, number, number]) => ({ ...view.project_spend[kind], spent_usd: spent, soft_limit: soft, hard_limit: hard, remaining: hard > 0 ? Math.max(hard - spent, 0) : 0 });
  view.project_spend = { day: window("day", day), week: window("week", week) };
  const [spent, soft, hard] = order;
  view.work_orders = [{ ...view.work_orders![0]!, charged: { recorded_usd: spent, estimated_usd: 0, estimated_turns: 0, charged_usd: spent }, soft_limit: soft, hard_limit: hard, remaining: hard > 0 ? Math.max(hard - spent, 0) : 0 }];
  return view;
}

const standing = async (name: string) => {
  const table = await screen.findByRole("table", { name });
  return within(table)
    .getAllByRole("row")
    .slice(1)
    .map((row) => within(row).getAllByRole("cell").at(-1)?.textContent);
};

describe("Settings: Spend", () => {
  const open = () => openPage(here("/settings?section=spend"));

  it("is a project section, and says what the project's lanes were charged today, this week, and per open work order, against each limit", async () => {
    serveSpend();
    open();
    expect(await screen.findByRole("heading", { level: 1, name: "Spend" })).toBeTruthy();
    const tree = screen.getByRole("navigation", { name: "Settings sections" });
    expect(within(tree).getByRole("link", { name: "Spend" }).getAttribute("href")).toBe(here("/settings?section=spend"));
    const project = await screen.findByRole("table", { name: "Charged spend of the project" });
    const today = within(project).getByText("Today").closest("tr") as HTMLElement;
    expect(within(today).getByText("$12.00")).toBeTruthy();
    expect(within(today).getByText("$8.00")).toBeTruthy();
    expect(within(today).getByText("$10.00")).toBeTruthy();
    expect(within(today).getByText("Past the hard limit")).toBeTruthy();
    const week = within(project).getByText("This week").closest("tr") as HTMLElement;
    expect(within(week).getByText("Under its limits")).toBeTruthy();
    const orders = await screen.findByRole("table", { name: "Charged spend of each open work order" });
    const order = within(orders).getByText("wo-spend").closest("tr") as HTMLElement;
    // Its hard limit, and what remains of it.
    expect(within(order).getAllByText("$25.00")).toHaveLength(2);
    expect(within(order).getByText("$12.50")).toBeTruthy();
    expect(order.textContent).toContain("estimated, at list price, with 0 turns estimated by average");
  });

  it.each([
    ["has no limit set", [0, 0, 0], [0, 0, 0], [0, 0, 0], ["No limit set", "No limit set"], ["No limit set"]],
    ["is under its soft limits", [3, 8, 10], [3, 40, 50], [5, 12.5, 25], ["Under its limits", "Under its limits"], ["Under its limits"]],
    ["is past a soft limit and under the hard one", [9, 8, 10], [3, 40, 50], [13, 12.5, 25], ["Past the soft limit", "Under its limits"], ["Past the soft limit"]],
    ["has just reached its limits", [8, 8, 10], [40, 40, 50], [12.5, 12.5, 25], ["Past the soft limit", "Past the soft limit"], ["Past the soft limit"]],
    ["has just reached its hard limits", [10, 8, 10], [50, 40, 50], [25, 12.5, 25], ["Past the hard limit", "Past the hard limit"], ["Past the hard limit"]],
    ["is past a hard limit", [12, 8, 10], [60, 40, 50], [26, 12.5, 25], ["Past the hard limit", "Past the hard limit"], ["Past the hard limit"]],
  ] as const)("says where a project that %s stands, for the day, the week, and a work order", async (_, day, week, order, windows, orders) => {
    serveSpend(undefined, stateOf([...day], [...week], [...order]));
    open();
    expect(await standing("Charged spend of the project")).toEqual(windows);
    expect(await standing("Charged spend of each open work order")).toEqual(orders);
  });

  it("says a limit nobody set is none, and a hard limit a member raised, and what it was raised to", async () => {
    const view = stateOf([0, 0, 0], [3, 40, 50], [1, 12.5, 25]);
    view.project_spend.week.raised_to = 50;
    serveSpend(undefined, view);
    open();
    const project = await screen.findByRole("table", { name: "Charged spend of the project" });
    const today = within(project).getByText("Today").closest("tr") as HTMLElement;
    expect(within(today).getAllByText("none").length).toBeGreaterThanOrEqual(2);
    expect(within(project).getByText("raised by a member to $50.00")).toBeTruthy();
  });

  it("lists no work order when none is open", async () => {
    const view = golden<SpendView>("spend");
    view.work_orders = null;
    serveSpend(undefined, view);
    open();
    expect(await screen.findByText("No open work order has a spend limit")).toBeTruthy();
  });

  it("shows every setting of the spend policy, with the value in force, and the version that holds them", async () => {
    serveSpend();
    open();
    expect(((await screen.findByLabelText(/^Project daily limit/)) as HTMLInputElement).value).toBe("10");
    expect((screen.getByLabelText(/^Project weekly limit/) as HTMLInputElement).value).toBe("50");
    expect((screen.getByLabelText(/^Longest live wait/) as HTMLInputElement).value).toBe("5m");
    expect((screen.getByLabelText(/^Soft limit sits at/) as HTMLSelectElement).value).toBe("p85");
    expect((screen.getByRole("checkbox", { name: /Turn off W4/ }) as HTMLInputElement).checked).toBe(false);
    expect(screen.getByText(/Version 2, by member-1/)).toBeTruthy();
    // Every key the policy states has a control, and a heading says what group it is in. The four wait rules are four
    // checkboxes of one key.
    const form = screen.getByRole("tabpanel");
    const groups = [...new Set(policies.spend.fields.map((field) => field.group))];
    for (const group of groups) {
      expect(within(form).getByRole("heading", { level: 2, name: group })).toBeTruthy();
    }
    expect(form.querySelectorAll("input, select, textarea").length).toBe(policies.spend.fields.length + 3);
  });

  it("publishes a version with the keys a maintainer changed, as the same body `ropeshift spend set` makes", async () => {
    const sent = serveSpend((call) => (call.method === "POST" && call.path === "/v1/policies" ? published() : undefined));
    open();
    fireEvent.change(await screen.findByLabelText(/^Project daily limit/), { target: { value: "30" } });
    fireEvent.change(screen.getByLabelText(/^Share of recent spend that waiting may reach/), { target: { value: "0.4" } });
    expect(screen.getByText("project_daily_limit 10 → 30, waiting_alert_share 0.5 → 0.4")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    const dialog = await screen.findByRole("dialog", { name: "Review the spend change" });
    const diff = within(dialog).getByRole("table", { name: "Policy change" });
    expect(within(diff).getByText("project_daily_limit = 30.0")).toBeTruthy();
    expect(within(diff).getByText("project_daily_limit = 10.0")).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 3" }));
    await waitFor(() => expect(posted(sent)).toHaveLength(1));
    expect(bodyOf(sent)).toBe(edits.spend.body);
    expect(posted(sent)[0]?.csrf).not.toBeNull();
  });

  it("reads the charged spend again once a version is saved, because the limits follow it at once", async () => {
    const sent = serveSpend((call) => (call.method === "POST" && call.path === "/v1/policies" ? published() : undefined));
    open();
    fireEvent.change(await screen.findByLabelText(/^Project daily limit/), { target: { value: "30" } });
    const reads = () => sent.filter((call) => call.method === "GET" && call.path === "/v1/spend").length;
    await screen.findByRole("table", { name: "Charged spend of the project" });
    const before = reads();
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    await userEvent.click(within(await screen.findByRole("dialog", { name: "Review the spend change" })).getByRole("button", { name: "Save version 3" }));
    await waitFor(() => expect(reads()).toBeGreaterThan(before));
  });

  it("writes the wait rules a member turned off, and the command prefixes a rule also matches, one to a line", async () => {
    const sent = serveSpend((call) => (call.method === "POST" && call.path === "/v1/policies" ? published() : undefined));
    open();
    await userEvent.click(await screen.findByRole("checkbox", { name: /Turn off W4/ }));
    await userEvent.type(screen.getByLabelText(/^Command prefixes rule W3 also matches/), "kubectl rollout status{Enter}helm status");
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    await userEvent.click(within(await screen.findByRole("dialog", { name: "Review the spend change" })).getByRole("button", { name: "Save version 3" }));
    await waitFor(() => expect(posted(sent)).toHaveLength(1));
    expect(bodyOf(sent)).toContain('wait_rules_off = ["W4"]\n');
    expect(bodyOf(sent)).toContain('wait_rule_patterns = { W1 = [], W2 = [], W3 = ["kubectl rollout status", "helm status"], W4 = [] }\n');
  });

  it("holds Review change back for a value the API would refuse, and names the range at the field", async () => {
    const sent = serveSpend();
    open();
    const review = () => screen.getByRole("button", { name: "Review change" }) as HTMLButtonElement;
    fireEvent.change(await screen.findByLabelText(/^Project daily limit/), { target: { value: "-5" } });
    expect(screen.getByText("Enter an amount in dollars from 0 to 1,000,000. 0 is none.")).toBeTruthy();
    expect(review().disabled).toBe(true);
    fireEvent.change(screen.getByLabelText(/^Project daily limit/), { target: { value: "5" } });
    fireEvent.change(screen.getByLabelText(/^Longest live wait/), { target: { value: "30s" } });
    expect(screen.getByText("Enter a duration from 1m to 1h, such as 5m.")).toBeTruthy();
    expect(review().disabled).toBe(true);
    fireEvent.change(screen.getByLabelText(/^Longest live wait/), { target: { value: "10m" } });
    expect(review().disabled).toBe(false);
    expect(posted(sent)).toHaveLength(0);
  });

  it("shows the API's reason when it refuses a publish, in the review, and keeps the draft", async () => {
    serveSpend((call) => (call.method === "POST" && call.path === "/v1/policies" ? refusal(400, "invalid", "invalid spend policy: hard_multiplier is 2, and the policy refuses it") : undefined));
    open();
    fireEvent.change(await screen.findByLabelText(/^Project daily limit/), { target: { value: "30" } });
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    const dialog = await screen.findByRole("dialog", { name: "Review the spend change" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 3" }));
    expect((await within(dialog).findByRole("alert")).textContent).toContain("the policy refuses it");
    await userEvent.click(within(dialog).getByRole("button", { name: "Keep editing" }));
    expect((screen.getByLabelText(/^Project daily limit/) as HTMLInputElement).value).toBe("30");
    expect((await screen.findAllByRole("alert")).some((alert) => alert.textContent?.includes("the policy refuses it"))).toBe(true);
  });

  it("reads only for a member the API refuses as forbidden, and says why", async () => {
    serveSpend((call) => (call.method === "POST" && call.path === "/v1/policies" ? refusal(403, "forbidden", "a maintainer changes the spend policy") : undefined));
    open();
    fireEvent.change(await screen.findByLabelText(/^Project daily limit/), { target: { value: "30" } });
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    await userEvent.click(within(await screen.findByRole("dialog", { name: "Review the spend change" })).getByRole("button", { name: "Save version 3" }));
    await userEvent.click(await screen.findByRole("button", { name: "Keep editing" }));
    expect(screen.getByText(/you may read this policy, and not publish a version of it/)).toBeTruthy();
    expect((screen.getByLabelText(/^Project daily limit/) as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Review change" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText(/a maintainer changes the spend policy/)).toBeTruthy();
  });

  it("lists every version with its difference, and reverts to the built-in one by recording it as the newest", async () => {
    const sent = serveSpend((call) => (call.method === "POST" && call.path === "/v1/policies" ? published() : undefined));
    open();
    await userEvent.click(await screen.findByRole("tab", { name: /History/ }));
    expect(await screen.findByText("Version 2")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Revert" }));
    const dialog = await screen.findByRole("dialog", { name: "Revert to version 1" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 3" }));
    await waitFor(() => expect(posted(sent)).toHaveLength(1));
    const first = golden<PolicyList>("policies-spend").versions.at(-1)!;
    expect(bodyOf(sent)).toBe(first.body.replace("version = 1\n", "version = 3\n"));
  });

  it("is reached by the keyboard alone: every control of the form takes focus in the order the page reads, down to Review change", async () => {
    serveSpend();
    open();
    await screen.findByLabelText(/^Project daily limit/);
    fireEvent.change(screen.getByLabelText(/^Project daily limit/), { target: { value: "30" } });
    const main = screen.getByRole("main");
    const form = [...main.querySelectorAll<HTMLElement>("[role=tabpanel] input, [role=tabpanel] select, [role=tabpanel] textarea, [role=tabpanel] button")].filter((one) => !(one as HTMLInputElement).disabled);
    expect(form.length).toBeGreaterThan(30);
    expect(form.every((one) => one.tabIndex >= 0)).toBe(true);
    (main.querySelector("[role=tab]") as HTMLElement).focus();
    const reached = new Set<Element>();
    for (let press = 0; press < 400 && form.some((one) => !reached.has(one)); press += 1) {
      await userEvent.tab();
      reached.add(document.activeElement as Element);
    }
    expect(form.filter((one) => !reached.has(one)).map((one) => one.getAttribute("aria-label") ?? one.id)).toEqual([]);
    const review = screen.getByRole("button", { name: "Review change" });
    review.focus();
    await userEvent.keyboard("{Enter}");
    expect(await screen.findByRole("dialog", { name: "Review the spend change" })).toBeTruthy();
  });

  it("reads and shows an archived project's spend policy without a way to change it", async () => {
    serveSpend(undefined, golden<SpendView>("spend"), { status: 200, body: { projects: [{ id: "default", name: "default", state: "archived", created_at: 1, repositories: [] }] } });
    open();
    const field = (await screen.findByLabelText(/^Project daily limit/)) as HTMLInputElement;
    expect(field.disabled).toBe(true);
    expect(screen.getByText(/is archived and read-only/)).toBeTruthy();
  });
});

describe("Settings: Orchestrator spend", () => {
  const open = () => openPage(here("/settings?section=installation-spend"));

  it("is an installation section, and holds the class the orchestrator runs on and its daily warning as the policy in force states them", async () => {
    serveSpend();
    open();
    expect(await screen.findByRole("heading", { level: 1, name: "Orchestrator spend" })).toBeTruthy();
    expect(((await screen.findByLabelText(/^Capability class of the orchestrator/)) as HTMLSelectElement).value).toBe("large");
    expect((screen.getByLabelText(/^Orchestrator daily warning/) as HTMLInputElement).value).toBe("20");
    const tree = screen.getByRole("navigation", { name: "Settings sections" });
    expect(within(tree).getByRole("link", { name: "Orchestrator spend" })).toBeTruthy();
    expect(screen.queryByLabelText(/^Project daily limit/)).toBeNull();
  });

  it("publishes a version with the keys an admin changed, as the same body `ropeshift spend set --installation` makes", async () => {
    const sent = serveSpend((call) => (call.method === "POST" && call.path === "/v1/policies" ? published() : undefined));
    open();
    await userEvent.selectOptions(await screen.findByLabelText(/^Capability class of the orchestrator/), "small");
    fireEvent.change(screen.getByLabelText(/^Orchestrator daily warning/), { target: { value: "40" } });
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    const dialog = await screen.findByRole("dialog", { name: "Review the orchestrator spend change" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 3" }));
    await waitFor(() => expect(posted(sent)).toHaveLength(1));
    expect(bodyOf(sent)).toBe(edits.installation.body);
  });

  it("reads only for anyone the API refuses, and says why", async () => {
    serveSpend((call) => (call.method === "POST" && call.path === "/v1/policies" ? refusal(403, "forbidden", "only an installation admin publishes the installation's spend policy") : undefined));
    open();
    fireEvent.change(await screen.findByLabelText(/^Orchestrator daily warning/), { target: { value: "40" } });
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    await userEvent.click(within(await screen.findByRole("dialog", { name: "Review the orchestrator spend change" })).getByRole("button", { name: "Save version 3" }));
    await userEvent.click(await screen.findByRole("button", { name: "Keep editing" }));
    expect((screen.getByLabelText(/^Orchestrator daily warning/) as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByLabelText(/^Capability class of the orchestrator/) as HTMLSelectElement).disabled).toBe(true);
    expect(screen.getByText(/only an installation admin publishes the installation's spend policy/)).toBeTruthy();
  });

  it("holds an amount past a million dollars back, and names the range", async () => {
    serveSpend();
    open();
    fireEvent.change(await screen.findByLabelText(/^Orchestrator daily warning/), { target: { value: "2000000" } });
    expect(screen.getByText("Enter an amount in dollars from 0 to 1,000,000. 0 is none.")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Review change" }) as HTMLButtonElement).disabled).toBe(true);
  });
});

describe("Settings: Notifications, the channel of each spend alert", () => {
  const open = () => openPage(here("/settings?section=notify"));
  const box = (kind: RegExp, channel: "desktop" | "webhook") => screen.getByRole("checkbox", { name: new RegExp(`${kind.source}.*send to the ${channel}`) }) as HTMLInputElement;
  const hard = /was reached, and work parked/;
  const changed = /A spend setting changed/;

  it("offers each kind of spend alert on the desktop and the webhook, with the channels the policy in force chose", async () => {
    serveSpend();
    open();
    expect(await screen.findByRole("table", { name: "Channels for each spend alert" })).toBeTruthy();
    expect(box(hard, "desktop").checked).toBe(true);
    expect(box(hard, "webhook").checked).toBe(false);
    expect(box(changed, "webhook").checked).toBe(false);
    const rows = within(screen.getByRole("table", { name: "Channels for each spend alert" })).getAllByRole("row");
    expect(rows).toHaveLength(6);
    expect(within(rows[1]!).getByText("Always")).toBeTruthy();
  });

  it("publishes the choice as `[notify_policy.spend_channels]`, with the member's other settings as they were", async () => {
    const sent = serveSpend((call) => (call.method === "POST" && call.path === "/v1/policies" ? published() : undefined));
    open();
    await screen.findByRole("table", { name: "Channels for each spend alert" });
    await userEvent.click(box(hard, "webhook"));
    await userEvent.click(box(changed, "webhook"));
    expect(screen.getByText("spend_channels spend_hard_limit to desktop → spend_hard_limit to desktop and webhook, spend_policy_changed to webhook")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    const dialog = await screen.findByRole("dialog", { name: "Review the notifications change" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 3" }));
    await waitFor(() => expect(posted(sent)).toHaveLength(1));
    expect(bodyOf(sent)).toBe(edits.notify.body);
  });

  it("takes a channel out of the body when the last kind that chose it is cleared, and leaves no table behind when none is chosen", async () => {
    const sent = serveSpend((call) => (call.method === "POST" && call.path === "/v1/policies" ? published() : undefined));
    open();
    await userEvent.click(await screen.findByRole("checkbox", { name: new RegExp(`${hard.source}.*send to the desktop`) }));
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    await userEvent.click(within(await screen.findByRole("dialog", { name: "Review the notifications change" })).getByRole("button", { name: "Save version 3" }));
    await waitFor(() => expect(posted(sent)).toHaveLength(1));
    expect(bodyOf(sent)).toBe('[notify_policy]\nversion = 3\ndesktop = true\nwebhook_url = "https://hooks.example.com/ropeshift"\n');
  });

  it("offers a channel only while the policy turns it on, and refuses to save a choice of one it turned off", async () => {
    serveSpend();
    open();
    await screen.findByRole("table", { name: "Channels for each spend alert" });
    await userEvent.click(screen.getByRole("checkbox", { name: /Desktop notifications/ }));
    expect(box(/A spend limit was nearly reached/, "desktop").disabled).toBe(true);
    expect(screen.getByText("An alert is sent to the desktop, and desktop notifications are off. Turn them on, or clear the choice.")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Review change" }) as HTMLButtonElement).disabled).toBe(true);
    await userEvent.click(box(hard, "desktop"));
    expect(screen.queryByText(/An alert is sent to the desktop/)).toBeNull();
    expect((screen.getByRole("button", { name: "Review change" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("shows the API's refusal of a choice, in the review", async () => {
    serveSpend((call) => (call.method === "POST" && call.path === "/v1/policies" ? refusal(400, "invalid", "invalid notify policy: spend_channels sends spend_waiting by email, which needs a mailer this build does not have") : undefined));
    open();
    await screen.findByRole("table", { name: "Channels for each spend alert" });
    await userEvent.click(box(changed, "webhook"));
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    const dialog = await screen.findByRole("dialog", { name: "Review the notifications change" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 3" }));
    expect((await within(dialog).findByRole("alert")).textContent).toContain("needs a mailer");
  });
});
