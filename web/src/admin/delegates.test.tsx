import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { PolicyList, PolicyVersion } from "../api/admin";
import type { DelegateList, OperationList } from "../api/delegates";
import { openPage } from "../opening";
import type { Sent } from "../testing";
import { golden, here, refusal, serveViews } from "../testing";

type Answer = { status: number; body?: unknown };

// serveDelegates answers what the members, settings, and audit pages read about delegates, each from what a
// Go test wrote from a real ledger, and lets a test answer anything else first, and the project registry.
function serveDelegates(answer?: (sent: Sent) => Answer | undefined, options: { projects?: Answer } = {}) {
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
  }, options);
}

// staleDelegatePolicy is the delegate policy a Go test wrote, with a newest version that still exempts an
// operation no policy may exempt any more, as a version published before the list narrowed does.
function staleDelegatePolicy(): PolicyList {
  const stored = golden<PolicyList>("policies-delegate");
  const [newest, ...older] = stored.versions as [PolicyVersion, ...PolicyVersion[]];
  return {
    ...stored,
    versions: [{ ...newest, body: '[delegate_policy]\nversion = 2\nexempt = ["work_order.accept", "work_order.cancel"]\n', settings: '{"version":2,"exempt":["work_order.accept","work_order.cancel"]}' }, ...older],
  };
}

