import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { Overturned } from "../api/types";
import { openPage } from "../opening";
import type { Sent } from "../testing";
import { refusal, serveViews } from "../testing";

// The golden decisions: decision-3 stands, as an assumption, and decision-1 and decision-2 were replaced.
const standing = "decision-3";
const dialog = () => screen.findByRole("dialog", { name: `Overturn ${standing}` });
const changes = (sent: Sent[]) => sent.filter((call) => call.method === "POST");
const anID = expect.stringMatching(/^[0-9A-Z]{26}$/);

const result = (change: Partial<Overturned> = {}): Overturned => ({
  old: { id: standing, status: "overturned", statement: "Retry until the budget runs out." },
  new: { id: "decision-new", status: "decided", statement: "Retry twice.", supersedes: standing },
  ...change,
});

function serving(post?: (call: Sent) => { status: number; body?: unknown } | undefined) {
  return serveViews((call) => (call.method === "POST" ? (post?.(call) ?? { status: 201, body: result() }) : undefined));
}

const card = (name: string) => screen.findByRole("article", { name });
const overturnButton = async () => within(await card("Retry until the budget runs out.")).getByRole("button", { name: `Overturn ${standing}` });

describe("overturning a decision", () => {
  it("is offered on a decision in force, and on none that was replaced", async () => {
    serving();
    openPage("/decisions");
    expect(await overturnButton()).toBeTruthy();
    expect(within(await card("Retry twice.")).queryByRole("button", { name: /^Overturn/ })).toBeNull();
    expect(within(await card("Retry three times.")).queryByRole("button", { name: /^Overturn/ })).toBeNull();
  });

  it("sends what stands instead, with its reason and what it cites, and shows what the overturn made", async () => {
    document.cookie = "ropeshift_csrf=token-from-cookie";
    const sent = serving((call) =>
      call.method === "POST" ? { status: 201, body: result({ rework: ["wo-retries", "wo-docs"], noticed: ["lane-retries"] }) } : undefined,
    );
    openPage("/decisions");
    await userEvent.click(await overturnButton());
    const box = await dialog();
    expect(within(box).getByText("Retry until the budget runs out.")).toBeTruthy();
    await userEvent.type(within(box).getByRole("textbox", { name: "New statement" }), "Retry twice, and no more.");
    await userEvent.type(within(box).getByRole("textbox", { name: "Rationale" }), "The outage ended.");
    await userEvent.type(within(box).getByRole("textbox", { name: "Citations" }), "requirement:FR-RETRY-1\ndocs/retries.md");
    await userEvent.click(within(box).getByRole("button", { name: "Overturn decision" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ method: "POST", path: `/v1/decisions/${standing}/overturn`, project: "default", csrf: "token-from-cookie" });
    expect(changes(sent)[0]?.body).toEqual({ id: anID, statement: "Retry twice, and no more.", rationale: "The outage ended.", citations: ["requirement:FR-RETRY-1", "docs/retries.md"] });
    // The dialog says what it did, and links the rework it created, and the decision that took its place.
    expect(await within(box).findByRole("heading", { name: `Overturned ${standing}` })).toBeTruthy();
    expect(within(box).getByRole("link", { name: "decision-new" }).getAttribute("href")).toBe("/projects/default/decisions?id=decision-new");
    const rework = within(box).getByRole("list", { name: "Rework created" });
    expect(within(rework).getAllByRole("link").map((link) => [link.textContent, link.getAttribute("href")])).toEqual([
      ["wo-retries", "/projects/default/work-order?id=wo-retries"],
      ["wo-docs", "/projects/default/work-order?id=wo-docs"],
    ]);
    expect(within(box).getByText(/1 lane was told/)).toBeTruthy();
    expect(within(box).queryByRole("button", { name: "Overturn decision" })).toBeNull();
    await userEvent.click(within(box).getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: `Overturn ${standing}` })).toBeNull());
  });

  it("says so when no completed work rested on the decision", async () => {
    serving();
    openPage("/decisions");
    await userEvent.click(await overturnButton());
    const box = await dialog();
    await userEvent.type(within(box).getByRole("textbox", { name: "New statement" }), "Retry twice.");
    await userEvent.click(within(box).getByRole("button", { name: "Overturn decision" }));
    expect(await within(box).findByText("No completed work rested on it, so no rework was created.")).toBeTruthy();
    expect(within(box).queryByRole("list", { name: "Rework created" })).toBeNull();
  });

  it("follows a rework link to the work order, and leaves the dialog", async () => {
    serving((call) => (call.method === "POST" ? { status: 201, body: result({ rework: ["wo-retries"] }) } : undefined));
    openPage("/decisions");
    await userEvent.click(await overturnButton());
    const box = await dialog();
    await userEvent.type(within(box).getByRole("textbox", { name: "New statement" }), "Retry twice.");
    await userEvent.click(within(box).getByRole("button", { name: "Overturn decision" }));
    await userEvent.click(await within(box).findByRole("link", { name: "wo-retries" }));
    expect(window.location.pathname).toBe("/projects/default/work-order");
    expect(window.location.search).toBe("?id=wo-retries");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: `Overturn ${standing}` })).toBeNull());
  });

  it("asks for what stands instead before it sends", async () => {
    const sent = serving();
    openPage("/decisions");
    await userEvent.click(await overturnButton());
    const box = await dialog();
    await userEvent.click(within(box).getByRole("button", { name: "Overturn decision" }));
    expect(within(box).getByText("State what stands instead.")).toBeTruthy();
    expect(within(box).getByRole("textbox", { name: "New statement" }).getAttribute("aria-invalid")).toBe("true");
    expect(changes(sent)).toHaveLength(0);
  });

  it("refuses an ID plan approval names its decisions by for the decision that takes its place", async () => {
    const sent = serving();
    openPage("/decisions");
    await userEvent.click(await overturnButton());
    const box = await dialog();
    await userEvent.type(within(box).getByRole("textbox", { name: "New statement" }), "Retry twice.");
    await userEvent.click(within(box).getByText("More"));
    await userEvent.type(within(box).getByRole("textbox", { name: "ID of the new decision (optional)" }), "pd-2f9a");
    await userEvent.click(within(box).getByRole("button", { name: "Overturn decision" }));
    expect(within(box).getByText(/may not start with pd-, which only plan approval names decisions under/)).toBeTruthy();
    expect(changes(sent)).toHaveLength(0);
  });

  it("shows the daemon's refusal, keeps what was written, and repeats under one ID", async () => {
    let calls = 0;
    const sent = serving(() => {
      calls += 1;
      return calls === 1 ? refusal(409, "decision_ended", `decision ${standing} is overturned`) : { status: 201, body: result() };
    });
    openPage("/decisions");
    await userEvent.click(await overturnButton());
    const box = await dialog();
    await userEvent.type(within(box).getByRole("textbox", { name: "New statement" }), "Retry twice.");
    await userEvent.click(within(box).getByRole("button", { name: "Overturn decision" }));
    expect((await within(box).findByRole("alert")).textContent).toContain("is overturned");
    expect((within(box).getByRole("textbox", { name: "New statement" }) as HTMLTextAreaElement).value).toBe("Retry twice.");
    await userEvent.click(within(box).getByRole("button", { name: "Overturn decision" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(2));
    expect(changes(sent).map((call) => (call.body as { id: string }).id)[1]).toBe((changes(sent)[0]?.body as { id: string }).id);
  });

  it("says what an overturn does before it sends, and Escape sends nothing", async () => {
    const sent = serving();
    openPage("/decisions");
    await userEvent.click(await overturnButton());
    const box = await dialog();
    expect(within(box).getByText(/Each completed work order that rested on it goes to rework, and each lane that read it is told/)).toBeTruthy();
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: `Overturn ${standing}` })).toBeNull());
    expect(changes(sent)).toHaveLength(0);
  });

  it("is on the page of one decision too", async () => {
    serving();
    openPage(`/decisions?id=${standing}`);
    expect(await within(await card("Retry until the budget runs out.")).findByRole("button", { name: `Overturn ${standing}` })).toBeTruthy();
  });
});
