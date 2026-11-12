import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { openPage } from "../opening";
import { refusal, serveViews } from "../testing";

const twoProjects = {
  status: 200,
  body: {
    projects: [
      { id: "default", name: "default", display_name: "Default", state: "active", created_at: 1, repositories: [] },
      { id: "acme-id", name: "acme", display_name: "Acme", state: "active", created_at: 2, repositories: ["example.com/acme/invoices"] },
      { id: "old-id", name: "old", state: "archived", created_at: 3, repositories: [] },
    ],
  },
};

// switcher opens the project switcher, the button that names the current project.
async function switcher() {
  await userEvent.click(await screen.findByRole("button", { name: /^Project: / }));
  return screen.findByRole("menu", { name: "Projects" });
}

describe("projects", () => {
  it("names the project in the address of every view and of every call", async () => {
    const sent = serveViews(undefined, { projects: twoProjects });
    openPage("/projects/acme/roadmap");
    await screen.findByRole("heading", { level: 1, name: "Roadmap" });
    // The registry and the combined Needs you are the installation's, and read every project.
    const scoped = () => sent.filter((call) => call.url !== "/v1/projects" && call.url !== "/v1/needs-you");
    // The heading can render before the view's first call is sent, so wait for one.
    await waitFor(() => expect(scoped().length).toBeGreaterThan(0));
    for (const call of scoped()) {
      expect(call.project, call.url).toBe("acme");
    }
    const views = await screen.findByRole("navigation", { name: "Views" });
    expect(within(views).getByRole("link", { name: "Plans" }).getAttribute("href")).toBe("/projects/acme/plans");
  });

  it("opens an address with no project on the first active one, at the same view", async () => {
    serveViews(undefined, { projects: twoProjects });
    openPage("/decisions?id=decision-1");
    await screen.findByRole("heading", { level: 1, name: "decision-1" });
    expect(`${window.location.pathname}${window.location.search}`).toBe("/projects/default/decisions?id=decision-1");
  });

  it("opens an address with no project on the one this browser showed last", async () => {
    serveViews(undefined, { projects: twoProjects });
    localStorage.setItem("ropeshift-project", "acme");
    openPage("/floor");
    await waitFor(() => expect(window.location.pathname).toBe("/projects/acme/floor"));
  });

  it("opens Needs you, installation-wide, on the address that names nothing", async () => {
    serveViews(undefined, { projects: twoProjects });
    openPage("/");
    await waitFor(() => expect(window.location.pathname).toBe("/needs-you"));
    expect(await screen.findByRole("heading", { level: 1, name: "Needs you" })).toBeTruthy();
  });

  it("opens a project's own address on Needs you for that project", async () => {
    serveViews(undefined, { projects: twoProjects });
    openPage("/projects/acme/");
    await waitFor(() => expect(`${window.location.pathname}${window.location.search}`).toBe("/needs-you?project=acme"));
  });

  it("switches to the same view of another project, and reads that project's records", async () => {
    const sent = serveViews(undefined, { projects: twoProjects });
    openPage("/projects/default/plans");
    await screen.findByRole("heading", { level: 1, name: "Plans" });
    const choose = await switcher();
    const rows = within(choose).getAllByRole("menuitemradio");
    expect(rows.map((row) => row.textContent)).toEqual(["DDefault", "AAcme", "Ooldarchived, read-only"]);
    expect(rows.map((row) => row.getAttribute("aria-checked"))).toEqual(["true", "false", "false"]);
    await userEvent.click(within(choose).getByRole("menuitemradio", { name: /Acme/ }));
    await waitFor(() => expect(window.location.pathname).toBe("/projects/acme/plans"));
    await waitFor(() => expect(sent.some((call) => call.project === "acme" && call.path === "/v1/views/plans")).toBe(true));
    expect(screen.getByRole("button", { name: /^Project: / }).textContent).toContain("Acme");
  });

  it("filters the projects by name", async () => {
    serveViews(undefined, { projects: twoProjects });
    openPage("/projects/default/plans");
    await screen.findByRole("heading", { level: 1, name: "Plans" });
    const choose = await switcher();
    await userEvent.type(screen.getByRole("searchbox", { name: "Filter projects" }), "ac");
    expect(within(choose).getAllByRole("menuitemradio").map((row) => row.textContent)).toEqual(["AAcme"]);
  });

  it("asks for a passphrase before it can list the projects", async () => {
    serveViews(undefined, { projects: refusal(401, "session_required") });
    openPage("/");
    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeTruthy();
  });

  it("says when the installation holds no project", async () => {
    serveViews(undefined, { projects: { status: 200, body: { projects: [] } } });
    openPage("/");
    expect(await screen.findByText(/holds no project/)).toBeTruthy();
  });
});