// leaveGuarded says the page asks before it closes, as it does while a form holds unsaved changes.
function leaveGuarded(): boolean {
  const leaving = new Event("beforeunload", { cancelable: true });
  window.dispatchEvent(leaving);
  return leaving.defaultPrevented;
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

  it("says how far off each expiry and how long ago each use is, by the server's clock", async () => {
    serveDelegates();
    openPage("/members");
    const members = await screen.findByRole("region", { name: "Members" });
    const member = within(members).getAllByRole("row")[1] as HTMLElement;
    expect(member.querySelector('[data-label="Next to expire"] [aria-hidden="true"]')?.textContent).toBe("in 1h");
    expect(member.querySelector('[data-label="Last sign-in"] [aria-hidden="true"]')?.textContent).toBe("just now");
    const delegates = await screen.findByRole("region", { name: "Delegate credentials" });
    const [used, , never] = within(delegates).getAllByRole("row").slice(1) as [HTMLElement, HTMLElement, HTMLElement];
    expect(used.querySelector('[data-label="Expires"] [aria-hidden="true"]')?.textContent).toBe("in 7h 57m");
    expect(used.querySelector('[data-label="Last use"] [aria-hidden="true"]')?.textContent).toBe("1m ago");
    expect(never.querySelector('[data-label="Expires"] [aria-hidden="true"]')?.textContent).toBe("in 6d 23h");
  });

  it("says how long a new delegate acts, from the moment the server made it", async () => {
    serveDelegates((call) =>
      call.method === "POST" && call.path === "/v1/delegates" ? { status: 201, body: { ...golden<DelegateList>("delegates").delegates[0], token: "a-delegate-token" } } : undefined,
    );
    openPage("/members");
    await userEvent.click(await screen.findByRole("button", { name: "Create delegate" }));
    const dialog = await screen.findByRole("dialog", { name: "Create delegate" });
    await userEvent.type(within(dialog).getByLabelText("Label"), "planning assistant");
    await userEvent.click(within(dialog).getByRole("button", { name: "Create" }));
    const shown = await screen.findByRole("dialog", { name: "Delegate created" });
    expect(within(shown).getByText(/acts in project default until/).querySelector('[aria-hidden="true"]')?.textContent).toBe("in 8h");
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
    expect(screen.getByText("Consent operations a delegate may run without a card, and whether a delegate's lane start may raise the model and effort. Only a member changes this policy.")).toBeTruthy();
    const consent = golden<OperationList>("operation-classes").operations.filter((op) => op.class === "consent");
    const field = await screen.findByRole("group", { name: /Consent operations a delegate runs without a card/ });
    expect(await within(field).findAllByRole("checkbox")).toHaveLength(consent.length);
    const accept = within(field).getByRole("checkbox", { name: /Accept work/ });
    expect((accept as HTMLInputElement).checked).toBe(true);
    const roster = within(field).getByRole("checkbox", { name: /Change the agent roster/ });
    expect((roster as HTMLInputElement).disabled).toBe(true);
    expect(roster.closest("label")?.parentElement?.textContent).toContain("ADR-0025");
    const cancel = within(field).getByRole("checkbox", { name: /Cancel work/ });
    expect((cancel as HTMLInputElement).disabled).toBe(true);
    expect(cancel.closest("label")?.parentElement?.textContent).toContain("exempts only accepting verified work");
    await userEvent.click(within(field).getByRole("checkbox", { name: /work_order\.rework$/ }));
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 3" }));
    await waitFor(() => expect(sentTo(sent, "POST", "/v1/policies")).toHaveLength(1));
    expect((sentTo(sent, "POST", "/v1/policies")[0]?.body as { body: string }).body).toBe('[delegate_policy]\nversion = 3\nexempt = ["work_order.accept", "work_order.rework"]\nraise_model_and_effort = true\n');
  });

  it("drops an operation the stored version lists that no policy may exempt any more, and says which", async () => {
    const stale = staleDelegatePolicy();
    const sent = serveDelegates((call) => {
      if (call.method === "GET" && call.path.split("?")[0] === "/v1/policies") {
        return { status: 200, body: stale };
      }
      return call.method === "POST" && call.path === "/v1/policies" ? { status: 201, body: { version: 3 } } : undefined;
    });
    openPage(here("/settings?section=delegate-policy"));
    const field = await screen.findByRole("group", { name: /Consent operations a delegate runs without a card/ });
    const cancel = await within(field).findByRole("checkbox", { name: /Cancel work/ });
    await waitFor(() => expect((cancel as HTMLInputElement).checked).toBe(false));
    expect(within(field).getByText(/no policy may exempt any more/).textContent).toContain("work_order.cancel");
    expect((within(field).getByRole("checkbox", { name: /Accept work/ }) as HTMLInputElement).checked).toBe(true);
    // What exempts nothing is no change of the person's, so nothing waits to be saved until they make one.
    expect(screen.getByText("No unsaved changes")).toBeTruthy();
    await userEvent.click(within(field).getByRole("checkbox", { name: /work_order\.rework$/ }));
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Save version 3" }));
    await waitFor(() => expect(sentTo(sent, "POST", "/v1/policies")).toHaveLength(1));
    expect((sentTo(sent, "POST", "/v1/policies")[0]?.body as { body: string }).body).toBe('[delegate_policy]\nversion = 3\nexempt = ["work_order.accept", "work_order.rework"]\nraise_model_and_effort = true\n');
  });

  it("discards back to the stored version without what it dropped, and leaves nothing unsaved", async () => {
    const stale = staleDelegatePolicy();
    serveDelegates((call) => (call.method === "GET" && call.path.split("?")[0] === "/v1/policies" ? { status: 200, body: stale } : undefined));
    openPage(here("/settings?section=delegate-policy"));
    const field = await screen.findByRole("group", { name: /Consent operations a delegate runs without a card/ });
    const cancel = await within(field).findByRole("checkbox", { name: /Cancel work/ });
    await waitFor(() => expect((cancel as HTMLInputElement).checked).toBe(false));
    const rework = within(field).getByRole("checkbox", { name: /work_order\.rework$/ });
    await userEvent.click(rework);
    expect(screen.queryByText("No unsaved changes")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Discard" }));
    expect(screen.getByText("No unsaved changes")).toBeTruthy();
    expect((rework as HTMLInputElement).checked).toBe(false);
    expect((cancel as HTMLInputElement).checked).toBe(false);
    expect((within(field).getByRole("checkbox", { name: /Accept work/ }) as HTMLInputElement).checked).toBe(true);
    expect(within(field).getByText(/no policy may exempt any more/).textContent).toContain("work_order.cancel");
    expect(leaveGuarded()).toBe(false);
  });

  it("leaves an archived project's stored version as it stands, with nothing unsaved", async () => {
    const stale = staleDelegatePolicy();
    serveDelegates((call) => (call.method === "GET" && call.path.split("?")[0] === "/v1/policies" ? { status: 200, body: stale } : undefined), {
      projects: { status: 200, body: { projects: [{ id: "default", name: "default", state: "archived", created_at: 1, repositories: [] }] } },
    });
    openPage(here("/settings?section=delegate-policy"));
    expect(await screen.findByText(/is archived and read-only/)).toBeTruthy();
    const field = await screen.findByRole("group", { name: /Consent operations a delegate runs without a card/ });
    const cancel = await within(field).findByRole("checkbox", { name: /Cancel work/ });
    expect((cancel as HTMLInputElement).disabled).toBe(true);
    expect((cancel as HTMLInputElement).checked).toBe(true);
    expect(within(field).queryByText(/no policy may exempt any more/)).toBeNull();
    expect(screen.getByText("No unsaved changes")).toBeTruthy();
    expect(leaveGuarded()).toBe(false);
  });

  it("shows the API's refusal at the field", async () => {
    serveDelegates((call) => (call.method === "POST" && call.path === "/v1/policies" ? refusal(400, "invalid", "the delegate policy cannot exempt work_order.rework is no operation") : undefined));
    openPage(here("/settings?section=delegate-policy"));
    const field = await screen.findByRole("group", { name: /Consent operations a delegate runs without a card/ });
    await userEvent.click(await within(field).findByRole("checkbox", { name: /work_order\.rework$/ }));
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Save version 3" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Keep editing" }));
    expect((await within(field).findByRole("alert")).textContent).toContain("cannot exempt work_order.rework");
  });

  it("allows a delegate's raise of the model and effort when the version leaves it out, and saves a version that refuses it", async () => {
    const sent = serveDelegates((call) => (call.method === "POST" && call.path === "/v1/policies" ? { status: 201, body: { version: 3 } } : undefined));
    openPage(here("/settings?section=delegate-policy"));
    const raise = (await screen.findByRole("checkbox", { name: /lane start may raise the model and effort/ })) as HTMLInputElement;
    expect(raise.checked).toBe(true);
    expect(raise.closest(".field")?.textContent).toContain("raise_model_and_effort");
    expect(screen.getByText("No unsaved changes")).toBeTruthy();
    await userEvent.click(raise);
    expect(raise.checked).toBe(false);
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Save version 3" }));
    await waitFor(() => expect(sentTo(sent, "POST", "/v1/policies")).toHaveLength(1));
    expect((sentTo(sent, "POST", "/v1/policies")[0]?.body as { body: string }).body).toBe('[delegate_policy]\nversion = 3\nexempt = ["work_order.accept"]\nraise_model_and_effort = false\n');
  });

  it("shows a version that refuses a delegate's raise as it stands", async () => {
    const stored = golden<PolicyList>("policies-delegate");
    const [newest, ...older] = stored.versions as [PolicyVersion, ...PolicyVersion[]];
    const refusing: PolicyList = {
      ...stored,
      versions: [{ ...newest, body: '[delegate_policy]\nversion = 2\nexempt = []\nraise_model_and_effort = false\n', settings: '{"version":2,"exempt":[],"raise_model_and_effort":false}' }, ...older],
    };
    serveDelegates((call) => (call.method === "GET" && call.path.split("?")[0] === "/v1/policies" ? { status: 200, body: refusing } : undefined));
    openPage(here("/settings?section=delegate-policy"));
    const raise = (await screen.findByRole("checkbox", { name: /lane start may raise the model and effort/ })) as HTMLInputElement;
    expect(raise.checked).toBe(false);
    expect(screen.getByText("No unsaved changes")).toBeTruthy();
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
