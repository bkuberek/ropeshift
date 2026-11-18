import type { FieldSpec, PolicyValue, PolicyValues } from "./logic";

// The spend policies' own reckoning (ADR-0037): the keys a member edits, the ranges the API holds them to, and the body a
// version records. A version states every key, in the order the built-in body writes them, so the review of a change shows
// the lines that changed and no others. The API refuses what it refuses; these ranges only keep a refusal from being the
// first a member hears of one.

const money = "Enter an amount in dollars from 0 to 1,000,000. 0 is none.";
const maxAmount = 1_000_000;

// The capability classes a session can run on, as the agent roster names them.
const classes = ["small", "standard", "large", "frontier"];

// The wait rules a policy may turn off or extend, by the ID the policy names them.
export const waitRules = ["W1", "W2", "W3", "W4"];

export const waitRuleWords: Record<string, string> = {
  W1: "a sleep of the longest sleep or more",
  W2: "a loop whose body sleeps",
  W3: "a watch of CI, such as gh run watch",
  W4: "a read of CI status, such as gh pr checks",
};

const amount = (key: string, label: string, group: string, help: string): FieldSpec => ({ key, label, type: "decimal", unit: "usd", min: 0, max: maxAmount, group, help });
const share = (key: string, label: string, group: string, help: string): FieldSpec => ({ key, label, type: "decimal", unit: "share", min: 0, max: 1, group, help });
const multiplier = (key: string, label: string, group: string, help: string): FieldSpec => ({ key, label, type: "decimal", unit: "times", min: 1, max: 10, group, help });
const rulePatterns = (rule: string): FieldSpec => ({
  key: `wait_rule_patterns.${rule}`,
  label: `Command prefixes rule ${rule} also matches`,
  type: "patterns",
  group: "Waits",
  help: `One command prefix per line, at most 64. Rule ${rule} denies ${waitRuleWords[rule]}.`,
});

const waits = "Waits";
const workOrders = "Work order limits";
const project = "Project limits and warnings";
const hardStop = "The hard stop";
const progress = "No progress";
const alert = "Waiting alert";
const sessions = "Session classes";

// spendFields are the settings of a project's spend policy in the order the page shows them.
export const spendFields: FieldSpec[] = [
  { key: "max_live_wait", label: "Longest live wait", type: "duration", min: 60, max: 3600, example: "5m", group: waits, help: "How long a lane may wait live. A longer wait parks the lane. From 1m to 1h." },
  { key: "max_tool_sleep", label: "Longest sleep inside a command", type: "duration", min: 1, max: 3600, example: "60s", group: waits, help: "Rule W1 denies a sleep this long or longer. From 1s to 1h." },
  { key: "wait_rules_off", label: "Wait rules turned off", type: "rules", group: waits, help: "A rule is a class of command that waits by construction. Turn one off only when it denies work you want." },
  ...waitRules.map(rulePatterns),
  { key: "soft_quantile", label: "Soft limit sits at", type: "choice", choices: ["p50", "p85"], group: workOrders, help: "The quantile of a work order's cost band where its soft limit sits." },
  multiplier("hard_multiplier", "Hard limit, in times the band's p85", workOrders, "A work order's hard limit is the band's p85 times this, from 1 to 10."),
  { key: "low_confidence_outcomes", label: "Outcomes a cost band needs to be trusted", type: "int", min: 1, max: 100, group: workOrders, help: "With fewer, the limit of the work order's size applies." },
  share("low_confidence_soft_share", "Soft limit of an untrusted band, as a share of its hard limit", workOrders, "From 0 to 1."),
  amount("no_history_limit.small", "Hard limit with no trusted band: small", workOrders, "In US dollars at list price."),
  amount("no_history_limit.medium", "Hard limit with no trusted band: medium", workOrders, "In US dollars at list price."),
  amount("no_history_limit.large", "Hard limit with no trusted band: large", workOrders, "In US dollars at list price."),
  share("review_share", "A review's hard limit, as a share of the work it reviews", workOrders, "From 0 to 1."),
  share("review_soft_share", "A review's soft limit, as a share of its hard limit", workOrders, "From 0 to 1."),
  multiplier("plan_hard_multiplier", "A plan's hard limit, in times its forecast's p85", workOrders, "From 1 to 10."),
  amount("project_daily_limit", "Project daily limit", project, "What the project's lanes may be charged in a day, in US dollars at list price. 0 is none."),
  amount("project_weekly_limit", "Project weekly limit", project, "The same for a week. 0 is none."),
  share("project_warn_share", "Warn at this share of a project limit", project, "A soft notice is raised at this share of each limit. From 0 to 1."),
  amount("assistant_daily_warn", "Assistant daily warning", project, "A soft daily limit for the assistant of this project. 0 is none."),
  share("budget_flag_headroom_share", "Headroom a headless generation's budget flag adds", hardStop, "The share over a hard limit that the next generation's --max-budget-usd carries. From 0 to 1."),
  { key: "repeat_limit", label: "Identical calls a lane may repeat", type: "int", min: 2, max: 20, group: progress, help: "Since its last progress, before the next identical call is denied. From 2 to 20." },
  { key: "repeat_denials_to_park", label: "Denials that park the lane", type: "int", min: 1, max: 10, group: progress, help: "From 1 to 10." },
  share("no_progress_share", "Spend since progress that raises a message", progress, "As a share of the baselined p50. From 0 to 1."),
  multiplier("no_progress_park_multiplier", "Where the lane parks, in times that share", progress, "From 1 to 10."),
  amount("no_history_no_progress", "Spend since progress that raises a message, with no baseline", progress, "In US dollars at list price."),
  share("waiting_alert_share", "Share of recent spend that waiting may reach", alert, "A notice is raised past it. From 0 to 1."),
  { key: "waiting_alert_window", label: "Span of that share", type: "duration", min: 60, max: 86_400, example: "30m", group: alert, help: "From 1m to 24h." },
  { key: "waiting_alert_min_calls", label: "Calls the share needs", type: "int", min: 1, max: 1000, group: alert, help: "From 1 to 1,000." },
  { key: "assistant_class", label: "Capability class of the assistant", type: "choice", choices: classes, group: sessions, help: "The class the project's assistant runs on." },
];

