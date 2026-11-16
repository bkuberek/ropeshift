import { screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { openPage } from "../opening";
import { serveViews } from "../testing";

// An address that says `?focus=Name` lands with the control of that name focused, so the command palette can take a
// member to a control that has no dialog of its own. The control is pressed by the member, never by the address.

describe("an address that names a control to focus", () => {
  it("focuses the control once the view has read, and leaves the address without the name", async () => {
    serveViews();
    openPage("/workers?focus=Create join token");
    const button = await screen.findByRole("button", { name: "Create join token" });
    await waitFor(() => expect(document.activeElement).toBe(button));
    expect(window.location.search).toBe("");
    expect(window.location.pathname).toBe("/workers");
  });

  it("matches the start of a control's name, in any case", async () => {
    serveViews();
    openPage("/workers?focus=create join");
    const button = await screen.findByRole("button", { name: "Create join token" });
    await waitFor(() => expect(document.activeElement).toBe(button));
  });

  it("presses nothing", async () => {
    const sent = serveViews();
    openPage("/workers?focus=Create join token");
    const button = await screen.findByRole("button", { name: "Create join token" });
    await waitFor(() => expect(document.activeElement).toBe(button));
    expect(sent.filter((call) => call.method !== "GET")).toEqual([]);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("gives up on a name no control has, and still takes it off the address", async () => {
    serveViews();
    openPage("/workers?focus=No such control");
    await screen.findByRole("heading", { level: 1, name: "Workers and resources" });
    await waitFor(() => expect(window.location.search).toBe(""), { timeout: 8000 });
  }, 12_000);
});
