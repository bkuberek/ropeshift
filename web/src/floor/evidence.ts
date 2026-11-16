import type { ChangeSize, Segment, SegmentKind, Standing, TimelineRead } from "../api/types";
import type { GlyphName } from "../ui/Icon";
import type { Tone } from "./states";

// How a segment is drawn, besides its color, so no kind reads by color alone (WCAG 1.4.1).
export type Fill = "tint" | "hatch" | "solid" | "dots" | "lines";

// What a segment kind is called, how it looks, and whether an agent worked in it. A wait for a person always
// has its own word, tone, and total, and never counts as agent time (FR-EST-2).
export type KindLook = { word: string; tone: Tone; glyph: GlyphName; fill: Fill; agent: boolean };

export const segmentKinds: Record<SegmentKind, KindLook> = {
  queued: { word: "Queued", tone: "neutral", glyph: "ring", fill: "tint", agent: false },
  held: { word: "Held", tone: "neutral", glyph: "pause", fill: "hatch", agent: false },
  working: { word: "Agent working", tone: "active", glyph: "dot", fill: "solid", agent: true },
  ladder: { word: "Orchestrator answering", tone: "active", glyph: "half", fill: "dots", agent: true },
  review: { word: "In review", tone: "active", glyph: "target", fill: "hatch", agent: true },
  integration: { word: "Checks and merge", tone: "active", glyph: "arrow", fill: "lines", agent: false },
  person: { word: "Waiting for a person", tone: "warning", glyph: "person", fill: "solid", agent: false },
  usage_limit: { word: "Usage limit", tone: "warning", glyph: "triangle", fill: "hatch", agent: false },
  external: { word: "External wait", tone: "neutral", glyph: "slash", fill: "dots", agent: false },
  stuck: { word: "Stuck", tone: "danger", glyph: "cross", fill: "hatch", agent: false },
};

const queuedCauses: Record<string, string> = { lane_cap: "lane cap", dispatch: "not claimed" };

// segmentWord is a segment's kind said as the design names it: a queued wait with its cause, and a hold with
// its reason.
export function segmentWord(segment: Segment): string {
  const word = segmentKinds[segment.kind]?.word ?? segment.kind;
  if (!segment.cause) {
    return word;
  }
  if (segment.kind === "queued") {
    return `${word}: ${queuedCauses[segment.cause] ?? segment.cause.replaceAll("_", " ")}`;
  }
  return segment.kind === "held" ? `${word}: ${segment.cause.replaceAll("_", " ")}` : word;
}

export type Totals = {
  // elapsed is the time from the first ready to the end, or to now while the work has not ended.
  elapsed: number;
  // agent is the time an agent worked: its lane, the orchestrator answering it, and its review.
  agent: number;
  // person is the time spent waiting for a person, which agent never holds.
  person: number;
  // other is every other wait: the queue, holds, checks and merge, usage limits, external waits, and being stuck.
  other: number;
  // kinds is the time in each kind, in the order the work first spent time on it.
  kinds: [SegmentKind, number][];
};

// totalsOf sums a timeline's segments. A segment is in exactly one of agent, person, and other, so the three
// add to elapsed and a wait for a person never adds to agent time.
export function totalsOf(timeline: TimelineRead): Totals {
  const totals: Totals = { elapsed: 0, agent: 0, person: 0, other: 0, kinds: [] };
  const byKind = new Map<SegmentKind, number>();
  for (const segment of timeline.segments) {
    const length = Math.max(0, segment.to - segment.from);
    totals.elapsed += length;
    byKind.set(segment.kind, (byKind.get(segment.kind) ?? 0) + length);
    if (segment.kind === "person") {
      totals.person += length;
    } else if (segmentKinds[segment.kind]?.agent) {
      totals.agent += length;
    } else {
      totals.other += length;
    }
  }
  totals.kinds = [...byKind];
  return totals;
}

export type Round = { round: number; label: string; segments: Segment[] };

