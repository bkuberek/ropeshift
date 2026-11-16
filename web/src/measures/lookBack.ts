import type { DeliveryGroup, DeliveryWeek } from "../api/measures";
import { byProject } from "../api/measures";
import { segmentKinds } from "../floor/evidence";
import { fixed } from "./format";

// The windows look back reads, as the command's --window names them, and the one it reads when none is named.
export const windows = [
  { value: "24h", label: "24 hours" },
  { value: "7d", label: "7 days" },
  { value: "30d", label: "30 days" },
] as const;

export const defaultWindow = "30d";

// The kinds of segment are drawn as the work order's timeline draws them: one word, tone, and fill each, so a
// colour never carries the meaning alone. Waiting for a person has a word and a tone of its own.
const order = Object.keys(segmentKinds);

export type Share = { kind: string; word: string; tone: string; fill: string; fraction: number; percent: string; apart: boolean };

// sharesOf lists where a group's time went, in the order of the kinds above with waiting for a person last and
// apart, each with its share to a tenth of a percent as the command prints it. A kind with no time is left out.
export function sharesOf(share: Record<string, number>): Share[] {
  const kinds = [...order.filter((kind) => kind !== "person"), ...Object.keys(share).filter((kind) => !(kind in segmentKinds)).sort(), "person"];
  return kinds
    .filter((kind) => (share[kind] ?? 0) > 0)
    .map((kind) => {
      const look = (segmentKinds as Record<string, { word: string; tone: string; fill: string } | undefined>)[kind];
      const fraction = share[kind] ?? 0;
      return { kind, word: look?.word ?? kind.replaceAll("_", " "), tone: look?.tone ?? "neutral", fill: look?.fill ?? "tint", fraction, percent: `${fixed(fraction * 100, 1)}%`, apart: kind === "person" };
    });
}

// Pick the value of an address's parameter that a control offers, or the default.
export function pick<T extends string>(choices: readonly { value: T }[], given: string | null, otherwise: T): T {
  return choices.find((choice) => choice.value === given)?.value ?? otherwise;
}

export type Scope = "project" | "all";

// The first column's name for each grouping, and what a group of it is called in words.
export const groupName: Record<string, string> = {
  kind: "Kind",
  size: "Size",
  agent: "Roster agent",
  repository: "Repository",
  [byProject]: "Project",
};

const capitalized = (text: string) => text.charAt(0).toUpperCase() + text.slice(1);

// labelOf names a group by its key: all work for no key, a kind or a size as words, and anything else, a roster
// agent, a repository, or a project, as it is named.
export function labelOf(by: string, key: string): string {
  if (key === "") {
    return "All work";
  }
  return by === "kind" || by === "size" ? capitalized(key.replaceAll("_", " ")) : key;
}

// finished says no work reached an end in the window: nothing is done, failed, or canceled in any group.
export function finished(groups: DeliveryGroup[]): boolean {
  return groups.some((group) => group.done + group.failed + group.canceled > 0);
}

// The weeks of a trend by the day each began, UTC, as the command prints them.
export const weekDate = (week: DeliveryWeek) => new Date(week.from).toISOString().slice(0, 10);
