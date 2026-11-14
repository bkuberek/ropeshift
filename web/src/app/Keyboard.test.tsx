import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { openPage } from "../opening";
import { golden, here, serveViews } from "../testing";

// The tests run where navigator.platform is not a Mac's, so Mod is Control.
const palette = () => screen.findByRole("dialog", { name: "Command palette" });

describe("the keyboard", () => {
  it("goes to a view with G and its key", async () => {
    serveViews();
    openPage("/needs-you");
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    await userEvent.keyboard("gf");
    expect(await screen.findByRole("heading", { level: 1, name: "Floor" })).toBeTruthy();
    expect(window.location.pathname).toBe(here("/floor"));
    await userEvent.keyboard("gn");
    await waitFor(() => expect(window.location.pathname).toBe("/needs-you"));
  });

  it("opens the palette with Control+K, even from a text field, and with a slash", async () => {
    serveViews();
    openPage("/needs-you");
    await userEvent.click(await screen.findByRole("link", { name: /2 questions/ }));
    await userEvent.click(screen.getAllByRole("radio", { name: "Something else" })[0] as HTMLElement);
    await userEvent.click(screen.getByRole("textbox", { name: "Your answer" }));
    await userEvent.keyboard("/");
    expect(screen.queryByRole("dialog", { name: "Command palette" })).toBeNull();
    await userEvent.keyboard("{Control>}k{/Control}");
    expect(await palette()).toBeTruthy();
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Command palette" })).toBeNull());
    expect(document.activeElement).toBe(screen.getByRole("textbox", { name: "Your answer" }));
  });

  it("shows the shortcut sheet with a question mark, where single keys can be turned off", async () => {
    serveViews();
    openPage("/needs-you");
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    await userEvent.keyboard("?");
    const sheet = await screen.findByRole("dialog", { name: "Keyboard shortcuts" });
    expect(within(sheet).getByText("Floor")).toBeTruthy();
    const toggle = within(sheet).getByRole("switch", { name: /Single-key shortcuts/ });
    expect((toggle as HTMLInputElement).checked).toBe(true);
    await userEvent.click(toggle);
    expect(localStorage.getItem("ropeshift-single-keys")).toBe("off");
    await userEvent.keyboard("{Escape}");
    await userEvent.keyboard("gf");
    expect(window.location.pathname).toBe("/needs-you");
    await userEvent.keyboard("{Control>}k{/Control}");
    expect(await palette()).toBeTruthy();
  });

  it("moves through Needs you with J and K, and A only moves focus to the primary button", async () => {
    const sent = serveViews();
    openPage("/needs-you");
    const items = within(await screen.findByRole("list", { name: "Waiting items" })).getAllByRole("link");
    expect(items[0]?.getAttribute("aria-current")).toBe("true");
    await userEvent.keyboard("j");
    await waitFor(() => expect(items[1]?.getAttribute("aria-current")).toBe("true"));
    await userEvent.keyboard("k");
    await waitFor(() => expect(items[0]?.getAttribute("aria-current")).toBe("true"));
    (document.activeElement as HTMLElement | null)?.blur();
    await userEvent.keyboard("jjj");
    await waitFor(() => expect(items[3]?.getAttribute("aria-current")).toBe("true"));
    await userEvent.keyboard("a");
    expect(document.activeElement?.textContent).toBe("Accept");
    expect(sent.some((call) => call.method === "POST")).toBe(false);
  });

  it("opens the conversation panel with a period, the cursor in its message box, and closes it with one from outside the box", async () => {
    serveViews();
    openPage("/needs-you");
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    const toggle = screen.getByRole("button", { name: "Conversation panel" });
    expect(toggle.getAttribute("aria-pressed")).toBe("false");
    await userEvent.keyboard(".");
    expect(toggle.getAttribute("aria-pressed")).toBe("true");
    const dock = await screen.findByRole("complementary", { name: "Conversation" });
    const box = await within(dock).findByRole("textbox", { name: "Message to the assistant" });
    await waitFor(() => expect(document.activeElement).toBe(box));
    // In the box a period is text, because single keys are off in a text field.
    await userEvent.keyboard(".");
    expect((box as HTMLTextAreaElement).value).toBe(".");
    expect(toggle.getAttribute("aria-pressed")).toBe("true");
    (box as HTMLElement).blur();
    await userEvent.keyboard(".");
    expect(toggle.getAttribute("aria-pressed")).toBe("false");
  });

  it("opens the project switcher with P", async () => {
    serveViews();
    openPage("/projects/default/roadmap");
    await screen.findByRole("heading", { level: 1, name: "Roadmap" });
    await userEvent.keyboard("p");
    expect(await screen.findByRole("dialog", { name: "Switch project" })).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByRole("searchbox", { name: "Filter projects" }));
  });
});

