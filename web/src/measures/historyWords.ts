import type { HistoryGroup } from "../api/measures";
import { fixed } from "./format";

// What a group of the delivery history says, in the words `ropeshift measures history` prints and the planning
// session's pack holds. An open p85 reads "more than" its figure, which is the longest record's, and a cost that
// holds a lower bound reads "at least".

// durationWords says a duration as a person reads it: hours and minutes from an hour on, minutes and seconds from a
// minute on, and seconds below. It rounds to the second, and from an hour on to the minute, before it splits into
// units, so a rounded unit carries into the next.
export function durationWords(ms: number): string {
  const seconds = Math.floor((ms + 500) / 1000);
  if (seconds >= 3600) {
    const minutes = Math.floor((seconds + 30) / 60);
    return minutes % 60 === 0 ? `${minutes / 60}h` : `${Math.floor(minutes / 60)}h${minutes % 60}m`;
  }
  if (seconds >= 60) {
    return seconds % 60 === 0 ? `${seconds / 60}m` : `${Math.floor(seconds / 60)}m${seconds % 60}s`;
  }
  return `${seconds}s`;
}

export type HistoryWords = {
  // The group's label: its kind and size, then its capability class and repository where the grouping has them.
  label: string;
  finished: string;
  delivery: string;
  lane: string;
  cost: string;
  rounds: string;
  change: string;
  explanation: string;
};

export function historyWords(g: HistoryGroup, repositories: Record<string, string>): HistoryWords {
  const label = [g.kind, g.size, g.capability_class, g.repository ? repositories[g.repository] || g.repository : ""].filter(Boolean).join(", ");
  const more = g.p85_open ? "more than " : "";
  const bound = g.cost_lower_bound ? "at least " : "";
  const dollars = (usd: number) => `${bound}$${fixed(usd, 2)}`;
  return {
    label,
    finished: `${g.count} finished`,
    delivery: `delivery p50 ${durationWords(g.delivery.p50)}, p85 ${more}${durationWords(g.delivery.p85)}`,
    lane: `lane time p50 ${durationWords(g.lane.p50)}, p85 ${more}${durationWords(g.lane.p85)}`,
    cost: `cost p50 ${dollars(g.cost.p50)}, p85 ${more}${dollars(g.cost.p85)}`,
    rounds: `mean ${fixed(g.mean_rounds, 1)} rounds`,
    change: g.change ? `median change ${g.change.files} files, +${g.change.added} -${g.change.removed} lines, of ${g.change.measured} measured` : "no change size recorded",
    explanation: g.explanation,
  };
}
