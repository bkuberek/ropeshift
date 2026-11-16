import type { Forecast, PlanForecast } from "../api/measures";
import { Badge } from "../ui/display";
import { absenceOf, wordsOf } from "./forecastWords";

// One forecast: the Low confidence badge before Forecast when the estimates it rests on are few, its dates and
// cost at p50 and p85, its what-ifs, the explanation it was made with, and what makes it again. A forecast with
// no dates says why in a word. On a summary, the what-ifs and what makes it again wait under one control, and
// the explanation never does.
export function ForecastLine({ forecast, title = "", summary = false }: { forecast: Forecast; title?: string; summary?: boolean }) {
  const words = wordsOf(forecast);
  const more = (
    <>
      {words.whatIfs.length > 0 ? (
        <ul className="forecast-ifs" aria-label={`What ifs of ${title || words.whose}`}>
          {words.whatIfs.map((one) => (
            <li key={one}>{one}</li>
          ))}
        </ul>
      ) : null}
      <p className="small muted forecast-basis">{words.basis}</p>
    </>
  );
  return (
    <div className="forecast" data-forecast={words.whose}>
      {title ? <p className="forecast-title">{title}</p> : null}
      <p className="forecast-range">
        {forecast.confidence === "low" ? <Badge tone="warning" glyph="triangle">Low confidence</Badge> : null}{" "}
        <span className="forecast-label">Forecast</span>{" "}
        {forecast.status === "forecast" ? (
          <>
            <span>{words.p50}</span> <span>{words.p85}</span> <span>{words.cost}</span>
          </>
        ) : (
          <span>{words.status}</span>
        )}
      </p>
      <p className="small muted forecast-explanation">{words.explanation}</p>
      {summary ? (
        <details className="forecast-more">
          <summary>What-ifs and how it was made</summary>
          {more}
        </details>
      ) : (
        more
      )}
    </div>
  );
}

// PlanForecastView says a plan version's forecasts: the plan's own, and each milestone's by its title. A version
// the daemon's forecast tick has not forecast says so in one line, and one a newer version replaced says it has
// none.
export function PlanForecastView({ forecast, titleOf, summary = false }: { forecast: PlanForecast; titleOf: (milestone: string) => string; summary?: boolean }) {
  const absent = absenceOf(forecast);
  if (absent) {
    return <p className="forecast-state muted">{absent}</p>;
  }
  return (
    <ul className="forecasts" aria-label={`Forecasts of version ${forecast.plan_version}`}>
      {forecast.forecasts.map((one) => (
        <li key={one.milestone_id ?? "plan"}>
          <ForecastLine forecast={one} title={one.milestone_id ? titleOf(one.milestone_id) : "Plan"} summary={summary} />
        </li>
      ))}
    </ul>
  );
}
