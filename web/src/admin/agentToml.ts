import { parse, TomlError } from "smol-toml";

import type { AgentFields } from "../api/admin";
import { tomlString } from "./logic";

// An agent's definition as text, read the way `ropeshift agent add --file` reads a file: one `[[agent]]` table, or
// its keys at the top of the text. The command makes the same agent of the same text, which web/testdata/agent-toml.json
// holds both to. The text is read here and nowhere else: whether the agent is a good one (its kind, its class, the
// tools it allows) is the API's to say when the roster change is saved, and the dialog shows what it says.

const textKeys = ["name", "purpose", "kind", "access", "class", "effort", "approval", "instructions", "report"] as const;
const agentKeys = new Set<string>([...textKeys, "models", "enabled", "tools"]);
const toolKeys = new Set(["allow", "deny"]);

export type ParsedAgent = { agent: AgentFields; error?: undefined } | { agent?: undefined; error: string };

// A table the parser read has no prototype, so a date, which has one, is no table.
const isTable = (value: unknown): value is Record<string, unknown> => typeof value === "object" && value !== null && Object.getPrototypeOf(value) === null;

const refuse = (error: string): ParsedAgent => ({ error });

// unknownKey names the first key an agent's text holds that no agent has, in the order the text wrote them.
function unknownKey(table: Record<string, unknown>): string {
  const stray = Object.keys(table).find((key) => !agentKeys.has(key));
  if (stray !== undefined) {
    return stray;
  }
  const tools = table.tools;
  const inTools = isTable(tools) ? Object.keys(tools).find((key) => !toolKeys.has(key)) : undefined;
  return inTools === undefined ? "" : `tools.${inTools}`;
}

function listOfText(value: unknown): string[] | null {
  return Array.isArray(value) && value.every((item) => typeof item === "string") ? (value as string[]) : null;
}

// agentOf reads the table that holds an agent's keys. Text is trimmed where the command trims it: the purpose, the
// instructions, and the report.
function agentOf(table: Record<string, unknown>): ParsedAgent {
  const stray = unknownKey(table);
  if (stray) {
    return refuse(`Unknown key ${stray}.`);
  }
  const text: Record<string, string> = {};
  for (const key of textKeys) {
    const value = table[key];
    if (value !== undefined && typeof value !== "string") {
      return refuse(`${key} is text.`);
    }
    text[key] = value ?? "";
  }
  if (table.enabled !== undefined && typeof table.enabled !== "boolean") {
    return refuse("enabled is true or false.");
  }
  const tools = { allow: [] as string[], deny: [] as string[] };
  if (table.tools !== undefined) {
    if (!isTable(table.tools)) {
      return refuse("tools is a table with allow and deny lists.");
    }
    for (const side of ["allow", "deny"] as const) {
      const list = table.tools[side] === undefined ? [] : listOfText(table.tools[side]);
      if (list === null) {
        return refuse(`tools.${side} is a list of text.`);
      }
      tools[side] = list;
    }
  }
  const models: Record<string, string> = {};
  if (table.models !== undefined) {
    if (!isTable(table.models)) {
      return refuse("models is a table of text, one model for each driver.");
    }
    for (const [driver, model] of Object.entries(table.models)) {
      if (typeof model !== "string") {
        return refuse(`models.${driver} is text.`);
      }
      models[driver] = model;
    }
  }
  const agent: AgentFields = {
    name: text.name ?? "",
    purpose: (text.purpose ?? "").trim(),
    kind: text.kind ?? "",
    access: text.access ?? "",
    tools,
    class: text.class ?? "",
    effort: text.effort ?? "",
    approval: text.approval ?? "",
    instructions: (text.instructions ?? "").trim(),
    report: (text.report ?? "").trim(),
    enabled: table.enabled !== false,
  };
  if (Object.keys(models).length > 0) {
    agent.models = models;
  }
  return { agent };
}

// reasonOf is the first line of what the parser says, which is the reason: the lines after it quote the text.
function reasonOf(error: TomlError): string {
  const reason = (error.message.split("\n")[0] ?? "").replace(/^Invalid TOML document: /, "");
  return reason.charAt(0).toUpperCase() + reason.slice(1);
}

// agentFromToml reads an agent's definition from text, or says why it cannot.
export function agentFromToml(text: string): ParsedAgent {
  let doc: Record<string, unknown>;
  try {
    doc = parse(text);
  } catch (error) {
    return refuse(error instanceof TomlError ? `${reasonOf(error)} (line ${error.line}, column ${error.column}).` : String(error));
  }
  if (!("agent" in doc)) {
    return agentOf(doc);
  }
  const wrapped = doc.agent;
  if (!Array.isArray(wrapped) || !wrapped.every(isTable)) {
    return refuse("Write the agent as [[agent]], or put its keys at the top of the text.");
  }
  if (wrapped.length > 1) {
    return refuse(`A text adds one agent, and this holds ${wrapped.length} [[agent]] tables.`);
  }
  // Nothing may stand beside the table: the command reads the text as the keys of an agent then, and `agent` is no key.
  const beside = Object.keys(doc).find((key) => key !== "agent");
  const table = wrapped[0];
  if (beside !== undefined || table === undefined) {
    return refuse(`Unknown key ${beside ?? "agent"}.`);
  }
  return agentOf(table);
}

// previewLines writes a parsed agent as the lines of a record the roster change adds under its name, in name order,
// which the review's diff viewer shows as added. A key the text leaves out has no line, so the preview says what the
// text says and nothing the roster fills in.
export function previewLines(name: string, agent: AgentFields): string[] {
  const rows: [string, string][] = [];
  for (const key of textKeys) {
    if (key !== "name" && agent[key] !== "") {
      rows.push([key, tomlString(agent[key])]);
    }
  }
  rows.push(["enabled", String(agent.enabled)]);
  for (const side of ["allow", "deny"] as const) {
    if (agent.tools[side].length > 0) {
      rows.push([`tools.${side}`, tomlString(agent.tools[side].join(","))]);
    }
  }
  for (const [driver, model] of Object.entries(agent.models ?? {})) {
    rows.push([`models.${driver}`, tomlString(model)]);
  }
  return [`[agents.${name}]`, ...rows.sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)).map(([key, value]) => `${key} = ${value}`)];
}
