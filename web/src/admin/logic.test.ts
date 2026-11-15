import { describe, expect, it } from "vitest";

import { agentLines, capsAbove, capsDraftOf, capsProblems, capsSettings, capsWords, changedFields, forgeBindingProblem, minutesOf, newID, nextVersion, outcomeOf, policies, policyBody, policySettings, problemOf, projectNameOf, settingsOf, tomlString } from "./logic";


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

describe("the assistant policy", () => {
  it("writes each stop as a duration in minutes", () => {
    expect(policyBody("assistant", 3, { idle_stop: 30, warm_idle_stop: 2 })).toBe('[assistant]\nversion = 3\nidle_stop = "30m"\nwarm_idle_stop = "2m"\n');
  });

  it("reads a duration the way the policy writes one, in whole minutes", () => {
    expect(minutesOf("15m")).toBe(15);
    expect(minutesOf("2h")).toBe(120);
    expect(minutesOf("1h30m")).toBe(90);
    expect(minutesOf("24h")).toBe(1440);
    expect(minutesOf("90s")).toBe(2);
    expect(minutesOf("never")).toBeNull();
    expect(minutesOf("")).toBeNull();
  });

  it("reads a version's stops as minutes, and leaves another policy's settings as they are", () => {
    expect(policySettings("assistant", '{"version":2,"idle_stop":"30m","warm_idle_stop":"2m"}')).toEqual({ version: 2, idle_stop: 30, warm_idle_stop: 2 });
    // A version that names no warm idle stop runs on the two minutes the policy gives it.
    expect(policySettings("assistant", '{"version":1,"idle_stop":"15m"}')).toEqual({ version: 1, idle_stop: 15, warm_idle_stop: 2 });
    expect(policySettings("recovery", '{"max_attempts":3}')).toEqual({ max_attempts: 3 });
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

describe("lane caps", () => {
  const recommendation = { max_lanes: 6, reason: "8 cores less 1 reserved fit 7 lanes; the lower is 6, from the estimates" };

  it("reads a worker's caps into the form: null follows the recommendation, 0 caps nothing, and any other number stands", () => {
    expect(capsDraftOf({ max_lanes: null, max_lanes_per_sign_in: 0 })).toEqual({ mode: "recommended", lanes: "", perSignIn: "0" });
    expect(capsDraftOf({ max_lanes: 0, max_lanes_per_sign_in: 2 })).toEqual({ mode: "none", lanes: "", perSignIn: "2" });
    expect(capsDraftOf({ max_lanes: 9, max_lanes_per_sign_in: 0 })).toEqual({ mode: "number", lanes: "9", perSignIn: "0" });
    expect(capsDraftOf(undefined)).toEqual({ mode: "recommended", lanes: "", perSignIn: "0" });
  });

  it("writes the form as the request the route reads, both caps every time", () => {
    expect(capsSettings({ mode: "recommended", lanes: "", perSignIn: "0" })).toEqual({ max_lanes: null, max_lanes_per_sign_in: 0 });
    expect(capsSettings({ mode: "none", lanes: "7", perSignIn: "3" })).toEqual({ max_lanes: 0, max_lanes_per_sign_in: 3 });
    expect(capsSettings({ mode: "number", lanes: " 12 ", perSignIn: "" })).toEqual({ max_lanes: 12, max_lanes_per_sign_in: 0 });
  });

  it("refuses a number that is no whole number of lanes, and says how to cap nothing", () => {
    expect(capsProblems({ mode: "number", lanes: "", perSignIn: "0" }).lanes).toMatch(/whole number, 1 or more/);
    expect(capsProblems({ mode: "number", lanes: "0", perSignIn: "0" }).lanes).toMatch(/No cap/);
    expect(capsProblems({ mode: "number", lanes: "2.5", perSignIn: "0" }).lanes).toBeDefined();
    expect(capsProblems({ mode: "number", lanes: "-1", perSignIn: "0" }).lanes).toBeDefined();
    expect(capsProblems({ mode: "number", lanes: "1000", perSignIn: "0" })).toEqual({});
    // A number typed under another choice is not read.
    expect(capsProblems({ mode: "none", lanes: "abc", perSignIn: "0" })).toEqual({});
    expect(capsProblems({ mode: "recommended", lanes: "", perSignIn: "-2" }).perSignIn).toMatch(/whole number, 0 or more/);
    expect(capsProblems({ mode: "recommended", lanes: "", perSignIn: "1.5" }).perSignIn).toBeDefined();
    expect(capsProblems({ mode: "recommended", lanes: "", perSignIn: "" })).toEqual({});
  });

  it("says a cap in words, with the recommendation where it is what caps", () => {
    expect(capsWords({ mode: "recommended", lanes: "", perSignIn: "0" }, 6)).toBe("the recommendation, 6");
    expect(capsWords({ mode: "recommended", lanes: "", perSignIn: "0" })).toBe("the recommendation, which is not reported");
    expect(capsWords({ mode: "none", lanes: "", perSignIn: "0" }, 6)).toBe("no cap");
    expect(capsWords({ mode: "number", lanes: "9", perSignIn: "0" }, 6)).toBe("9");
  });

  it("finds a cap above the recommendation, as the API's warning does: only a number, and only past a recommendation", () => {
    expect(capsAbove({ mode: "number", lanes: "9", perSignIn: "0" }, recommendation)).toEqual({ value: 9, recommended: 6, reason: recommendation.reason });
    expect(capsAbove({ mode: "number", lanes: "6", perSignIn: "0" }, recommendation)).toBeNull();
    expect(capsAbove({ mode: "none", lanes: "9", perSignIn: "0" }, recommendation)).toBeNull();
    expect(capsAbove({ mode: "recommended", lanes: "", perSignIn: "0" }, recommendation)).toBeNull();
    expect(capsAbove({ mode: "number", lanes: "9", perSignIn: "0" }, undefined)).toBeNull();
    expect(capsAbove({ mode: "number", lanes: "9", perSignIn: "0" }, { max_lanes: 0, reason: "" })).toBeNull();
    expect(capsAbove({ mode: "number", lanes: "x", perSignIn: "0" }, recommendation)).toBeNull();
  });
});

describe("the estimate policy", () => {
  const field = (key: string) => policies.estimate.fields.find((one) => one.key === key)!;
  const stated = { min_samples: 12, recent_samples: 60, forecast_trials: 1000, share_history: true };

  it("writes the table with every setting stated, in the order the built-in version writes them", () => {
    expect(policyBody("estimate", 3, stated)).toBe("[estimate_policy]\nversion = 3\nmin_samples = 12\nrecent_samples = 60\nforecast_trials = 1000\nshare_history = true\n");
    expect(policyBody("estimate", 3, { ...stated, share_history: false })).toContain("share_history = false\n");
  });

  it("reads a version's settings as the form holds them", () => {
    expect(policySettings("estimate", '{"version":2,"min_samples":12,"recent_samples":60,"forecast_trials":1000,"share_history":true}')).toEqual({ version: 2, ...stated });
  });

  it("holds a confident estimate to at least 5 outcomes, as the API does, and says so at the field", () => {
    expect(problemOf(field("min_samples"), { ...stated, min_samples: 4 })).toBe("Enter a whole number, 5 or more.");
    expect(problemOf(field("min_samples"), { ...stated, min_samples: "" })).toBe("Enter a whole number, 5 or more.");
    expect(problemOf(field("min_samples"), { ...stated, min_samples: 5, recent_samples: 5 })).toBe("");
  });

  it("holds the recent outcomes to the outcomes a confident estimate needs, and a forecast to 1 to 20,000 trials", () => {
    expect(problemOf(field("recent_samples"), { ...stated, recent_samples: 11 })).toBe("Enter at least 12, the outcomes a confident estimate needs.");
    expect(problemOf(field("recent_samples"), { ...stated, recent_samples: 12 })).toBe("");
    expect(problemOf(field("forecast_trials"), { ...stated, forecast_trials: 0 })).toBe("Enter a whole number, 1 or more.");
    expect(problemOf(field("forecast_trials"), { ...stated, forecast_trials: 20_001 })).toBe("Enter a whole number from 1 to 20,000.");
    expect(problemOf(field("forecast_trials"), { ...stated, forecast_trials: 20_000 })).toBe("");
    expect(problemOf(field("forecast_trials"), { ...stated, forecast_trials: 2.5 })).toBe("Enter a whole number, 1 or more.");
  });

  it("leaves every other policy's whole numbers as they were: only an empty one is a problem, and a setting a version leaves out is none", () => {
    const attempts = policies.recovery.fields.find((one) => one.key === "max_attempts")!;
    expect(problemOf(attempts, { max_attempts: "" })).toBe("Enter a whole number, 0 or more.");
    expect(problemOf(attempts, { max_attempts: 0 })).toBe("");
    expect(problemOf(attempts, {})).toBe("");
    const idle = policies.assistant.fields.find((one) => one.key === "idle_stop")!;
    expect(problemOf(idle, { idle_stop: 0 })).toBe("Enter a whole number of minutes, 1 to 1,440.");
    expect(problemOf(idle, { idle_stop: 30 })).toBe("");
    expect(problemOf(policies.recovery.fields.find((one) => one.key === "retry_failed")!, { retry_failed: true })).toBe("");
  });
});

describe("a forge binding", () => {
  // The server reads HOST/OWNER/REPO as ledger.ParseForgeBinding does: three parts, each a name a forge accepts.
  it.each(["github.com/acme/widgets", "git.example.com/team/repo.name", "gitlab.com/group/sub_project-2", ""])("accepts %j", (text) => {
    expect(forgeBindingProblem(text)).toBe("");
  });

  it.each(["widgets", "acme/widgets", "github.com/acme", "github.com/acme/widgets/extra", "github.com//widgets", "/acme/widgets", "github.com/acme/", "https://github.com/acme/widgets"])(
    "asks for three parts where %j has not",
    (text) => {
      expect(forgeBindingProblem(text)).toContain("HOST/OWNER/REPO");
    },
  );

  it.each(["github.com/acme/wid gets", "github.com/acme/widgets ", "github.com/./widgets", "github.com/acme/..", "github.com/-acme/widgets", "git@github.com/acme/widgets", "github.com:443/acme/widgets", "github.com/acme/widgets#x", "github.com/acme/wid?gets", "github.com/ac\\me/widgets", "github.com/acme/wid\u0007gets"])(
    "names a part a forge would refuse in %j",
    (text) => {
      expect(forgeBindingProblem(text)).toContain("a forge would refuse");
    },
  );
});
