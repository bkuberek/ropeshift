import type { AgentFields, RepositoryAdded } from "../api/admin";
import type { StateLook } from "../floor/states";

// The settings pages' own reckoning: what a difference shows, the body a policy version records, and the
// fields a roster change sends. None of it decides anything the API decides; the API refuses what it
// refuses, and the page shows that refusal at the field.

export type PolicyKind = "decision" | "lane" | "recovery" | "notify" | "autonomy" | "delegation";

export type DelegationClass = { tool: string; command_prefix: string; path: string; agent: string };
export type PolicyValue = boolean | number | string | string[] | DelegationClass[];
export type PolicyValues = Record<string, PolicyValue>;

export type FieldSpec = {
  key: string;
  label: string;
  type: "bool" | "int" | "text" | "choice" | "list" | "classes";
  choices?: string[];
  help?: string;
  // optional text is left out of the body when empty, because the policy reads its absence.
  optional?: boolean;
};

// Each policy's table and the settings it states, in the order its built-in version writes them.
export const policies: Record<PolicyKind, { table: string; fields: FieldSpec[] }> = {
  decision: {
    table: "decision_policy",
    fields: [
      { key: "reversible_default", label: "Reversible decisions", type: "choice", choices: ["assume"], help: "What an orchestrator does with a reversible decision: assume it, and record the assumption." },
      { key: "one_way_categories", label: "One-way categories", type: "list", help: "Decisions in these categories always wait for a member." },
      { key: "max_assumptions_per_work_order", label: "Assumptions per work order", type: "int", help: "How many assumptions one work order may hold before the next question waits for a member." },
    ],
  },
  lane: {
    table: "lane_policy",
    fields: [
      { key: "on_contract_version", label: "When a contract changes under a lane", type: "choice", choices: ["notify", "park"], help: "notify tells the lane and lets it go on; park stops it until a member resumes it." },
      { key: "on_one_way_decision", label: "When a one-way decision lands under a lane", type: "choice", choices: ["notify", "park"] },
    ],
  },
  recovery: {
    table: "recovery_policy",
    fields: [
      { key: "restart_stalled", label: "Restart a stalled lane", type: "bool" },
      { key: "max_breaker_resets_per_day", label: "Breaker resets per day", type: "int", help: "How often an orchestrator may reset a tripped lane in one day." },
      { key: "retry_failed", label: "Retry failed work", type: "bool" },
      { key: "replan_blocked", label: "Replan blocked work", type: "bool" },
      { key: "max_attempts", label: "Attempts per work order", type: "int", help: "For work that names no bound of its own." },
    ],
  },
  notify: {
    table: "notify_policy",
    fields: [
      { key: "desktop", label: "Desktop notifications", type: "bool" },
      { key: "webhook_url", label: "Webhook address", type: "text", optional: true, help: "Each item that starts to wait on a member is posted here." },
      { key: "webhook_secret_env", label: "Webhook secret variable", type: "text", optional: true, help: "The name of the daemon's environment variable that holds the signing secret, never the secret." },
    ],
  },
  autonomy: {
    table: "autonomy_policy",
    fields: [
      { key: "subscription_default", label: "Level for a subscription sign-in", type: "choice", choices: ["manual", "attended", "unattended"] },
      { key: "api_key_default", label: "Level for an API key", type: "choice", choices: ["manual", "attended", "unattended"] },
      { key: "attended_max_lanes", label: "Lanes while attended", type: "int" },
      { key: "attended_max_respawns_per_day", label: "Respawns per day while attended", type: "int" },
    ],
  },
  delegation: {
    table: "delegation_policy",
    fields: [{ key: "classes", label: "Approval classes an orchestrator decides", type: "classes", help: "Each class names a tool, and optionally a command prefix, a path, or an agent." }],
  },
};

export function tomlString(text: string): string {
  return `"${text.replaceAll("\\", "\\\\").replaceAll('"', '\\"').replaceAll("\n", "\\n").replaceAll("\t", "\\t")}"`;
}

function tomlValue(value: PolicyValue): string {
  if (typeof value === "boolean" || typeof value === "number") {
    return String(value);
  }
  if (typeof value === "string") {
    return tomlString(value);
  }
  return `[${(value as string[]).map((item) => tomlString(item)).join(", ")}]`;
}

