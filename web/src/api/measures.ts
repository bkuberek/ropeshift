import { ApiError, call, inProjectOf, project } from "./client";
import type { CIDurations, Project } from "./types";

// What the delivery measures and the forecasts read, as the API serves them. Durations are milliseconds, and
// dates epoch milliseconds. A duration or a cost with no work done is absent, never zero.

export type Quantiles = { p50: number; p85: number };

export type DeliveryWeek = { from: number; done: number; delivery?: Quantiles };

// One group of delivery records. Only the work that reached `done` counts in the durations, the cost, the
// throughput, and the share of elapsed time in each kind of segment.
export type DeliveryGroup = {
  key: string;
  done: number;
  failed: number;
  canceled: number;
  elapsed?: Quantiles;
  delivery?: Quantiles;
  cost?: Quantiles;
  cost_lower_bound?: boolean;
  per_week: number;
  share: Record<string, number>;
  trend: DeliveryWeek[];
};

// LookBack is delivery over a window, for all work first (the group with no key), and then each group in the
// order of its key.
export type LookBack = { by: string; from: number; to: number; groups: DeliveryGroup[] };

// The groupings one project's records read by, the default first, as the command's --by names them.
export const deliveryGroupings = [
  { value: "kind", label: "Kind" },
  { value: "size", label: "Size" },
  { value: "agent", label: "Roster agent" },
  { value: "repository", label: "Repository" },
] as const;

export type DeliveryGrouping = (typeof deliveryGroupings)[number]["value"];

// The grouping of every active project, one group each. Percentiles of separate projects do not combine, so
// no other grouping reads across projects.
export const byProject = "project";

export type EstimateStatus = "estimated" | "no_history";

// One work order's band of delivery time and estimated cost, and what it rests on. The explanation is the text
// every surface shows. An open band has no p85 of its own: the p85 it carries is the longest record's, and
// `p85_open` says it reads as more than that. A cost that holds a lower bound reads as at least.
export type Estimate = {
  id?: string;
  estimate_set_id?: string;
  work_order_id?: string;
  status: EstimateStatus;
  confidence?: "confident" | "low";
  tier?: number;
  keys: string[];
  dropped_keys: string[];
  sample_count: number;
  min_samples: number;
  sample_ids: string[];
  pool_samples: number;
  delivery_p50_ms: number;
  delivery_p85_ms: number;
  delivery_min_ms: number;
  delivery_max_ms: number;
  cost_p50_usd: number;
  cost_p85_usd: number;
  cost_min_usd: number;
  cost_max_usd: number;
  p85_open: boolean;
  cost_at_least: boolean;
  rev: number;
  estimator_version: number;
  policy_version: number;
  explanation: string;
  current: boolean;
  created_at?: number;
};

// The estimates made together at one ledger revision: the open work of a plan version, or one work order made
// outside a plan.
export type EstimateSet = {
  id: string;
  plan_id?: string;
  plan_version?: number;
  ledger_rev: number;
  estimator_version: number;
  policy_version: number;
  digest: string;
  created_at: number;
  estimates: Estimate[];
};

// The estimate a work order's baseline holds, which its finished work is measured against: the first one recorded,
// by a plan approval that showed it, or by the work when it was made outside a plan, with no approval.
export type WorkBaseline = { id: string; approval_id?: string; recorded_at: number; estimate: Estimate };

// WorkEstimate is a work order's current estimate, or none when none was made, and its baseline, or none before one
// is recorded.
export type WorkEstimate = { estimate: Estimate | null; baseline: WorkBaseline | null };

export type ForecastStatus = "forecast" | "no_history" | "no_capacity" | "finished";

// LowerBound says which of a forecast's results have no upper bound: all of them, or the p85 alone.
export type LowerBound = "none" | "p85" | "all";

