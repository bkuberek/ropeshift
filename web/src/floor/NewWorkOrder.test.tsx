import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { openPage } from "../opening";
import type { Sent } from "../testing";
import { golden, refusal, serveViews } from "../testing";

const dialog = () => screen.findByRole("dialog", { name: "New work order" });
const changes = (sent: Sent[]) => sent.filter((call) => call.method === "POST");
const anID = expect.stringMatching(/^[0-9A-Z]{26}$/);
const created = { id: "wo-new", title: "Export the invoices", state: "contract_review", kind: "change" };

function serving(post?: (call: Sent) => { status: number; body?: unknown } | undefined, answer?: (call: Sent) => { status: number; body?: unknown } | undefined) {
  return serveViews((call) => {
    const own = answer?.(call);
    if (own) {
      return own;
    }
    if (call.method === "POST") {
      return post?.(call) ?? { status: 201, body: created };
    }
    if (call.path === "/v1/roster") {
      return { status: 200, body: golden("roster") };
    }
    return undefined;
  });
}

// The page's own button, which opens the dialog.
const pageButton = () => screen.findByRole("button", { name: "New work order" });

const field = (box: HTMLElement, name: string) => within(box).getByRole("textbox", { name });

async function fill(box: HTMLElement, title: string, criteria = "") {
  await userEvent.type(field(box, "Title"), title);
  if (criteria) {
    await userEvent.type(field(box, "Criteria"), criteria);
  }
}

