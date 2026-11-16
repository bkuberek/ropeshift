import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { NoticeList } from "../api/types";
import { openPage } from "../opening";
import { golden, here, refusal, serveViews } from "../testing";

// The Audit view's Notices tab says what each channel was told and how many times, as `ropeshift notice list` does:
// one row for each notice on each channel, with its kind and reason, its attempts, and whether the channel took it.

function serveNotices(read: () => { status: number; body?: unknown } = () => ({ status: 200, body: golden<NoticeList>("notices") })) {
  return serveViews((call) => {
    if (call.method === "GET" && call.path === "/v1/notices") {
      return read();
    }
    if (call.method === "GET" && call.path.startsWith("/v1/audit")) {
      return { status: 200, body: golden("audit") };
    }
    if (call.method === "GET" && call.path === "/v1/members") {
      return { status: 200, body: golden("members") };
    }
    return undefined;
  });
}

const cell = (row: HTMLElement, column: string) => row.querySelector<HTMLElement>(`[data-label="${column}"]`);
const flat = (element: Element | null | undefined) => (element?.textContent ?? "").replaceAll(/\s+/g, " ").trim();

async function table(): Promise<HTMLElement[]> {
  const region = await screen.findByRole("region", { name: "Notices" });
  return waitFor(() => {
    const rows = within(region).getAllByRole("row").slice(1);
    if (rows.length === 0) {
      throw new Error("no notice row");
    }
    return rows;
  });
}

describe("the Notices tab of the Audit view", () => {
  const notices = golden<NoticeList>("notices").notices;

  it("is a tab beside Events on a project's log, which the address names, and not on the installation's", async () => {
    serveNotices();
    openPage(here("/audit"));
    const tabs = await screen.findByRole("tablist", { name: "Audit" });
    expect(within(tabs).getAllByRole("tab").map((tab) => tab.textContent)).toEqual(["Events", "Notices"]);
    expect(within(tabs).getByRole("tab", { name: "Events" }).getAttribute("aria-selected")).toBe("true");
    await userEvent.click(within(tabs).getByRole("tab", { name: "Notices" }));
    expect(window.location.search).toBe("?tab=notices");
    await table();
  });

  it("opens on the tab its address names", async () => {
    serveNotices();
    openPage(here("/audit?tab=notices"));
    expect((await screen.findByRole("tab", { name: "Notices" })).getAttribute("aria-selected")).toBe("true");
    expect(await table()).toHaveLength(notices.length);
  });

  it("has no tabs on the installation's log, which holds no project's notices", async () => {
    serveNotices();
    openPage("/audit?tab=notices");
    await screen.findByRole("region", { name: "Events" });
    expect(screen.queryByRole("tablist", { name: "Audit" })).toBeNull();
  });

  it("lists each notice on each channel with its kind, reason, attempts, and whether the channel took it, as the command prints", async () => {
    serveNotices();
    openPage(here("/audit?tab=notices"));
    const rows = await table();
    notices.forEach((notice, at) => {
      const row = rows[at] as HTMLElement;
      expect(flat(cell(row, "Channel")), notice.delivery).toBe(notice.channel);
      expect(flat(cell(row, "Notice")), notice.delivery).toContain(notice.kind === "work" ? "Work" : notice.kind[0]?.toUpperCase() + notice.kind.slice(1));
      if (notice.reason) {
        expect(flat(cell(row, "Notice")), notice.delivery).toContain(notice.reason);
      }
      expect(flat(cell(row, "Attempts")), notice.delivery).toBe(String(notice.attempts));
      expect(flat(cell(row, "State")), notice.delivery).toContain(notice.delivered ? "Delivered" : "Not delivered");
      expect(cell(row, "Delivery")?.textContent, notice.delivery).toBe(notice.delivery.slice(0, 12));
    });
  });

  it("says a channel that took a notice after more than one attempt may have heard it twice, and quotes what a channel refused with", async () => {
    serveNotices();
    openPage(here("/audit?tab=notices"));
    const rows = await table();
    const twice = notices.findIndex((notice) => notice.delivered && notice.attempts > 1);
    expect(twice).toBeGreaterThanOrEqual(0);
    expect(flat(rows[twice])).toMatch(/may have heard it more than once/);
    for (const [at, notice] of notices.entries()) {
      if (notice.delivered && notice.attempts === 1) {
        expect(flat(rows[at]), notice.delivery).not.toMatch(/more than once/);
      }
      if (notice.failure) {
        expect(flat(rows[at]), notice.delivery).toContain(notice.failure);
      }
    }
  });

  it("says no channel was told anything, and what makes a notice, when the ledger holds none", async () => {
    serveNotices(() => ({ status: 200, body: golden<NoticeList>("notices-empty") }));
    openPage(here("/audit?tab=notices"));
    expect(await screen.findByText("No channel has been told anything")).toBeTruthy();
    expect(screen.getByText(/only when a notify policy turns a channel on/)).toBeTruthy();
    expect(screen.queryByRole("region", { name: "Notices" })).toBeNull();
  });

  it("says why when the read is refused", async () => {
    serveNotices(() => refusal(500, "internal", "the ledger could not be read"));
    openPage(here("/audit?tab=notices"));
    expect(await screen.findByText(/the ledger could not be read/)).toBeTruthy();
  });
});
