import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { openPage } from "../opening";
import type { Sent } from "../testing";
import { refusal, serveViews } from "../testing";

const dialog = () => screen.findByRole("dialog", { name: "Add category" });
const filters = () => screen.findByRole("navigation", { name: "Filter by category" });
const changes = (sent: Sent[]) => sent.filter((call) => call.method === "POST");

// serving answers the views from their goldens and the project's categories, and a change with what a test says.
function serving(post?: (call: Sent) => { status: number; body?: unknown } | undefined) {
  return serveViews((call) => {
    if (call.method === "POST") {
      return post?.(call) ?? { status: 204 };
    }
    if (call.method === "GET" && call.path === "/v1/categories") {
      return { status: 200, body: { categories: ["architecture", "business", "data", "security"] } };
    }
    return undefined;
  });
}

describe("adding a category", () => {
  it("sends the name from the category filter of the open questions, and says it was added", async () => {
    document.cookie = "ropeshift_csrf=token-from-cookie";
    const sent = serving();
    openPage("/questions");
    await userEvent.click(within(await filters()).getByRole("button", { name: "Add category" }));
    const box = await dialog();
    await userEvent.type(within(box).getByRole("textbox", { name: "Name" }), "release-planning");
    await userEvent.click(within(box).getByRole("button", { name: "Add category" }));
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]).toMatchObject({ method: "POST", path: "/v1/categories", project: "default", body: { name: "release-planning" }, csrf: "token-from-cookie" });
    expect((await screen.findAllByText("Added the category release-planning.")).length).toBeGreaterThan(0);
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Add category" })).toBeNull());
  });

  it("is offered in the category filter of the decisions too", async () => {
    const sent = serving();
    openPage("/decisions");
    await userEvent.click(within(await filters()).getByRole("button", { name: "Add category" }));
    const box = await dialog();
    await userEvent.type(within(box).getByRole("textbox", { name: "Name" }), "billing");
    await userEvent.keyboard("{Enter}");
    await waitFor(() => expect(changes(sent)).toHaveLength(1));
    expect(changes(sent)[0]?.body).toEqual({ name: "billing" });
  });

  it("holds a name the ledger would refuse at its field, and sends nothing", async () => {
    const sent = serving();
    openPage("/questions");
    await userEvent.click(within(await filters()).getByRole("button", { name: "Add category" }));
    const box = await dialog();
    const name = within(box).getByRole("textbox", { name: "Name" });
    for (const bad of ["Release Planning", "-leading", "has space", "x".repeat(64)]) {
      await userEvent.clear(name);
      await userEvent.type(name, bad);
      await userEvent.click(within(box).getByRole("button", { name: "Add category" }));
      expect(within(box).getByText(/lowercase words joined by hyphens/)).toBeTruthy();
      expect(name.getAttribute("aria-invalid")).toBe("true");
    }
    await userEvent.clear(name);
    await userEvent.click(within(box).getByRole("button", { name: "Add category" }));
    expect(within(box).getByText(/Give the category a name/)).toBeTruthy();
    expect(changes(sent)).toHaveLength(0);
  });

  it("shows the daemon's refusal in the dialog, and keeps what was typed", async () => {
    serving((call) => (call.method === "POST" ? refusal(400, "invalid", "invalid question or decision: category is taken") : undefined));
    openPage("/questions");
    await userEvent.click(within(await filters()).getByRole("button", { name: "Add category" }));
    const box = await dialog();
    await userEvent.type(within(box).getByRole("textbox", { name: "Name" }), "billing");
    await userEvent.click(within(box).getByRole("button", { name: "Add category" }));
    expect((await within(box).findByRole("alert")).textContent).toContain("category is taken");
    expect((within(box).getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("billing");
  });

  it("closes on Escape and sends nothing", async () => {
    const sent = serving();
    openPage("/questions");
    await userEvent.click(within(await filters()).getByRole("button", { name: "Add category" }));
    await dialog();
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Add category" })).toBeNull());
    expect(changes(sent)).toHaveLength(0);
  });

  it("is in the command palette, which opens the dialog on the view that filters by category", async () => {
    serving();
    openPage("/needs-you");
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    await userEvent.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Command palette" });
    await userEvent.type(within(palette).getByRole("combobox"), "add category");
    await userEvent.click(await within(palette).findByRole("option", { name: /^Add category/ }));
    const box = await dialog();
    expect(window.location.pathname).toBe("/projects/default/questions");
    // The view a person moved to has taken its own focus by then, and the dialog is where focus ends.
    expect(box.contains(document.activeElement)).toBe(true);
    // Closing it leaves the address of the view, so a reload does not open it again.
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(window.location.search).toBe(""));
    cleanup();
  });

  it("opens the dialog where a member is, when that is the decisions", async () => {
    serving();
    openPage("/decisions");
    await within(await filters()).findByRole("button", { name: "Add category" });
    await userEvent.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Command palette" });
    await userEvent.type(within(palette).getByRole("combobox"), "add category");
    await userEvent.click(await within(palette).findByRole("option", { name: /^Add category/ }));
    expect(await dialog()).toBeTruthy();
    expect(window.location.pathname).toBe("/projects/default/decisions");
  });
});
