import type { CalibrationRate, CalibrationShare, MedianRatio, MilestoneResult } from "../api/measures";
import { fixed, utcDate } from "./format";

// How estimate calibration says a rate, a ratio, and a share. Each reads as `ropeshift measures estimates` prints
// it, so a number and its words are the same on either surface. Nothing here computes a rate: a rate the API
// leaves out, because fewer than 10 outcomes stand behind it, is said as its counts and no rate.

// The value a calibrated estimator's p50 and p85 hit rates come near. The tick of a rate's meter marks it.
export const calibrated = { p50: 0.5, p85: 0.85 } as const;

// percent says a share to a tenth of a percent.
const percent = (share: number) => `${fixed(share * 100, 1)}%`;

// hitsText says how often something held: 7 of 17 41.2%, or 1 of 5, no rate.
export const hitsText = (rate: CalibrationRate) => (rate.rate === undefined ? `${rate.hits} of ${rate.n}, no rate` : `${rate.hits} of ${rate.n} ${percent(rate.rate)}`);

// ratioText says the median ratio of actual to p50: 1.20 of 17, or no median of 5.
export const ratioText = (ratio: MedianRatio) => (ratio.median === undefined ? `no median of ${ratio.n}` : `${fixed(ratio.median, 2)} of ${ratio.n}`);

// shareText says a count of ended work and its share of all that ended, which is absent under 10 ended.
export const shareText = (share: CalibrationShare) => (share.share === undefined ? `${share.count}` : `${share.count} ${percent(share.share)}`);

// confidenceText says the confidence of a milestone's forecast, none when it had no confidence.
export const confidenceText = (result: MilestoneResult) => result.confidence ?? "none";

// forecastDates says a milestone forecast's two dates, as the command does: the p50 date is a lower bound when all
// of the forecast is, and the p85 date unless the forecast says none of it is.
export function forecastDates(result: MilestoneResult): { p50: string; p85: string } | null {
  if (!result.forecast_p50) {
    return null;
  }
  return { p50: utcDate(result.forecast_p50, result.lower_bound === "all"), p85: utcDate(result.forecast_p85 ?? 0, result.lower_bound !== "none") };
}

// comparedText says whether a milestone ended at or before a forecast date, or that nothing compares.
export function comparedText(held: boolean | null | undefined): string {
  if (typeof held !== "boolean") {
    return "not compared";
  }
  return held ? "within" : "past";
}
