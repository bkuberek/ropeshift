import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { Health } from "../api/admin";
import { openPage } from "../opening";
import { refusal, runningHealth, serveViews } from "../testing";

const stale: Health = {
  ...runningHealth,
  installed: "v0.1.0-alpha.17",
  warning: "ropeshift v0.1.0-alpha.17 is installed, but the daemon is still running v0.1.0-alpha.16. Run brew services restart ropeshift to run it.",
};

const serveHealth = (answer: { status: number; body?: unknown }) => serveViews((call) => (call.method === "GET" && call.path === "/v1/health" ? answer : undefined));

const sidebar = () => screen.findByRole("navigation", { name: "Views" });
// The status bar is signed-in's, and stands empty until the page has read what it shows.
const statusBar = async () => {
  await waitFor(() => expect(document.querySelector(".statusbar")?.textContent).toContain("Lanes"));
  return document.querySelector(".statusbar") as HTMLElement;
};
const staleLink = { name: /Daemon out of date\s*: ropeshift v0.1.0-alpha.17 is installed/ };

describe("the version the daemon runs", () => {
  it("is named in the sidebar footer", async () => {
    serveHealth({ status: 200, body: runningHealth });
    openPage("/");
    expect(await within(await sidebar()).findByText("ropeshift v0.1.0-alpha.16")).toBeTruthy();
  });

  it("is given by Settings, About, with the commit, the build date, and the installed release", async () => {
    serveHealth({ status: 200, body: runningHealth });
    openPage("/settings?section=about");
    expect(await screen.findByRole("heading", { level: 1, name: "About" })).toBeTruthy();
    const facts = await screen.findByLabelText("Daemon build");
    const rows = Object.fromEntries([...facts.querySelectorAll(".kv-row")].map((row) => [row.querySelector("dt")?.textContent, row.querySelector("dd")?.textContent]));
    expect(rows).toEqual({
      Version: "v0.1.0-alpha.16",
      Commit: "0123456789abcdef0123456789abcdef01234567",
      Built: "2026-11-08T10:00:00Z",
      Installed: "v0.1.0-alpha.16",
    });
    expect(screen.queryByText(/is installed, but the daemon/)).toBeNull();
  });

  it("gives only what a build stamped", async () => {
    serveHealth({ status: 200, body: { status: "ok", revision: 1, version: "dev" } });
    openPage("/settings?section=about");
    const facts = await screen.findByLabelText("Daemon build");
    expect([...facts.querySelectorAll("dt")].map((term) => term.textContent)).toEqual(["Version"]);
    expect(within(facts).getByText("dev")).toBeTruthy();
  });

  it("is listed among the installation's Settings sections", async () => {
    serveHealth({ status: 200, body: runningHealth });
    openPage("/settings?section=about");
    const tree = await screen.findByRole("navigation", { name: "Settings sections" });
    expect(within(tree).getByRole("link", { name: "About" }).getAttribute("href")).toBe("/settings?section=about");
  });

  it("is left out of the sidebar when the daemon does not say", async () => {
    serveHealth(refusal(503, "unavailable"));
    openPage("/");
    await sidebar();
    await statusBar();
    expect(document.querySelector(".sidebar-version")).toBeNull();
  });
});

describe("the warning that the installed ropeshift is newer than the daemon", () => {
  it("is not shown when the daemon runs the installed release", async () => {
    serveHealth({ status: 200, body: runningHealth });
    openPage("/");
    await within(await sidebar()).findByText("ropeshift v0.1.0-alpha.16");
    expect(within(await statusBar()).queryByText("Daemon out of date")).toBeNull();
  });

  it("is in the status bar, names the fix, and leads to About", async () => {
    serveHealth({ status: 200, body: stale });
    openPage("/");
    const link = await within(await statusBar()).findByRole("link", staleLink);
    expect(link.getAttribute("href")).toBe("/settings?section=about");
    expect(link.textContent).toContain("brew services restart ropeshift");
    expect(link.getAttribute("title")).toBe(stale.warning);
  });

  it("is in About as a warning, beside the installed release", async () => {
    serveHealth({ status: 200, body: stale });
    openPage("/");
    await userEvent.click(await within(await statusBar()).findByRole("link", staleLink));
    const main = await screen.findByRole("main");
    expect(await within(main).findByText(stale.warning as string)).toBeTruthy();
    expect(within(main).getByText("v0.1.0-alpha.17")).toBeTruthy();
  });

  it("says to restart the service when the install is not Homebrew's", async () => {
    const other = { ...stale, warning: "ropeshift v0.1.0-alpha.17 is installed, but the daemon is still running v0.1.0-alpha.16. Restart the ropeshift service to run it." };
    serveHealth({ status: 200, body: other });
    openPage("/settings?section=about");
    expect(await screen.findByText(other.warning)).toBeTruthy();
  });
});