// installationSpendFields are the settings of the installation's spend policy, which the orchestrator serving every
// project reads.
export const installationSpendFields: FieldSpec[] = [
  { key: "orchestrator_class", label: "Capability class of the orchestrator", type: "choice", choices: classes, group: "Orchestrator", help: "The class the orchestrator runs on, for every project on the machine." },
  amount("orchestrator_daily_warn", "Orchestrator daily warning", "Orchestrator", "A soft daily limit per machine, in US dollars at list price. 0 is none."),
];

// tomlFloat writes a number so TOML reads it as a float, as the policy's own body does.
export function tomlFloat(value: number): string {
  const text = value.toLocaleString("en-US", { useGrouping: false, maximumFractionDigits: 20 });
  return text.includes(".") ? text : `${text}.0`;
}

// durationSeconds reads a duration the way the policy writes one, such as "5m", "60s", or "1h30m", in seconds. A word that
// is no duration reads as nothing.
export function durationSeconds(text: string): number | null {
  const found = /^(?:(\d+(?:\.\d+)?)h)?(?:(\d+(?:\.\d+)?)m)?(?:(\d+(?:\.\d+)?)s)?(?:(\d+(?:\.\d+)?)ms)?$/.exec(text.trim());
  if (!found || text.trim() === "") {
    return null;
  }
  const [, hours, minutes, seconds, millis] = found;
  return Number(hours ?? 0) * 3600 + Number(minutes ?? 0) * 60 + Number(seconds ?? 0) + Number(millis ?? 0) / 1000;
}

// secondsWords writes a bound of a duration as a member writes one.
function secondsWords(seconds: number): string {
  if (seconds % 3600 === 0) {
    return `${seconds / 3600}h`;
  }
  return seconds % 60 === 0 ? `${seconds / 60}m` : `${seconds}s`;
}

const formatted = (value: number) => value.toLocaleString("en-US");

// spendProblem says what is wrong with the value of a field of a spend policy, or nothing, and null for a field that is no
// spend field's kind, which the settings' own reckoning decides.
export function spendProblem(field: FieldSpec, value: PolicyValue | undefined): string | null {
  switch (field.type) {
    case "decimal": {
      const low = field.min ?? 0;
      const high = field.max ?? maxAmount;
      if (typeof value === "number" && Number.isFinite(value) && value >= low && value <= high) {
        return "";
      }
      if (field.unit === "usd") {
        return money;
      }
      return field.unit === "share" ? "Enter a share from 0 to 1, such as 0.8 for 80 percent." : `Enter a number from ${low} to ${formatted(high)}.`;
    }
    case "duration": {
      const seconds = typeof value === "string" ? durationSeconds(value) : null;
      if (seconds !== null && seconds >= (field.min ?? 0) && seconds <= (field.max ?? Infinity)) {
        return "";
      }
      return `Enter a duration from ${secondsWords(field.min ?? 0)} to ${secondsWords(field.max ?? 0)}, such as ${field.example ?? "5m"}.`;
    }
    case "patterns": {
      const lines = Array.isArray(value) ? (value as string[]) : [];
      const bad = lines.length > 64 || lines.some((line) => line.trim() === "" || new TextEncoder().encode(line).length > 200);
      return bad ? "Name at most 64 command prefixes, each up to 200 bytes." : "";
    }
    case "rules":
      return "";
    default:
      return null;
  }
}