// One forecast of a plan's work, or of a milestone's, with what makes it again. The explanation is the text
// every surface shows.
export type Forecast = {
  id?: string;
  estimate_set_id?: string;
  plan_id: string;
  plan_version?: number;
  milestone_id?: string;
  status: ForecastStatus;
  ledger_rev: number;
  seed: string;
  trials: number;
  cap: number;
  as_of: number;
  person_median_ms: number;
  policy_version: number;
  end_p50: number;
  end_p85: number;
  cost_p50_usd: number;
  cost_p85_usd: number;
  end_p85_cap_plus_one: number;
  end_p85_person_median: number;
  confidence?: "confident" | "low";
  low_confidence_work_orders: string[];
  left_out: string[];
  outside: string[];
  lower_bound?: LowerBound;
  explanation: string;
  inputs_digest?: string;
  day?: number;
  created_at?: number;
};

// A plan version's forecast: pending until the daemon's forecast tick forecasts the version, forecast once it
// has, and superseded when a newer version replaced it first.
export type ForecastState = "pending" | "forecast" | "superseded";

export type PlanForecast = {
  plan_id: string;
  plan_version: number;
  state: ForecastState;
  estimate_set: EstimateSet | null;
  forecasts: Forecast[];
};

// Estimate calibration: how often finished work landed inside the estimates it was baselined with. A rate or a
// median is absent where fewer than 10 outcomes stand behind it, and its count stays.

// CalibrationRate is how often something held among the outcomes that could be compared: n of them were, and
// hits of those held.
export type CalibrationRate = { n: number; hits: number; rate?: number };

export type MedianRatio = { n: number; median?: number };

// Comparisons are what one quantity, delivery time or cost, did against its baseline: the share at or below
// the p50 band, the share at or below the p85 band, and the median ratio of actual to p50.
export type Comparisons = { p50: CalibrationRate; p85: CalibrationRate; ratio: MedianRatio };

// Standing is the calibration of one group of outcomes. An outcome whose baseline p85 was open counts in
// `p85_open`, in the p50 rate, and in the ratio, and not in the p85 rate.
export type Standing = { outcomes: number; done: number; failed: number; canceled: number; p85_open: number; time: Comparisons; cost: Comparisons };

export type TierStanding = Standing & { tier: number };

export type VersionStanding = Standing & { estimator_version: number };

// Calibrated is one confidence's outcomes over one period: all together, then by tier and by estimator version.
export type Calibrated = { total: Standing; tiers: TierStanding[]; estimator_versions: VersionStanding[] };

// CalibrationShare is a count of ended work and its share of all that ended, absent under 10 ended.
export type CalibrationShare = { count: number; share?: number };

export type ConfidenceShare = { ended: number; confident: CalibrationShare; low: CalibrationShare; none: CalibrationShare };

// CalibrationPeriod is calibration over one stretch of time. Confident is the headline, and low confidence
// stands apart.
export type CalibrationPeriod = { from: number; to: number; share: ConfidenceShare; confident: Calibrated; low: Calibrated };

export type CalibrationWeek = { from: number; confident: Standing; low: Standing };

// CalibrationScope is calibration over one repository's work, or over all of the project's work when the
// repository is absent.
export type CalibrationScope = { repository?: string; recent: CalibrationPeriod; all_time: CalibrationPeriod; weeks: CalibrationWeek[] };

export type MilestoneResult = {
  plan_id: string;
  milestone_id: string;
  confidence?: "confident" | "low";
  outcome: string;
  forecast_p50?: number;
  forecast_p85?: number;
  lower_bound?: LowerBound;
  actual_end: number;
  at_or_below_p50: boolean | null;
  at_or_below_p85: boolean | null;
};

export type MilestoneStanding = { outcomes: number; done: number; failed: number; canceled: number; p85_open: number; p50: CalibrationRate; p85: CalibrationRate };

export type MilestonePeriod = { from: number; to: number; confident: MilestoneStanding; low: MilestoneStanding; none: number };

export type MilestoneCalibration = { recent: MilestonePeriod; all_time: MilestonePeriod; results: MilestoneResult[] };

// Calibration is all of the project's work and then each repository's, over the window from `from` to `to` and
// over all time, and each milestone's forecast beside its actual.
export type Calibration = { from: number; to: number; scopes: CalibrationScope[]; milestones: MilestoneCalibration };

