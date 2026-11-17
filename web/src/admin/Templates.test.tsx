import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { Template, TemplateList } from "../api/admin";
import { openPage } from "../opening";
import { golden, refusal, serveViews } from "../testing";

// The Templates view lists every template the daemon's binary embeds, by the first segment of its name, beside the
// selected template's text, as `ropeshift template show <name>` prints it (FR-TPL-5).

const list = golden<TemplateList>("templates");

function serveTemplates(read: () => { status: number; body?: unknown } = () => ({ status: 200, body: list })) {
  return serveViews((call) => (call.method === "GET" && call.path === "/v1/templates" ? read() : undefined));
}

// The groups of the list in the order the view shows them, derived here from the names alone: the five folders of
// templates, then the top-level files under Shared.
const folders = ["workspace", "monorepo", "repository", "user", "policies"];
const expectedGroups: [string, Template[]][] = [
  ...folders.map((folder): [string, Template[]] => [folder, list.templates.filter((template) => template.name.startsWith(`${folder}/`))]),
  ["Shared", list.templates.filter((template) => !template.name.includes("/"))],
];

const rowName = (row: HTMLElement) => row.querySelector(".template-name")?.textContent;

async function groups(): Promise<HTMLElement[]> {
  const region = await screen.findByRole("region", { name: "Templates" });
  return within(region).getAllByRole("list");
}

const rowsOf = (group: HTMLElement) => within(group).getAllByRole("link");

