import { describe, expect, it } from "vitest";

import { agentLines, changedFields, newID, nextVersion, outcomeOf, policyBody, projectNameOf, settingsOf, tomlString } from "./logic";


describe("policy bodies", () => {
  it("writes the table a policy's kind names, with its version first", () => {
    const body = policyBody("recovery", 4, { restart_stalled: true, max_breaker_resets_per_day: 2, retry_failed: false, replan_blocked: true, max_attempts: 3 });
    expect(body).toBe("[recovery_policy]\nversion = 4\nrestart_stalled = true\nmax_breaker_resets_per_day = 2\nretry_failed = false\nreplan_blocked = true\nmax_attempts = 3\n");
  });

  it("quotes text and lists, and escapes what TOML would read as syntax", () => {
    expect(tomlString('a "b" \\ c\nd')).toBe('"a \\"b\\" \\\\ c\\nd"');
    const body = policyBody("decision", 2, { reversible_default: "assume", one_way_categories: ["security", "data"], max_assumptions_per_work_order: 5 });
    expect(body).toContain('one_way_categories = ["security", "data"]');
    expect(body).toContain('reversible_default = "assume"');
  });

  it("leaves out an optional text a member left empty", () => {
    expect(policyBody("notify", 2, { desktop: true, webhook_url: "", webhook_secret_env: "" })).toBe("[notify_policy]\nversion = 2\ndesktop = true\n");
  });

  it("writes each delegation class as a table of its own", () => {
    const body = policyBody("delegation", 3, { classes: [{ tool: "Bash", command_prefix: "go test", path: "", agent: "" }] });
    expect(body).toBe('[delegation_policy]\nversion = 3\n\n[[delegation_policy.classes]]\ntool = "Bash"\ncommand_prefix = "go test"\n');
    expect(policyBody("delegation", 3, { classes: [] })).toBe("[delegation_policy]\nversion = 3\nclasses = []\n");
  });

  it("reads a version's settings, and nothing from settings that do not parse", () => {
    expect(settingsOf('{"version":2,"max_attempts":3}')).toEqual({ version: 2, max_attempts: 3 });
    expect(settingsOf("not json")).toEqual({});
  });

  it("names the version a save records: one past the newest", () => {
    expect(nextVersion([{ version: 3 }, { version: 1 }])).toBe(4);
    expect(nextVersion([])).toBe(1);
  });
});

describe("roster edits", () => {
  const agent = {
    name: "heavy-implementation",
    purpose: "A change across many files.",
    kind: "implementation",
    access: "writing",
    class: "large",
    effort: "high",
    approval: "none",
    instructions: "Read the contract first.",
    report: "",
    enabled: true,
    tools: { allow: ["read", "edit"], deny: ["web"] },
    models: { "claude-code": "opus" },
    sources: {},
  };

  it("sends only the fields a member changed, in the API's names", () => {
    expect(changedFields(agent, { ...agent, effort: "max", class: "frontier" })).toEqual({ effort: "max", class: "frontier" });
    expect(changedFields(agent, { ...agent, tools: { allow: ["read"], deny: ["web", "shell"] } })).toEqual({ "tools.allow": "read", "tools.deny": "web,shell" });
    expect(changedFields(agent, { ...agent, models: { "claude-code": "opus", codex: "gpt-5" } })).toEqual({ "models.codex": "gpt-5" });
    expect(changedFields(agent, agent)).toEqual({});
  });

  it("writes an agent as the lines a difference compares", () => {
    expect(agentLines({ effort: "max", class: "frontier" })).toEqual(['class = "frontier"', 'effort = "max"']);
  });
});

describe("a dry run's outcomes", () => {
  it("says what registering would do with each clone, in the design's words", () => {
    expect(outcomeOf({ path: "/w/api", outcome: "registered" }, true)).toMatchObject({ label: "Will register", tone: "success" });
    expect(outcomeOf({ path: "/w/api", outcome: "registered" }, false)).toMatchObject({ label: "Registered", tone: "success" });
    expect(outcomeOf({ path: "/w/api", outcome: "known" }, true)).toMatchObject({ label: "Already in this project" });
    expect(outcomeOf({ path: "/w/x", outcome: "refused", reason: "project beta holds it" }, true)).toMatchObject({ label: "Refused: project beta holds it", tone: "danger" });
  });
});

describe("the adopt flow's names", () => {
  it("names a project after its folder as the CLI does: lowercase letters and digits, a hyphen for each run of anything else", () => {
    expect(projectNameOf("/srv/code/Tide Water_2")).toBe("tide-water-2");
    expect(projectNameOf("/srv/code/--Harbor--/")).toBe("harbor");
    expect(projectNameOf("/")).toBe("");
  });

  it("makes IDs the ledger reads: 26 characters of Crockford base32, in time order", () => {
    const first = newID(1_700_000_000_000);
    const later = newID(1_700_000_000_001);
    expect(first).toMatch(/^[0-9A-HJKMNP-TV-Z]{26}$/);
    expect(first.slice(0, 10) < later.slice(0, 10)).toBe(true);
  });
});
