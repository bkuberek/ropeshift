import type { PlanForecast } from "../api/measures";
import { EstimateBand } from "./Estimate";
import { PlanForecastView } from "./Forecast";

// tallyOf says how many of a version's estimates are confident, low confidence, and absent, which the API
// returns as the estimates of the set and this counts, one by one.
function tallyOf(forecast: PlanForecast): string {
  const estimates = forecast.estimate_set?.estimates ?? [];
  const absent = estimates.filter((one) => one.status === "no_history").length;
  const low = estimates.filter((one) => one.confidence === "low").length;
  const confident = estimates.filter((one) => one.confidence === "confident").length;
  return `${estimates.length} ${estimates.length === 1 ? "estimate" : "estimates"}: ${confident} confident, ${low} low confidence, ${absent} with no history.`;
}

// PlanEstimates is a plan version's estimates and forecasts as its approval shows them, and as the approval
// records them as the baseline: how many estimates are confident, low confidence, and absent, the ledger
// revision they read, each work order's band, and the plan's and each milestone's forecast with its what-ifs and
// explanation. A version the forecast tick has not forecast says so, and still shows the estimates it made. Where
// space is short, each band holds its explanation under a control.
export function PlanEstimates({ forecast, titleOf, compact = false }: { forecast: PlanForecast; titleOf: (milestone: string) => string; compact?: boolean }) {
  const set = forecast.estimate_set;
  return (
    <div className="stack-8 plan-estimates">
      {set && set.estimates.length > 0 ? (
        <>
          <p className="estimate-tally">{tallyOf(forecast)}</p>
          <p className="small muted">The estimates read ledger revision {set.ledger_rev}.</p>
        </>
      ) : (
        <p className="muted">No estimates were made for this version.</p>
      )}
      <PlanForecastView forecast={forecast} titleOf={titleOf} summary={compact} />
      {set && set.estimates.length > 0 ? (
        <ul className="estimates" aria-label={`Estimates of version ${forecast.plan_version}`}>
          {set.estimates.map((one) => (
            <li key={one.work_order_id ?? one.id} data-estimate={one.work_order_id}>
              <p className="mono small estimate-work">{one.work_order_id}</p>
              <EstimateBand estimate={one} compact={compact} />
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
