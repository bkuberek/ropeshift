import { describe, expect, it } from "vitest";

import { fixed, goSpan, span, usd, utcDate } from "./format";

describe("goSpan", () => {
  it("says a stretch as the command line does, to the second", () => {
    expect(goSpan(0)).toBe("0s");
    expect(goSpan(45_000)).toBe("45s");
    expect(goSpan(1_800_000)).toBe("30m0s");
    expect(goSpan(5_400_000)).toBe("1h30m0s");
    expect(goSpan(7_200_000)).toBe("2h0m0s");
    expect(goSpan(90_061_000)).toBe("25h1m1s");
  });

  it("rounds to the nearest second, a half up", () => {
    expect(goSpan(1_499)).toBe("1s");
    expect(goSpan(1_500)).toBe("2s");
    expect(goSpan(59_600)).toBe("1m0s");
  });
});

describe("utcDate", () => {
  it("says a time to the minute in UTC", () => {
    expect(utcDate(1_763_200_000_000, false)).toBe("2025-11-15 09:46 UTC");
  });

  it("says a lower bound as or later", () => {
    expect(utcDate(1_763_300_000_000, true)).toBe("2025-11-16 13:33 UTC or later");
  });
});

describe("usd", () => {
  it("says a cost in dollars and cents, and a lower bound as at least", () => {
    expect(usd(3.5, false)).toBe("$3.50");
    expect(usd(6, true)).toBe("at least $6.00");
    expect(usd(0.004, false)).toBe("$0.00");
  });

  it("rounds a cost that falls exactly between two cents to the even one, as the command line does", () => {
    expect(usd(0.125, false)).toBe("$0.12");
    expect(usd(0.375, false)).toBe("$0.38");
    expect(usd(0.005, false)).toBe("$0.01");
    expect(usd(2.675, false)).toBe("$2.67");
  });
});

describe("span", () => {
  it("says a stretch in its two largest units", () => {
    expect(span(0)).toBe("0s");
    expect(span(45_000)).toBe("45s");
    expect(span(2_700_000)).toBe("45m");
    expect(span(5_400_000)).toBe("1h 30m");
    expect(span(54_000_000)).toBe("15h");
    expect(span(93_600_000)).toBe("1d 2h");
    expect(span(172_800_000)).toBe("2d");
  });

  it("rounds to the minute from a minute up", () => {
    expect(span(89_999)).toBe("1m");
    expect(span(3_599_000)).toBe("1h");
  });
});

describe("fixed", () => {
  it("writes a number to some places, and a number exactly between two to the even one, as the command line does", () => {
    expect(fixed(1.6333333333333333, 1)).toBe("1.6");
    expect(fixed(0.25, 1)).toBe("0.2");
    expect(fixed(0.75, 1)).toBe("0.8");
    expect(fixed(1.25, 1)).toBe("1.2");
    expect(fixed(0.05, 1)).toBe("0.1");
    expect(fixed(59.9, 1)).toBe("59.9");
    expect(fixed(2, 1)).toBe("2.0");
  });
});
