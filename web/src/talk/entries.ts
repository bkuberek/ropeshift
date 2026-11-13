import type { TalkEntry } from "../api/talk";

// A stream as rows a person reads: its entries in time order, a divider before the first entry of each day,
// the notice a clear leaves, and the line a message never confirming a card.

export type Row =
  | { type: "divider"; key: string; label: string }
  | { type: "entry"; key: string; entry: TalkEntry; beforeClear: boolean }
  | { type: "assent"; key: string };

export const assentWords = "A message never confirms a card. The card above waits for your click on Confirm.";

const day = 24 * 60 * 60 * 1000;

const startOfDay = (at: number) => new Date(at).setHours(0, 0, 0, 0);

// dayLabel names a day by its place from now when it is today or yesterday, and by its date otherwise.
export function dayLabel(at: number, now: number): string {
  const apart = Math.round((startOfDay(now) - startOfDay(at)) / day);
  if (apart === 0) {
    return "Today";
  }
  if (apart === 1) {
    return "Yesterday";
  }
  return new Date(at).toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric", year: new Date(at).getFullYear() === new Date(now).getFullYear() ? undefined : "numeric" });
}

// markerWords says a session's marker as the notice it is.
export function markerWords(marker: string): string {
  switch (marker) {
    case "started":
      return "Session started.";
    case "stopped":
      return "Session stopped.";
    case "compacted":
      return "Context compacted.";
    case "cleared":
      return "Context cleared. Earlier messages stay readable.";
    case "idle_stopped":
      return "Session stopped after sitting idle.";
  }
  return marker;
}

// buildRows lays a stream out. Entries stand by the time they were recorded, which the API's own order
// already follows, and one that shares a moment with another keeps the order it came in.
export function buildRows(entries: TalkEntry[], now: number): Row[] {
  const ordered = entries.map((entry, at) => ({ entry, at })).sort((a, b) => a.entry.at - b.entry.at || a.at - b.at);
  const rows: Row[] = [];
  let shown = "";
  for (const { entry } of ordered) {
    const label = dayLabel(entry.at, now);
    const on = String(startOfDay(entry.at));
    if (on !== shown) {
      rows.push({ type: "divider", key: `day-${on}`, label });
      shown = on;
    }
    rows.push({ type: "entry", key: entry.id, entry, beforeClear: Boolean(entry.before_clear) });
    if (entry.kind === "member" && entry.assent) {
      rows.push({ type: "assent", key: `assent-${entry.id}` });
    }
  }
  return rows;
}

// mergePages joins an older run of entries to the newest page, oldest first. An entry both hold takes the
// newest page's form, because a card, a request, and a question show as they stand now.
export function mergePages(older: TalkEntry[], newest: TalkEntry[]): TalkEntry[] {
  const seen = new Set(newest.map((entry) => entry.id));
  return [...older.filter((entry) => !seen.has(entry.id)), ...newest];
}
