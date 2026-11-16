import type { Forecast, LowerBound, PlanForecast } from "../api/measures";
import { goSpan, usd, utcDate } from "./format";

// What a forecast says, in the words `ropeshift plan forecast` prints. A date or a cost that is a lower bound
// reads "or later" and "at least"; the lower bound is the forecast's own, and the explanation is the text the
// API gives every surface.

export type ForecastWords = {
  // The plan's own forecast, or a milestone's by its ID.
  whose: string;
  // A forecast with no dates says why in a word, such as "no history", and one with dates has the three below.
  status: string;
  p50: string;
  p85: string;
  cost: string;
  // The what-ifs, each a sentence that begins with a capital.
  whatIfs: string[];
  explanation: string;
  // What makes the forecast again.
  basis: string;
};

const capitalized = (text: string) => text.charAt(0).toUpperCase() + text.slice(1);

// opened says which dates and costs of a forecast are lower bounds, as the command does: `all` makes the p50 and the
// p85 so, `p85` the p85 alone, and `none` or no word at all neither.
export function opened(bound: LowerBound | undefined): { everything: boolean; p85: boolean } {
  return { everything: bound === "all", p85: bound === "p85" || bound === "all" };
}

export function wordsOf(f: Forecast): ForecastWords {
  const { everything, p85 } = opened(f.lower_bound);
  const lanes = f.cap > 0 ? `at most ${f.cap} lanes` : "no lane cap";
  const whatIfs: string[] = [];
  const dated = f.status === "forecast";
  if (dated) {
    if (f.cap > 0) {
      whatIfs.push(`With one more lane, p85 ${utcDate(f.end_p85_cap_plus_one, p85)}`);
    }
    whatIfs.push(`With each wait for a person cut to ${goSpan(f.person_median_ms)}, p85 ${utcDate(f.end_p85_person_median, p85)}`);
  }
  return {
    whose: f.milestone_id ? `milestone ${f.milestone_id}` : "plan",
    status: f.status.replaceAll("_", " "),
    p50: dated ? `p50 ${utcDate(f.end_p50, everything)}` : "",
    p85: dated ? `p85 ${utcDate(f.end_p85, p85)}` : "",
    cost: dated ? `cost p50 ${usd(f.cost_p50_usd, everything)} p85 ${usd(f.cost_p85_usd, p85)}` : "",
    whatIfs,
    explanation: f.explanation,
    basis: `seed ${f.seed}, ${f.trials} trials, ${lanes}, revision ${f.ledger_rev}, as of ${utcDate(f.as_of, false)}`,
  };
}

// The sentence for a version with no forecast: the tick has not forecast it yet, which a version the tick
// kept nothing for reads as too, or a newer version replaced it first. Any other state has forecasts to show.
export function absenceOf(view: PlanForecast): string {
  if (view.state === "superseded") {
    return `No forecast: a newer version replaced version ${view.plan_version} before the daemon's forecast tick forecast it`;
  }
  if (view.state === "pending" || view.forecasts.length === 0) {
    return capitalized(`forecast pending: the daemon's forecast tick has not forecast version ${view.plan_version} yet`);
  }
  return "";
}
