import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { openPage } from "../opening";
import type { Sent } from "../testing";
import { golden, here, serveViews } from "../testing";

function serveSettings(answer?: (sent: Sent) => { status: number; body?: unknown } | undefined) {
  return serveViews((call) => {
    const own = answer?.(call);
    if (own) {
      return own;
    }
    if (call.method === "GET" && call.path === "/v1/policies?kind=assistant") {
      return { status: 200, body: golden("policies-assistant") };
    }
    return undefined;
  });
}

const posted = (sent: Sent[]) => sent.filter((call) => call.method === "POST" && call.path === "/v1/policies");

describe("Settings: Assistant", () => {
  it("is an installation section after Autonomy defaults, with its note", async () => {
    serveSettings();
    openPage(here("/settings?section=assistant"));
    expect(await screen.findByRole("heading", { name: "Assistant" })).toBeTruthy();
    expect(screen.getByText("When an idle assistant session stops. A stopped conversation stays in the ledger and resumes when you send.")).toBeTruthy();
    const nav = screen.getByRole("navigation", { name: "Settings sections" });
    const titles = within(nav)
      .getAllByRole("link")
      .map((link) => link.textContent);
    expect(titles.indexOf("Assistant")).toBe(titles.indexOf("Autonomy defaults") + 1);
  });

  it("holds the idle stop and the warm idle stop as minutes, as the policy in force states them", async () => {
    serveSettings();
    openPage(here("/settings?section=assistant"));
    const idle = (await screen.findByRole("spinbutton", { name: /Stop an idle session after/ })) as HTMLInputElement;
    const warm = screen.getByRole("spinbutton", { name: /Stop a session no message has reached after/ }) as HTMLInputElement;
    expect(idle.value).toBe("30");
    expect(warm.value).toBe("2");
    expect(idle.min).toBe("1");
    expect(idle.max).toBe("1440");
    expect(screen.getByText(/Version 2, by member-1/)).toBeTruthy();
  });

  it("saves a new version after the difference is reviewed, as durations", async () => {
    const sent = serveSettings((call) => (call.method === "POST" && call.path === "/v1/policies" ? { status: 201, body: { version: 3 } } : undefined));
    openPage(here("/settings?section=assistant"));
    const idle = await screen.findByRole("spinbutton", { name: /Stop an idle session after/ });
    await userEvent.clear(idle);
    await userEvent.type(idle, "45");
    await userEvent.click(screen.getByRole("button", { name: "Review change" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog.textContent).toContain('idle_stop = "45m"');
    await userEvent.click(within(dialog).getByRole("button", { name: "Save version 3" }));
    await waitFor(() => expect(posted(sent)).toHaveLength(1));
    expect((posted(sent)[0]?.body as { body: string }).body).toBe('[assistant]\nversion = 3\nidle_stop = "45m"\nwarm_idle_stop = "2m"\n');
  });

  it("refuses a value outside 1 to 1,440 minutes at the field, and saves nothing", async () => {
    const sent = serveSettings();
    openPage(here("/settings?section=assistant"));
    const idle = await screen.findByRole("spinbutton", { name: /Stop an idle session after/ });
    await userEvent.clear(idle);
    await userEvent.type(idle, "1441");
    expect(await screen.findByText("Enter a whole number of minutes, 1 to 1,440.")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Review change" }) as HTMLButtonElement).disabled).toBe(true);
    expect(posted(sent)).toHaveLength(0);
  });
});
