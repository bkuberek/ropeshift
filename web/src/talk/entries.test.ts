import { describe, expect, it } from "vitest";

import type { TalkEntry, TalkPage } from "../api/talk";
import { golden } from "../testing";
import { assentWords, buildRows, dayLabel, markerWords, mergePages } from "./entries";

const entry = (id: string, at: number, extra: Partial<TalkEntry> = {}): TalkEntry => ({ id, at, kind: "reply", body: id, ...extra });

const day = (offset: number, hour = 12) => new Date(2026, 10, 13 + offset, hour, 0, 0).getTime();
const now = day(0, 15);

describe("the rows of a stream", () => {
  it("stand in time order, with a divider before the first entry of each day", () => {
    const rows = buildRows([entry("c", day(0)), entry("a", day(-2)), entry("b", day(-1, 9)), entry("d", day(0, 14))], now);
    expect(rows.map((row) => (row.type === "divider" ? `-- ${row.label}` : row.type === "entry" ? row.entry.id : row.type))).toEqual(["-- " + dayLabel(day(-2), now), "a", "-- Yesterday", "b", "-- Today", "c", "d"]);
  });

  it("say a day by name when it is today or yesterday, and by date otherwise", () => {
    expect(dayLabel(day(0), now)).toBe("Today");
    expect(dayLabel(day(-1), now)).toBe("Yesterday");
    expect(dayLabel(day(-9), now)).toMatch(/\d/);
  });

  it("mark each entry before a clear, and say so once above the first entry after it", () => {
    const rows = buildRows(
      [entry("old", day(0, 9), { before_clear: true }), entry("cleared", day(0, 10), { kind: "marker", marker: "cleared", body: undefined }), entry("new", day(0, 11))],
      now,
    );
    expect(rows.filter((row) => row.type === "entry").map((row) => (row.type === "entry" ? [row.entry.id, row.beforeClear] : []))).toEqual([
      ["old", true],
      ["cleared", false],
      ["new", false],
    ]);
  });

  it("add the line a message never confirms a card, under a member's assent", () => {
    const rows = buildRows([entry("yes", day(0, 9), { kind: "member", assent: true }), entry("no", day(0, 10), { kind: "member" })], now);
    const kinds = rows.map((row) => row.type);
    expect(kinds).toEqual(["divider", "entry", "assent", "entry"]);
    expect(assentWords).toBe("A message never confirms a card. The card above waits for your click on Confirm.");
  });

  it("say each marker in words", () => {
    expect(markerWords("started")).toBe("Session started.");
    expect(markerWords("stopped")).toBe("Session stopped.");
    expect(markerWords("compacted")).toBe("Context compacted.");
    expect(markerWords("cleared")).toBe("Context cleared. Earlier messages stay readable.");
    expect(markerWords("idle_stopped")).toBe("Session stopped after sitting idle.");
    expect(markerWords("surprise")).toBe("surprise");
  });

  it("read what the API wrote from a real ledger", () => {
    const page = golden<TalkPage>("conversation-entries");
    const rows = buildRows(page.entries, page.as_of);
    expect(rows.filter((row) => row.type === "entry")).toHaveLength(page.entries.length);
    expect(rows.filter((row) => row.type === "assent")).toHaveLength(1);
    const at = rows.findIndex((row) => row.type === "assent");
    const before = rows[at - 1];
    expect(before?.type === "entry" && before.entry.assent).toBe(true);
  });
});

describe("pages of a stream", () => {
  it("merge into one run, oldest first, with an entry seen again taking its newest form", () => {
    const older = [entry("a", 1), entry("b", 2)];
    const newest = [entry("b", 2, { body: "b, newer" }), entry("c", 3)];
    expect(mergePages(older, newest).map((row) => [row.id, row.body])).toEqual([
      ["a", "a"],
      ["b", "b, newer"],
      ["c", "c"],
    ]);
  });
});