// roundsOf groups segments by the rework round they belong to, in order. Round 1 is the first attempt at review,
// and each rework after it starts another.
export function roundsOf(segments: Segment[]): Round[] {
  const rounds: Round[] = [];
  for (const segment of segments) {
    let found = rounds.find((round) => round.round === segment.round);
    if (!found) {
      found = { round: segment.round, label: segment.round > 1 ? `Round ${segment.round}, rework` : `Round ${segment.round}`, segments: [] };
      rounds.push(found);
    }
    found.segments.push(segment);
  }
  return rounds;
}

const second = 1000;
const minute = 60 * second;
const hour = 60 * minute;
const day = 24 * hour;

// spanLabel says a length of time in its two largest units, as "6h 10m" or "45s".
export function spanLabel(ms: number): string {
  const units: [number, string][] = [
    [day, "d"],
    [hour, "h"],
    [minute, "m"],
    [second, "s"],
  ];
  const at = units.findIndex(([size]) => ms >= size);
  if (at < 0) {
    return "0s";
  }
  const [size, name] = units[at] as [number, string];
  const lead = Math.floor(ms / size);
  const next = units[at + 1];
  const rest = next ? Math.floor((ms % size) / next[0]) : 0;
  return rest > 0 && next ? `${lead}${name} ${rest}${next[1]}` : `${lead}${name}`;
}

// spanWords says the same length of time in words a screen reader reads well.
export function spanWords(ms: number): string {
  const parts: string[] = [];
  const add = (amount: number, unit: string) => {
    if (amount > 0) {
      parts.push(`${amount} ${unit}${amount === 1 ? "" : "s"}`);
    }
  };
  add(Math.floor(ms / day), "day");
  add(Math.floor((ms % day) / hour), "hour");
  add(Math.floor((ms % hour) / minute), "minute");
  if (ms < hour) {
    add(Math.floor((ms % minute) / second), "second");
  }
  return parts.join(" ") || "0 seconds";
}

// standingWords say where a criterion stands in the acceptance table. Work that waits for acceptance names what a
// criterion needs, which a member decides.
export const standingWords: Record<string, string> = {
  covered: "Covered by a check",
  failed: "A check failed",
  second_principal: "Only the lane said so",
  member: "No check covered it",
};

export const waitingStandingWords: Record<string, string> = { ...standingWords, second_principal: "Needs a member", member: "Needs a member" };

// changeWords says what a branch changed as `ropeshift work acceptance` records it: the files and the lines.
export function changeWords(change: ChangeSize): string {
  return `${change.files} file${change.files === 1 ? "" : "s"} changed, +${change.added} \u2212${change.removed}`;
}

const reviewOpen = new Set(["ready", "claimed", "running", "parked", "lapsed", "blocked"]);
const ended = new Set(["done", "failed", "canceled"]);

// leftToDo says, in the order the ledger gives them, what a work order's evidence leaves to do. Every line is the
// acceptance table's own answer in words: the page decides nothing the ledger did not.
export function leftToDo(standing: Standing): string[] {
  const lines: string[] = [];
  if (standing.state === "done") {
    return ["This work order is done. Nothing is left to do."];
  }
  if (ended.has(standing.state)) {
    lines.push(`This work order ended as ${standing.state}.`);
  } else {
    if (standing.why) {
      lines.push(`It waits: ${standing.why}.`);
    }
    if ((standing.reviews ?? []).some((review) => reviewOpen.has(review.state))) {
      lines.push("A review of this work has not ended.");
    }
  }
  for (const name of standing.assessment.failed ?? []) {
    lines.push(`A check failed: ${name}.`);
  }
  if (ended.has(standing.state)) {
    return lines;
  }
  if (standing.assessment.needs === "second_principal") {
    lines.push("Only the lane says a criterion holds. A check the lane did not write, or a member, must confirm it.");
  } else if (standing.assessment.needs === "member") {
    lines.push("A member decides: no check covers a criterion, a check failed, or the work has no criteria yet.");
  } else if (!(standing.assessment.failed ?? []).length) {
    lines.push("Every criterion has a check the lane did not write, and none failed.");
  }
  return lines;
}