// policyBody writes a policy version as the TOML its route reads: the kind's table, the version, then
// each setting.
export function policyBody(kind: PolicyKind, version: number, values: PolicyValues): string {
  const { table, fields } = policies[kind];
  const lines = [`[${table}]`, `version = ${version}`];
  const tables: string[] = [];
  for (const field of fields) {
    const value = values[field.key];
    if (value === undefined || (field.optional && value === "")) {
      continue;
    }
    if (field.type === "classes") {
      const classes = value as DelegationClass[];
      if (classes.length === 0) {
        lines.push(`${field.key} = []`);
      }
      for (const row of classes) {
        tables.push("", `[[${table}.${field.key}]]`);
        for (const name of ["tool", "command_prefix", "path", "agent"] as const) {
          if (row[name]) {
            tables.push(`${name} = ${tomlString(row[name])}`);
          }
        }
      }
      continue;
    }
    lines.push(`${field.key} = ${tomlValue(value)}`);
  }
  return `${[...lines, ...tables].join("\n")}\n`;
}

// settingsOf reads the JSON a policy version keeps beside its body.
export function settingsOf(settings: string): PolicyValues {
  try {
    const parsed: unknown = JSON.parse(settings);
    return parsed && typeof parsed === "object" ? (parsed as PolicyValues) : {};
  } catch {
    return {};
  }
}

// nextVersion is the version a save records, one past the newest.
export function nextVersion(versions: { version: number }[]): number {
  return Math.max(0, ...versions.map((row) => row.version)) + 1;
}

// The fields a roster editor changes, each as the API names it, with how to write its value.
const textFields = ["purpose", "kind", "access", "class", "effort", "approval", "instructions", "report"] as const;

// changedFields is a `set` change's fields: what an edit changed of an agent, in the API's names.
export function changedFields(agent: AgentFields, edited: AgentFields): Record<string, string> {
  const out: Record<string, string> = {};
  for (const name of textFields) {
    if (edited[name] !== agent[name]) {
      out[name] = edited[name];
    }
  }
  for (const side of ["allow", "deny"] as const) {
    if (edited.tools[side].join(",") !== agent.tools[side].join(",")) {
      out[`tools.${side}`] = edited.tools[side].join(",");
    }
  }
  for (const [driver, model] of Object.entries(edited.models ?? {})) {
    if ((agent.models ?? {})[driver] !== model) {
      out[`models.${driver}`] = model;
    }
  }
  return out;
}

// agentLines writes fields as the lines a difference compares, in name order.
export function agentLines(fields: Record<string, string>): string[] {
  return Object.keys(fields)
    .sort()
    .map((name) => `${name} = ${tomlString(fields[name] ?? "")}`);
}

// outcomeOf says what registering did, or a dry run says it would do, with one clone or path.
export function outcomeOf(added: RepositoryAdded, dryRun: boolean): StateLook {
  switch (added.outcome) {
    case "registered":
      return { label: dryRun ? "Will register" : "Registered", tone: "success", glyph: "dot" };
    case "known":
      return { label: "Already in this project", tone: "neutral", glyph: "ring" };
    default:
      return { label: `Refused: ${added.reason ?? "no reason given"}`, tone: "danger", glyph: "cross" };
  }
}

// projectNameOf names a project after a folder, as `ropeshift adopt` does: the folder's last part in
// lowercase letters and digits, with a hyphen for each run of anything else, at most 63 characters.
export function projectNameOf(path: string): string {
  const folder = path.replace(/\/+$/, "").split("/").pop() ?? "";
  const words = folder.toLowerCase().match(/[a-z0-9]+/g) ?? [];
  return words.join("-").slice(0, 63).replace(/-+$/, "");
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";

// newID makes an ID the ledger reads, as the CLI's does: the time in milliseconds, then randomness, in
// Crockford base32, so a retried step names the record its first try made.
export function newID(now = Date.now()): string {
  let time = "";
  let left = now;
  for (let at = 0; at < 10; at += 1) {
    time = crockford[left % 32] + time;
    left = Math.floor(left / 32);
  }
  const random = crypto.getRandomValues(new Uint8Array(16));
  return time + Array.from(random, (byte) => crockford[byte % 32]).join("");
}