// readDelivery reads look back over a window by a grouping, as `ropeshift measures delivery` does.
export function readDelivery(window: string, by: string): Promise<LookBack> {
  return call("GET", project(`/measures/delivery?${new URLSearchParams({ window, by })}`));
}

// readDeliveryByProject reads every active project's look back apart, each project one group, as the command
// does for `--by project`: it asks each project's route and keeps its group for all its work.
export async function readDeliveryByProject(projects: Project[], window: string): Promise<LookBack> {
  const out: LookBack = { by: byProject, from: 0, to: 0, groups: [] };
  for (const one of projects.filter((row) => row.state === "active")) {
    const report = await inProjectOf(one.name, () => readDelivery(window, "kind"));
    const all = report.groups[0];
    if (all) {
      out.from = report.from;
      out.to = report.to;
      out.groups.push({ ...all, key: one.name });
    }
  }
  return out;
}

// readEstimate reads one work order's current estimate and its baseline, as `ropeshift work show` prints them.
export function readEstimate(id: string): Promise<WorkEstimate> {
  return call("GET", project(`/work-orders/${encodeURIComponent(id)}/estimate`));
}

// readPlanForecast reads the latest forecasts the forecast tick kept for a plan's version, as `ropeshift plan
// forecast` does. No version reads the newest. It forecasts nothing itself.
export function readPlanForecast(plan: string, version = 0): Promise<PlanForecast> {
  return call("GET", project(`/plans/${encodeURIComponent(plan)}/forecast${version ? `?${new URLSearchParams({ version: String(version) })}` : ""}`));
}

// readPlanForecasts reads the newest forecast of each plan, by the plan's ID. A plan with no version has no
// forecast to read, and is left out.
export async function readPlanForecasts(plans: string[]): Promise<Record<string, PlanForecast>> {
  const out: Record<string, PlanForecast> = {};
  await Promise.all(
    plans.map(async (id) => {
      try {
        out[id] = await readPlanForecast(id);
      } catch (error) {
        if (!(error instanceof ApiError && error.code === "not_found")) {
          throw error;
        }
      }
    }),
  );
  return out;
}

export type HistoryQuantiles = { p50: number; p85: number };

// One group of a project's finished work as a planning session reads it: how many work orders it holds, their
// delivery time, lane time, and cost at p50 and p85, the mean rounds, the median change size, and the label and
// explanation an estimate from just these records carries. A group of kind and size alone has no capability class
// or repository. An open p85 is the longest record's, and a cost that holds a lower bound reads as at least.
export type HistoryGroup = {
  kind: string;
  size: string;
  capability_class?: string;
  repository?: string;
  count: number;
  delivery: HistoryQuantiles;
  lane: HistoryQuantiles;
  cost: HistoryQuantiles;
  cost_lower_bound?: boolean;
  p85_open?: boolean;
  mean_rounds: number;
  change?: { measured: number; files: number; added: number; removed: number };
  confidence: "confident" | "low";
  explanation: string;
};

// DeliveryHistory is what a planning session's pack holds of the project's finished work, in two groupings, with each
// repository's name by its ID.
export type DeliveryHistory = {
  min_samples: number;
  recent_samples: number;
  by_kind_size: HistoryGroup[];
  by_kind_size_class_repository: HistoryGroup[];
  repositories: Record<string, string>;
};

// readDeliveryHistory reads the delivery history a planning session sees, as `ropeshift measures history` prints it.
export function readDeliveryHistory(): Promise<DeliveryHistory> {
  return call("GET", project("/measures/history"));
}

// readCIDurations reads what each repository's CI takes, as `ropeshift measures ci` prints it.
export function readCIDurations(): Promise<CIDurations> {
  return call("GET", project("/measures/ci"));
}

// readCalibration reads how often finished work landed inside its baseline estimates over a window and over
// all time, as `ropeshift measures estimates` does.
export function readCalibration(window: string): Promise<Calibration> {
  return call("GET", project(`/measures/estimates?${new URLSearchParams({ window })}`));
}
