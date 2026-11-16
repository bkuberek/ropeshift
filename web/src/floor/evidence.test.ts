import { describe, expect, it } from "vitest";

import type { Segment, SegmentKind, WorkTimeline } from "../api/types";
import { golden } from "../testing";
import { roundsOf, segmentKinds, segmentWord, spanLabel, totalsOf } from "./evidence";

const segment = (kind: SegmentKind, from: number, to: number, over: Partial<Segment> = {}): Segment => ({ kind, state: "running", round: 1, from, to, ...over });

const minute = 60_000;

describe("the totals of a timeline", () => {
  it("never count a wait for a person as agent time", () => {
    const agent = [segment("working", 0, 10 * minute), segment("review", 10 * minute, 14 * minute)];
    const without = totalsOf({ work_order: "wo", version: 1, start: 0, end: 14 * minute, segments: agent });
    const waits = [...agent, segment("person", 14 * minute, 74 * minute, { state: "acceptance" })];
    const withWait = totalsOf({ work_order: "wo", version: 1, start: 0, end: 74 * minute, segments: waits });
    expect(without.person).toBe(0);
    expect(withWait.person).toBe(60 * minute);
    expect(withWait.agent).toBe(without.agent);
    expect(withWait.agent).toBe(14 * minute);
    expect(withWait.agent + withWait.person + withWait.other).toBe(withWait.elapsed);
  });

  it("hold what an agent did apart from every wait, whichever kind the wait is", () => {
    const kinds = Object.keys(segmentKinds) as SegmentKind[];
    let at = 0;
    const segments = kinds.map((kind) => segment(kind, at, (at += minute)));
    const totals = totalsOf({ work_order: "wo", version: 1, start: 0, end: at, segments });
    expect(totals.person).toBe(minute);
    expect(totals.agent).toBe(3 * minute);
    expect(totals.other).toBe((kinds.length - 4) * minute);
    expect(totals.elapsed).toBe(kinds.length * minute);
  });

  it("read the person time of an ended work order's delivery record", () => {
    const ended = golden<WorkTimeline>("timeline-ended");
    const totals = totalsOf(ended.timeline);
    expect(totals.person).toBe(ended.record?.person_ms);
    expect(totals.elapsed).toBe(ended.record?.elapsed_ms);
    expect(totals.agent).toBe((ended.record?.by_kind.working ?? 0) + (ended.record?.by_kind.review ?? 0) + (ended.record?.by_kind.ladder ?? 0));
    expect(totals.agent).not.toBe(totals.agent + totals.person);
  });

  it("read nothing from a work order that never reached ready", () => {
    expect(totalsOf(golden<WorkTimeline>("timeline-empty").timeline)).toMatchObject({ elapsed: 0, agent: 0, person: 0, other: 0, kinds: [] });
  });

  it("list each kind once, in the order the work first spent time on it", () => {
    const totals = totalsOf({
      work_order: "wo",
      version: 1,
      start: 0,
      end: 6 * minute,
      segments: [segment("queued", 0, minute), segment("working", minute, 3 * minute), segment("person", 3 * minute, 4 * minute), segment("working", 4 * minute, 6 * minute)],
    });
    expect(totals.kinds).toEqual([
      ["queued", minute],
      ["working", 4 * minute],
      ["person", minute],
    ]);
  });
});

describe("the words of a segment", () => {
  it("name the cause of a queued wait and the reason of a hold", () => {
    expect(segmentWord(segment("queued", 0, 1, { cause: "lane_cap" }))).toBe("Queued: lane cap");
    expect(segmentWord(segment("queued", 0, 1, { cause: "dispatch" }))).toBe("Queued: not claimed");
    expect(segmentWord(segment("queued", 0, 1))).toBe("Queued");
    expect(segmentWord(segment("held", 0, 1, { cause: "replanning" }))).toBe("Held: replanning");
    expect(segmentWord(segment("person", 0, 1))).toBe("Waiting for a person");
    expect(segmentWord(segment("working", 0, 1))).toBe("Agent working");
  });

  it("have a word, a tone, and a fill for every kind the ledger derives", () => {
    for (const [kind, look] of Object.entries(segmentKinds)) {
      expect(look.word, kind).not.toBe("");
      expect(look.fill, kind).toMatch(/^(tint|hatch|solid|dots|lines)$/);
    }
    expect(segmentKinds.person.tone).toBe("warning");
    expect(segmentKinds.person.agent).toBe(false);
  });
});

describe("the rounds of a timeline", () => {
  it("group segments by round, in order, and name a later round a rework", () => {
    const rounds = roundsOf([segment("working", 0, 1), segment("person", 1, 2, { round: 1 }), segment("working", 2, 3, { round: 2 })]);
    expect(rounds.map((round) => [round.round, round.label, round.segments.length])).toEqual([
      [1, "Round 1", 2],
      [2, "Round 2, rework", 1],
    ]);
  });
});

describe("a span of time", () => {
  it("reads in the two largest units it has", () => {
    expect(spanLabel(0)).toBe("0s");
    expect(spanLabel(45_000)).toBe("45s");
    expect(spanLabel(8 * minute)).toBe("8m");
    expect(spanLabel(8 * minute + 30_000)).toBe("8m 30s");
    expect(spanLabel(6 * 60 * minute + 10 * minute)).toBe("6h 10m");
    expect(spanLabel(26 * 60 * minute)).toBe("1d 2h");
  });
});
