import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { DelegateList, OperationList } from "../api/delegates";
import { openPage } from "../opening";
import type { Sent } from "../testing";
import { golden, here, refusal, serveViews } from "../testing";

type Answer = { status: number; body?: unknown };

// serveDelegates answers what the members, settings, and audit pages read about delegates, each from what a
// Go test wrote from a real ledger, and lets a test answer anything else first.
function serveDelegates(answer?: (sent: Sent) => Answer | undefined) {
  return serveViews((call) => {
    const own = answer?.(call);
    if (own) {
      return own;
    }
    if (call.method !== "GET") {
      return undefined;
    }
    const reads: [string, () => unknown][] = [
      ["/v1/delegates", () => golden("delegates")],
      ["/v1/operations", () => golden("operation-classes")],
      ["/v1/policies?kind=delegate", () => golden("policies-delegate")],
      ["/v1/members", () => golden("members")],
      ["/v1/audit", () => golden("audit-delegates")],
    ];
    const found = reads.find(([prefix]) => call.path === prefix || call.path.startsWith(`${prefix}?`));
    return found ? { status: 200, body: found[1]() } : undefined;
  });
}

const sentTo = (sent: Sent[], method: string, path: string) => sent.filter((call) => call.method === method && call.path.split("?")[0] === path);

describe("Delegates in Members", () => {
  it("lists each delegate with its project, expiry, last use, and state, and offers what each state allows", async () => {
    serveDelegates();
    openPage("/members");
    const table = await screen.findByRole("region", { name: "Delegate credentials" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(3);
    const [used, revoked, never] = rows as [HTMLElement, HTMLElement, HTMLElement];
    expect(revoked.textContent).toContain("planning assistant");
    expect(within(revoked).getByText("Revoked")).toBeTruthy();
    expect(within(revoked).getByRole("button", { name: /Remove from list/ })).toBeTruthy();
    expect(within(revoked).queryByRole("button", { name: /Revoke/ })).toBeNull();
    expect(never.textContent).toContain("release notes bot");
    expect(within(never).getByText("never used").closest(".unavailable")).not.toBeNull();
    expect(within(never).getByText("Active")).toBeTruthy();
    expect(within(used).getByRole("button", { name: /Revoke/ })).toBeTruthy();
    expect(used.textContent).toContain("default");
    expect(screen.getByText("Signing out of every session revokes every delegate.")).toBeTruthy();
  });

  it("creates a delegate and shows its credential once", async () => {
    const sent = serveDelegates((call) =>
      call.method === "POST" && call.path === "/v1/delegates"
        ? { status: 201, body: { ...golden<DelegateList>("delegates").delegates[0], token: "a-delegate-token", label: "ci helper" } }
        : undefined,
    );
    openPage("/members");
    await userEvent.click(await screen.findByRole("button", { name: "Create delegate" }));
    const dialog = await screen.findByRole("dialog", { name: "Create delegate" });
    await userEvent.type(within(dialog).getByLabelText("Label"), "ci helper");
    await userEvent.selectOptions(within(dialog).getByLabelText("Expires in"), "7 days");
    await userEvent.click(within(dialog).getByRole("button", { name: "Create" }));
    await waitFor(() => expect(sentTo(sent, "POST", "/v1/delegates")).toHaveLength(1));
    expect(sentTo(sent, "POST", "/v1/delegates")[0]?.body).toEqual({ project: "default", label: "ci helper", expires_in_ms: 7 * 24 * 3_600_000 });
    const shown = await screen.findByRole("dialog", { name: "Delegate created" });
    expect(within(shown).getByText("export ROPESHIFT_DELEGATE_TOKEN=a-delegate-token")).toBeTruthy();
    expect(within(shown).getByText("This credential is shown once. Store it now; ropeshift keeps only its digest.")).toBeTruthy();
    const copy = vi.fn(async () => {});
    Object.defineProperty(navigator, "clipboard", { value: { writeText: copy }, configurable: true });
    await userEvent.click(within(shown).getByRole("button", { name: "Copy" }));
    expect(copy).toHaveBeenCalledWith("export ROPESHIFT_DELEGATE_TOKEN=a-delegate-token");
  });

  it("revokes an active delegate and removes one that no longer acts", async () => {
    const list = golden<DelegateList>("delegates");
    const sent = serveDelegates((call) => (call.method === "POST" ? { status: call.path.endsWith("/remove") ? 204 : 200, body: call.path.endsWith("/remove") ? undefined : list.delegates[0] } : undefined));
    openPage("/members");
    const table = await screen.findByRole("region", { name: "Delegate credentials" });
    const rows = within(table).getAllByRole("row").slice(1);
    await userEvent.click(within(rows[0] as HTMLElement).getByRole("button", { name: /Revoke/ }));
    await waitFor(() => expect(sentTo(sent, "POST", `/v1/delegates/${list.delegates[0]?.id}/revoke`)).toHaveLength(1));
    await userEvent.click(within(rows[1] as HTMLElement).getByRole("button", { name: /Remove from list/ }));
    await waitFor(() => expect(sentTo(sent, "POST", `/v1/delegates/${list.delegates[1]?.id}/remove`)).toHaveLength(1));
  });

  it("says so when the member has no delegate, and offers to create one", async () => {
    serveDelegates((call) => (call.method === "GET" && call.path === "/v1/delegates" ? { status: 200, body: { as_of: 1, delegates: [] } } : undefined));
    openPage("/members");
    const empty = await screen.findByText("No delegates");
    expect(within(empty.closest(".empty") as HTMLElement).getByRole("button", { name: "Create delegate" })).toBeTruthy();
  });

  it("signs out of every session after a confirmation that names what it revokes", async () => {
    const sent = serveDelegates((call) => (call.method === "POST" && call.path === "/v1/sessions/end-all" ? { status: 200, body: { sessions: 2, delegates: ["x"] } } : undefined));
    openPage("/members");
    await userEvent.click(await screen.findByRole("button", { name: "Sign out all sessions" }));
    const dialog = await screen.findByRole("dialog", { name: "Sign out all sessions" });
    expect(dialog.textContent).toContain("revokes every delegate credential of yours");
    await userEvent.click(within(dialog).getByRole("button", { name: "Sign out everywhere" }));
    await waitFor(() => expect(sentTo(sent, "POST", "/v1/sessions/end-all")).toHaveLength(1));
  });
});

describe("Delegate policy in Settings", () => {
  it("lists every consent operation, disables one no policy may exempt, and saves a version", async () => {
    const sent = serveDelegates((call) => (call.method === "POST" && call.path === "/v1/policies" ? { status: 201, body: { version: 3 } } : undefined));
    openPage(here("/settings?section=delegate-policy"));
    expect(await screen.findByRole("heading", { name: "Delegate policy" })).toBeTruthy();
    expect(screen.getByText("Consent operations a delegate may run without a card. Only a member changes this policy.")).toBeTruthy();
    const consent = golden<OperationList>("operation-classes").operations.filter((op) => op.class === "consent");
    const field = await screen.findByRole("group", { name: /Consent operations a delegate runs without a card/ });
    expect(await within(field).findAllByRole("checkbox")).toHaveLength(consent.length);
    const accept = within(field).getByRole("checkbox", { name: /Accept work/ });
    expect((accept as HTMLInputElement).checked).toBe(true);
    const roster = within(field).getByRole("checkbox", { name: /Change the agent roster/ });
    expect((roster as HTMLInputElement).disabled).toBe(true);
    expect(roster.closest("label")?.parentElement?.textContent).toContain("ADR-0025");
    await userEvent.click(within(field).getByRole("checkbox", { name: /Cancel work/ }));
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 3" }));
    await waitFor(() => expect(sentTo(sent, "POST", "/v1/policies")).toHaveLength(1));
    expect((sentTo(sent, "POST", "/v1/policies")[0]?.body as { body: string }).body).toBe('[delegate_policy]\nversion = 3\nexempt = ["work_order.accept", "work_order.cancel"]\n');
  });

  it("shows the API's refusal at the field", async () => {
    serveDelegates((call) => (call.method === "POST" && call.path === "/v1/policies" ? refusal(400, "invalid", "the delegate policy cannot exempt work_order.cancel is a member's alone") : undefined));
    openPage(here("/settings?section=delegate-policy"));
    const field = await screen.findByRole("group", { name: /Consent operations a delegate runs without a card/ });
    await userEvent.click(await within(field).findByRole("checkbox", { name: /Cancel work/ }));
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Save version 3" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Keep editing" }));
    expect((await within(field).findByRole("alert")).textContent).toContain("cannot exempt work_order.cancel");
  });
});