describe("creating a work order", () => {
  it("sends the title and the criteria as the command does, and opens the work order it made", async () => {
    document.cookie = "ropeshift_csrf=token-from-cookie";
    const sent = serving();
    openPage("/work");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await fill(box, "Export the invoices", "An export downloads.{Enter}Quotes are escaped.");
    await userEvent.click(within(box).getByRole("button", { name: "Create work order" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ method: "POST", path: "/v1/work-orders", project: "default", csrf: "token-from-cookie" });
    expect(changes(sent)[0]?.body).toEqual({
      id: anID,
      title: "Export the invoices",
      criteria: [
        { id: "c1", text: "An export downloads." },
        { id: "c2", text: "Quotes are escaped." },
      ],
    });
    expect((await screen.findAllByText("Created Export the invoices.")).length).toBeGreaterThan(0);
    await waitFor(() => expect(window.location.pathname + window.location.search).toBe("/projects/default/work-order?id=wo-new"));
    expect(screen.queryByRole("dialog", { name: "New work order" })).toBeNull();
  });

  it("sends every field the command takes, each as the command sends it", async () => {
    const sent = serving();
    openPage("/work");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await fill(box, "Plan the export", "The plan covers CSV.");
    await userEvent.click(within(box).getByText("More"));
    await userEvent.selectOptions(within(box).getByRole("combobox", { name: "Kind" }), "plan");
    await userEvent.type(field(box, "Statement"), "Plan how the export works.");
    await waitFor(() => expect(within(within(box).getByRole("combobox", { name: "Repository" })).getAllByRole("option").length).toBeGreaterThan(1));
    await userEvent.selectOptions(within(box).getByRole("combobox", { name: "Repository" }), "id-api");
    await userEvent.type(field(box, "Depends on"), "wo-1, wo-2{Enter}wo-1");
    await userEvent.type(field(box, "Requirements"), "FR-A-1 FR-B.2");
    await waitFor(() => expect(within(within(box).getByRole("combobox", { name: "Agent" })).getAllByRole("option").length).toBeGreaterThan(1));
    await userEvent.selectOptions(within(box).getByRole("combobox", { name: "Agent" }), "heavy-implementation");
    await userEvent.selectOptions(within(box).getByRole("combobox", { name: "Size" }), "small");
    await userEvent.type(field(box, "Priority"), "3");
    await userEvent.type(field(box, "Work order ID (optional)"), "wo-form");
    await userEvent.click(within(box).getByRole("button", { name: "Create work order" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.body).toEqual({
      id: "wo-form",
      kind: "plan",
      title: "Plan the export",
      statement: "Plan how the export works.",
      criteria: [{ id: "c1", text: "The plan covers CSV." }],
      repository_id: "id-api",
      depends_on: ["wo-1", "wo-2"],
      requirements: ["FR-A-1", "FR-B.2"],
      agent: "heavy-implementation",
      size: "small",
      priority: 3,
    });
  });

  it("offers the roster's enabled agents and the project's repositories, and the default of each", async () => {
    serving();
    openPage("/work");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await userEvent.click(within(box).getByText("More"));
    const agents = within(box).getByRole("combobox", { name: "Agent" });
    await waitFor(() => expect(within(agents).getAllByRole("option").length).toBeGreaterThan(1));
    expect(within(agents).getAllByRole("option")[0]?.textContent).toBe("The default for its kind and size");
    expect(within(agents).getByRole("option", { name: "implementation" })).toBeTruthy();
    const repositories = within(box).getByRole("combobox", { name: "Repository" });
    await waitFor(() => expect(within(repositories).getAllByRole("option").length).toBeGreaterThan(1));
    expect(within(repositories).getAllByRole("option")[0]?.textContent).toBe("None");
    expect(within(repositories).getByRole("option", { name: "api" })).toBeTruthy();
  });

  it("asks for a title before it sends, and sends nothing without one", async () => {
    const sent = serving();
    openPage("/work");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await userEvent.click(within(box).getByRole("button", { name: "Create work order" }));
    expect(within(box).getByText("Give the work order a title.")).toBeTruthy();
    expect(field(box, "Title").getAttribute("aria-invalid")).toBe("true");
    expect(changes(sent)).toHaveLength(0);
  });

  it("opens More for a problem it holds, so the problem is seen, and sends nothing", async () => {
    const sent = serving();
    openPage("/work");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await fill(box, "Export the invoices");
    await userEvent.type(field(box, "Work order ID (optional)"), "has space");
    expect(within(box).getByText("More").closest("details")?.open).toBe(false);
    await userEvent.click(within(box).getByRole("button", { name: "Create work order" }));
    expect(within(box).getByText("More").closest("details")?.open).toBe(true);
    expect(within(box).getByText(/1 to 64 letters, digits/)).toBeTruthy();
    expect(field(box, "Work order ID (optional)").getAttribute("aria-invalid")).toBe("true");
    expect(changes(sent)).toHaveLength(0);
  });

  it("shows the API's refusal at the foot, keeps what was written, and repeats under one ID", async () => {
    let calls = 0;
    const sent = serving(() => {
      calls += 1;
      return calls === 1 ? refusal(400, "invalid", "size \"huge\"") : { status: 201, body: created };
    });
    openPage("/work");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await fill(box, "Export the invoices", "An export downloads.");
    await userEvent.click(within(box).getByRole("button", { name: "Create work order" }));
    expect((await within(box).findByRole("alert")).textContent).toContain('size "huge"');
    expect((field(box, "Title") as HTMLInputElement).value).toBe("Export the invoices");
    expect(window.location.pathname).toBe("/projects/default/work");
    await userEvent.click(within(box).getByRole("button", { name: "Create work order" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(2));
    const ids = changes(sent).map((call) => (call.body as { id: string }).id);
    expect(ids[1]).toBe(ids[0]);
    await waitFor(() => expect(window.location.search).toBe("?id=wo-new"));
  });

  it("stays usable when the repositories or the roster cannot be read", async () => {
    const sent = serving(undefined, (call) => (call.method === "GET" && (call.path === "/v1/repositories" || call.path === "/v1/roster" || call.path === "/v1/sources") ? refusal(500, "internal", "read failed") : undefined));
    openPage("/work");
    await userEvent.click(await pageButton());
    const box = await dialog();
    await fill(box, "Export the invoices");
    await userEvent.click(within(box).getByText("More"));
    expect(await within(box).findByText(/The agents could not be read/)).toBeTruthy();
    await userEvent.click(within(box).getByRole("button", { name: "Create work order" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.body).toEqual({ id: anID, title: "Export the invoices" });
  });

  it("closes on Escape and sends nothing", async () => {
    const sent = serving();
    openPage("/work");
    await userEvent.click(await pageButton());
    await dialog();
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "New work order" })).toBeNull());
    expect(changes(sent)).toHaveLength(0);
  });

  it("is in the command palette, which opens the dialog on the work orders", async () => {
    serving();
    openPage("/needs-you");
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    await userEvent.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Command palette" });
    await userEvent.type(within(palette).getByRole("combobox"), "new work order");
    await userEvent.click(await within(palette).findByRole("option", { name: /^New work order/ }));
    const box = await dialog();
    expect(window.location.pathname).toBe("/projects/default/work");
    expect(box.contains(document.activeElement)).toBe(true);
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(window.location.search).toBe(""));
  });
});