// The kinds of spend alert, in the order the ledger lists them, with what each says.
export const spendNoticeKinds = [
  { kind: "spend_soft_limit", words: "A spend limit was nearly reached" },
  { kind: "spend_hard_limit", words: "A spend limit was reached, and work parked or stopped being claimed" },
  { kind: "spend_no_progress", words: "A lane parked because it made no progress" },
  { kind: "spend_waiting", words: "A lane spends most of its calls waiting" },
  { kind: "spend_policy_changed", words: "A spend setting changed" },
];

// channelsProblem says a channel is chosen for a kind of alert that the policy leaves off, which the API refuses: the desktop
// is a switch above, and the webhook is its address.
export function channelsProblem(values: PolicyValues): string {
  const held = (values as Record<string, unknown>).spend_channels;
  const chosen = held && typeof held === "object" ? (Object.values(held as Record<string, unknown>).flat() as unknown[]) : [];
  if (chosen.includes("desktop") && !values.desktop) {
    return "An alert is sent to the desktop, and desktop notifications are off. Turn them on, or clear the choice.";
  }
  if (chosen.includes("webhook") && !values.webhook_url) {
    return "An alert is sent to the webhook, and no webhook address is set. Set it, or clear the choice.";
  }
  return "";
}

// spendSettings reads a version's settings as the form holds them: each key by the name the CLI gives it, so a table is
// its parts, such as no_history_limit.medium.
export function spendSettings(settings: PolicyValues): PolicyValues {
  const raw = settings as Record<string, unknown>;
  const out: PolicyValues = {};
  for (const field of spendFields) {
    const [name, part] = field.key.split(".");
    const table = part !== undefined ? raw[name ?? ""] : undefined;
    const held = part !== undefined ? (table && typeof table === "object" ? (table as Record<string, unknown>)[part] : undefined) : raw[name ?? ""];
    if (held !== undefined && held !== null) {
      out[field.key] = held as PolicyValue;
    } else if (field.type === "patterns" || field.type === "rules") {
      out[field.key] = [];
    }
  }
  return out;
}

const text = (value: PolicyValue | undefined) => `"${String(value ?? "").replaceAll("\\", "\\\\").replaceAll('"', '\\"')}"`;
const list = (value: PolicyValue | undefined) => `[${((Array.isArray(value) ? value : []) as string[]).map(text).join(", ")}]`;
const float = (value: PolicyValue | undefined) => tomlFloat(Number(value));
const whole = (value: PolicyValue | undefined) => String(Number(value));

// spendPolicyBody writes a version of the spend policy as the policy's own body does: every key stated, in the same order,
// under the same comments.
export function spendPolicyBody(version: number, values: PolicyValues): string {
  const v = (key: string) => values[key];
  const patterns = waitRules.map((rule) => `${rule} = ${list(v(`wait_rule_patterns.${rule}`))}`).join(", ");
  return (
    [
      "[spend_policy]",
      `version = ${version}`,
      "# Waits",
      `max_live_wait = ${text(v("max_live_wait"))}`,
      `max_tool_sleep = ${text(v("max_tool_sleep"))}`,
      `wait_rules_off = ${list(v("wait_rules_off"))}`,
      `wait_rule_patterns = { ${patterns} }`,
      "# Limits",
      `soft_quantile = ${text(v("soft_quantile"))}`,
      `hard_multiplier = ${float(v("hard_multiplier"))}`,
      `low_confidence_outcomes = ${whole(v("low_confidence_outcomes"))}`,
      `low_confidence_soft_share = ${float(v("low_confidence_soft_share"))}`,
      `no_history_limit = { small = ${float(v("no_history_limit.small"))}, medium = ${float(v("no_history_limit.medium"))}, large = ${float(v("no_history_limit.large"))} }`,
      `review_share = ${float(v("review_share"))}`,
      `review_soft_share = ${float(v("review_soft_share"))}`,
      `plan_hard_multiplier = ${float(v("plan_hard_multiplier"))}`,
      `project_daily_limit = ${float(v("project_daily_limit"))}`,
      `project_weekly_limit = ${float(v("project_weekly_limit"))}`,
      `project_warn_share = ${float(v("project_warn_share"))}`,
      `assistant_daily_warn = ${float(v("assistant_daily_warn"))}`,
      `budget_flag_headroom_share = ${float(v("budget_flag_headroom_share"))}`,
      "# No progress",
      `repeat_limit = ${whole(v("repeat_limit"))}`,
      `repeat_denials_to_park = ${whole(v("repeat_denials_to_park"))}`,
      `no_progress_share = ${float(v("no_progress_share"))}`,
      `no_progress_park_multiplier = ${float(v("no_progress_park_multiplier"))}`,
      `no_history_no_progress = ${float(v("no_history_no_progress"))}`,
      "# Waiting alert",
      `waiting_alert_share = ${float(v("waiting_alert_share"))}`,
      `waiting_alert_window = ${text(v("waiting_alert_window"))}`,
      `waiting_alert_min_calls = ${whole(v("waiting_alert_min_calls"))}`,
      "# Session classes",
      `assistant_class = ${text(v("assistant_class"))}`,
    ].join("\n") + "\n"
  );
}
