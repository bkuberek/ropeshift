import { screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { openPage } from "../opening";
import { serveViews } from "../testing";
import { reloading } from "./ErrorBoundary";

// The page's code loads when a person first opens a page that manages the installation. When the daemon was updated under an
// open tab, that file is gone, and the page must say so and keep its navigation, never go blank.
vi.mock("../admin/Agents", () => ({
  get AgentsPage(): never {
    throw new TypeError("Failed to fetch dynamically imported module: /assets/Agents-3f2a.js");
  },
}));

describe("a page whose code is gone", () => {
  it("says ropeshift was updated and offers Reload, inside the shell", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const reload = vi.spyOn(reloading, "now").mockImplementation(() => {});
    sessionStorage.clear();
    serveViews();
    openPage("/projects/default/agents");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("ropeshift was updated");
    expect(within(alert).getByRole("button", { name: "Reload" })).toBeTruthy();
    expect(screen.getByRole("navigation", { name: "Views" })).toBeTruthy();
    expect(reload).toHaveBeenCalledTimes(1);
    sessionStorage.clear();
  });
});