describe("the Templates view", () => {
  it("lists every template under the first segment of its name, the top-level files under Shared", async () => {
    serveTemplates();
    openPage("/templates");
    const shown = await groups();
    const labelOf = (group: HTMLElement) => document.getElementById(group.getAttribute("aria-labelledby") ?? "")?.textContent;
    expect(shown.map(labelOf)).toEqual(expectedGroups.map(([name]) => name));
    expectedGroups.forEach(([name, members], at) => {
      expect(rowsOf(shown[at] as HTMLElement).map(rowName), name).toEqual(members.map((template) => template.name));
    });
    const listed = shown.flatMap((group) => rowsOf(group).map(rowName));
    expect(listed.toSorted()).toEqual(list.templates.map((template) => template.name).toSorted());
    expect(listed.length).toBeGreaterThan(20);
  });

  it("shows on each row its shapes as badges and the path it is written to", async () => {
    serveTemplates();
    openPage("/templates");
    const shown = await groups();
    for (const [, members] of expectedGroups) {
      for (const template of members) {
        const row = shown.flatMap(rowsOf).find((candidate) => rowName(candidate) === template.name) as HTMLElement;
        expect([...row.querySelectorAll(".badge")].map((badge) => badge.textContent), template.name).toEqual(template.shapes);
        const path = row.querySelector(".template-path");
        if (template.writes_to) {
          expect(path?.textContent, template.name).toBe(template.writes_to);
        } else {
          expect(path, template.name).toBeNull();
          expect(row.textContent, template.name).toContain("Not written by a command");
        }
      }
    }
    const claude = shown.flatMap(rowsOf).find((row) => rowName(row) === "claude.md") as HTMLElement;
    expect([...claude.querySelectorAll(".badge")].map((badge) => badge.textContent)).toEqual(["workspace", "monorepo", "repository"]);
  });

  it("selects the template the address names, and shows its text as the command prints it", async () => {
    serveTemplates();
    const name = "policies/team/lane-policy.toml";
    const chosen = list.templates.find((template) => template.name === name) as Template;
    openPage(`/templates?name=${encodeURIComponent(name)}`);
    const shown = await groups();
    const current = shown.flatMap(rowsOf).filter((row) => row.getAttribute("aria-current") === "true");
    expect(current.map(rowName)).toEqual([name]);
    const detail = screen.getByRole("region", { name: "The selected template" });
    expect(within(detail).getByRole("heading", { level: 2, name }).textContent).toBe(name);
    expect(within(detail).getByText(`ropeshift template show ${name}`).tagName).toBe("CODE");
    const content = within(detail).getByRole("region", { name: `Text of ${name}` });
    expect(content.querySelector("pre")?.textContent).toBe(chosen.content);
    expect(content.querySelector("pre")?.textContent?.length).toBeGreaterThan(50);
  });

  it("selects the first template shown when the address names none", async () => {
    serveTemplates();
    openPage("/templates");
    const shown = await groups();
    const first = (expectedGroups[0]?.[1] as Template[])[0] as Template;
    expect(shown.flatMap(rowsOf).filter((row) => row.getAttribute("aria-current") === "true").map(rowName)).toEqual([first.name]);
    const detail = screen.getByRole("region", { name: "The selected template" });
    expect(within(detail).getByRole("heading", { level: 2 }).textContent).toBe(first.name);
    expect(detail.querySelector("pre")?.textContent).toBe(first.content);
  });

  it("opens a template from its row, with the address naming it", async () => {
    serveTemplates();
    openPage("/templates");
    const shown = await groups();
    const target = list.templates.find((template) => template.name === "security.md") as Template;
    await userEvent.click(shown.flatMap(rowsOf).find((row) => rowName(row) === "security.md") as HTMLElement);
    expect(new URLSearchParams(window.location.search).get("name")).toBe("security.md");
    expect(window.location.pathname).toBe("/templates");
    const detail = screen.getByRole("region", { name: "The selected template" });
    await waitFor(() => expect(detail.querySelector("pre")?.textContent).toBe(target.content));
    expect(within(detail).getByRole("heading", { level: 2 }).textContent).toBe("security.md");
  });

  it("says so when the address names no template, and selects none", async () => {
    serveTemplates();
    openPage("/templates?name=workspace%2Fagent.md");
    const shown = await groups();
    expect(shown.flatMap(rowsOf).filter((row) => row.getAttribute("aria-current") === "true")).toEqual([]);
    expect(await screen.findByText("No template with that name")).toBeTruthy();
    expect(document.querySelector(".split-templates .template-text")).toBeNull();
  });

  it("copies the selected template's text exactly", async () => {
    serveTemplates();
    const name = "workspace/makefile";
    const chosen = list.templates.find((template) => template.name === name) as Template;
    openPage(`/templates?name=${encodeURIComponent(name)}`);
    const detail = await screen.findByRole("region", { name: "The selected template" });
    const copy = vi.fn(async () => {});
    Object.defineProperty(navigator, "clipboard", { value: { writeText: copy }, configurable: true });
    await userEvent.click(within(detail).getByRole("button", { name: "Copy" }));
    expect(copy).toHaveBeenCalledWith(chosen.content);
    expect(copy).toHaveBeenCalledTimes(1);
  });

  it("shows the detail as a pushed page when the address names a template, with a way back to the list", async () => {
    serveTemplates();
    openPage("/templates");
    await groups();
    const pushed = () => document.querySelector(".split-templates")?.classList.contains("show-detail");
    expect(pushed()).toBe(false);
    window.history.pushState(null, "", "/templates?name=security.md");
    window.dispatchEvent(new PopStateEvent("popstate"));
    await waitFor(() => expect(pushed()).toBe(true));
    await userEvent.click(screen.getByRole("button", { name: "All templates" }));
    expect(window.location.search).toBe("");
    await waitFor(() => expect(pushed()).toBe(false));
  });

  it("is in the sidebar's Installation group after Projects, and has no G key", async () => {
    serveTemplates();
    openPage("/templates");
    const nav = await screen.findByRole("navigation", { name: "Views" });
    const names = within(nav).getAllByRole("link").map((link) => link.textContent);
    expect(names.slice(names.indexOf("Projects"), names.indexOf("Projects") + 2)).toEqual(["Projects", "Templates"]);
    const link = within(nav).getByRole("link", { name: "Templates" });
    expect(link.getAttribute("href")).toBe("/templates");
    expect(link.getAttribute("aria-current")).toBe("page");
    expect(link.getAttribute("title")).toBe("Templates");
    await waitFor(() => expect(document.title).toBe("Templates - ropeshift"));
  });

  it("is a view of the palette, which opens it", async () => {
    serveTemplates();
    openPage("/needs-you");
    await screen.findByRole("heading", { level: 1, name: "Needs you" });
    await userEvent.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Command palette" });
    await userEvent.type(within(palette).getByRole("combobox"), "templates");
    const option = within(palette).getByRole("option", { name: /^Templates/ });
    expect(option.textContent).not.toMatch(/\bG\s+\w\b/);
    await userEvent.click(option);
    expect(await screen.findByRole("heading", { level: 1, name: "Templates" })).toBeTruthy();
    expect(window.location.pathname).toBe("/templates");
  });

  it("shows a failed read in the API's words with a way to try again, which reads again", async () => {
    let failing = true;
    const sent = serveTemplates(() => (failing ? refusal(503, "unavailable", "this daemon serves no templates") : { status: 200, body: list }));
    openPage("/templates");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("this daemon serves no templates");
    expect(screen.queryByRole("region", { name: "Templates" })).toBeNull();
    failing = false;
    await userEvent.click(within(alert).getByRole("button", { name: "Try again" }));
    expect((await groups()).length).toBe(expectedGroups.length);
    expect(sent.filter((call) => call.path === "/v1/templates").length).toBeGreaterThanOrEqual(2);
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