describe("the command palette", () => {
  it("finds a view and goes there", async () => {
    serveViews();
    openPage("/needs-you");
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    await userEvent.keyboard("{Control>}k{/Control}");
    const field = within(await palette()).getByRole("combobox");
    await userEvent.type(field, "floor");
    const option = within(await palette()).getByRole("option", { name: /^Floor/ });
    expect(option.getAttribute("aria-selected")).toBe("true");
    expect(field.getAttribute("aria-activedescendant")).toBe(option.id);
    await userEvent.keyboard("{Enter}");
    expect(await screen.findByRole("heading", { level: 1, name: "Floor" })).toBeTruthy();
  });

  it("searches the project's records through the API, and opens the one chosen", async () => {
    const sent = serveViews();
    openPage("/needs-you");
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    await userEvent.keyboard("{Control>}k{/Control}");
    await userEvent.type(within(await palette()).getByRole("combobox"), "#write the api");
    const record = await waitFor(() => {
      const found = within(screen.getByRole("dialog", { name: "Command palette" }))
        .getAllByRole("option")
        .find((option) => option.querySelector(".truncate")?.textContent === "Write the API client");
      if (!found) {
        throw new Error("no record named Write the API client");
      }
      return found;
    });
    const search = sent.find((call) => call.path.startsWith("/v1/views/search"));
    expect(search?.url).toContain("q=write+the+api");
    expect(within(await palette()).queryByRole("option", { name: /^Floor/ })).toBeNull();
    await userEvent.click(record);
    const id = golden<{ records: { id: string; title?: string }[] }>("search").records.find((row) => row.title === "Write the API client")?.id ?? "";
    await waitFor(() => expect(`${window.location.pathname}${window.location.search}`).toBe(here(`/work-order?id=${id}`)));
  });

  it("lists only actions after >, and an action opens what it names without deciding anything", async () => {
    const sent = serveViews();
    openPage("/needs-you");
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    await userEvent.keyboard("{Control>}k{/Control}");
    await userEvent.type(within(await palette()).getByRole("combobox"), ">shortcuts");
    const options = within(await palette()).getAllByRole("option");
    expect(options.map((option) => option.textContent)).toEqual(["Show keyboard shortcuts?"]);
    await userEvent.keyboard("{Enter}");
    expect(await screen.findByRole("dialog", { name: "Keyboard shortcuts" })).toBeTruthy();
    expect(sent.some((call) => call.method === "POST")).toBe(false);
  });

  it("opens a decision with its primary button focused, and presses nothing", async () => {
    const sent = serveViews();
    openPage("/projects/default/roadmap");
    await screen.findByRole("heading", { level: 1, name: "Roadmap" });
    await userEvent.keyboard("{Control>}k{/Control}");
    await userEvent.type(within(await palette()).getByRole("combobox"), "decide trim");
    await userEvent.keyboard("{Enter}");
    await waitFor(() => expect(document.activeElement?.textContent).toBe("Accept"));
    expect(sent.some((call) => call.method === "POST")).toBe(false);
  });

  it("says when nothing matches", async () => {
    serveViews((call) => (call.path.startsWith("/v1/views/search") ? { status: 200, body: { as_of: 1, revision: 1, records: [] } } : undefined));
    openPage("/needs-you");
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    await userEvent.keyboard("{Control>}k{/Control}");
    await userEvent.type(within(await palette()).getByRole("combobox"), "zzzz");
    expect(await within(await palette()).findByText(/Nothing matches/)).toBeTruthy();
  });
});
