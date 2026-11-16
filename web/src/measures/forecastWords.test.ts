import { describe, expect, it } from "vitest";

import type { Forecast, LowerBound, MilestoneResult } from "../api/measures";
import { forecastDates } from "./calibration";
import { wordsOf } from "./forecastWords";

// Which dates of a forecast are lower bounds is the forecast's own word: `all` makes the p50 and the p85 so, `p85`
// the p85 alone, and `none`, or no word at all, neither. The command prints the same, and the outcome that compares
// a forecast reads an unstated bound as none.

const forecast = (bound: LowerBound | undefined): Forecast => ({
  plan_id: "plan-1",
  milestone_id: "m1",
  status: "forecast",
  ledger_rev: 9,
  seed: "sha256:seed",
  trials: 2000,
  cap: 2,
  as_of: 1_763_100_000_000,
  person_median_ms: 1_800_000,
  policy_version: 1,
  end_p50: 1_763_200_000_000,
  end_p85: 1_763_300_000_000,
  cost_p50_usd: 3.5,
  cost_p85_usd: 6,
  end_p85_cap_plus_one: 1_763_250_000_000,
  end_p85_person_median: 1_763_280_000_000,
  confidence: "confident",
  low_confidence_work_orders: [],
  left_out: [],
  outside: [],
  lower_bound: bound,
  explanation: "From 2000 trials.",
});

const result = (bound: LowerBound | undefined): MilestoneResult => ({
  plan_id: "plan-1",
  milestone_id: "m1",
  outcome: "done",
  forecast_p50: 1_763_200_000_000,
  forecast_p85: 1_763_300_000_000,
  lower_bound: bound,
  actual_end: 1_763_250_000_000,
  at_or_below_p50: false,
  at_or_below_p85: true,
});

describe("a forecast's words", () => {
  it.each<[LowerBound | undefined, string, string]>([
    ["all", "p50 2025-11-15 09:46 UTC or later", "p85 2025-11-16 13:33 UTC or later"],
    ["p85", "p50 2025-11-15 09:46 UTC", "p85 2025-11-16 13:33 UTC or later"],
    ["none", "p50 2025-11-15 09:46 UTC", "p85 2025-11-16 13:33 UTC"],
    [undefined, "p50 2025-11-15 09:46 UTC", "p85 2025-11-16 13:33 UTC"],
  ])("with the bound %s says its dates and what-ifs", (bound, p50, p85) => {
    const words = wordsOf(forecast(bound));
    expect(words.p50).toBe(p50);
    expect(words.p85).toBe(p85);
    expect(words.whatIfs[0]?.endsWith("or later")).toBe(p85.endsWith("or later"));
    expect(words.cost.includes("at least $6.00")).toBe(p85.endsWith("or later"));
  });
});

describe("a milestone result's forecast dates", () => {
  it.each<[LowerBound | undefined, string, string]>([
    ["all", "2025-11-15 09:46 UTC or later", "2025-11-16 13:33 UTC or later"],
    ["p85", "2025-11-15 09:46 UTC", "2025-11-16 13:33 UTC or later"],
    ["none", "2025-11-15 09:46 UTC", "2025-11-16 13:33 UTC"],
    [undefined, "2025-11-15 09:46 UTC", "2025-11-16 13:33 UTC"],
  ])("with the bound %s says its p50 and p85", (bound, p50, p85) => {
    expect(forecastDates(result(bound))).toEqual({ p50, p85 });
  });
});