describe("Delegate actors in the audit", () => {
  it("names a delegate by its key, its label, and the member it acted for, in the table and the detail, and filters by it", async () => {
    const sent = serveDelegates();
    openPage(here("/audit"));
    const table = await screen.findByRole("region", { name: "Events" });
    const row = within(table).getAllByRole("row")[1] as HTMLElement;
    const actor = row.querySelector('[data-label="Actor"]');
    expect(actor?.textContent).toBe("planning assistantfor member-1");
    expect(actor?.querySelector("svg")).not.toBeNull();
    await userEvent.click(within(row).getByRole("button", { name: "21" }));
    expect((await screen.findByRole("heading", { name: "Event 21" })).closest(".panel")?.textContent).toContain("planning assistantfor member-1");
    await userEvent.selectOptions(screen.getByLabelText("Actor"), "delegate");
    await userEvent.click(screen.getByRole("button", { name: "Filter" }));
    await waitFor(() => expect(sent.some((call) => call.path.startsWith("/v1/audit?") && call.path.includes("actor=delegate"))).toBe(true));
  });
});

describe("the command palette", () => {
  it("creates a delegate and signs out of every session from its actions", async () => {
    serveDelegates();
    openPage("/members");
    await screen.findByRole("heading", { level: 1, name: "Members" });
    await userEvent.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Command palette" });
    await userEvent.keyboard(">delegate");
    expect(within(palette).getByRole("option", { name: /Create delegate/ })).toBeTruthy();
    await userEvent.clear(within(palette).getByRole("combobox"));
    await userEvent.keyboard(">sign out all");
    await userEvent.click(within(palette).getByRole("option", { name: /Sign out all sessions/ }));
    expect(await screen.findByRole("dialog", { name: "Sign out all sessions" })).toBeTruthy();
  });
});

describe("an outside delegate called the assistant, in the audit", () => {
  it("shows its key and label, never the assistant's chip", async () => {
    const audit = golden<{ events: { actor_kind: string; actor_label?: string; delegate_kind?: string }[] }>("audit-delegates");
    const row = audit.events.find((event) => event.actor_kind === "delegate");
    if (row) {
      row.actor_label = "assistant";
      row.delegate_kind = "external";
    }
    serveDelegates((call) => (call.method === "GET" && call.path.startsWith("/v1/audit?") ? { status: 200, body: audit } : undefined));
    openPage(here("/audit?actor=delegate"));
    const table = await screen.findByRole("region", { name: "Events" });
    const actor = (within(table).getAllByRole("row")[1] as HTMLElement).querySelector('[data-label="Actor"]');
    expect(actor?.querySelector(".target-chip")).toBeNull();
    expect(actor?.querySelector("svg")).not.toBeNull();
    expect(actor?.textContent).toContain("assistant");
  });
});
