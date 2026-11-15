import { describe, expect, it } from "vitest";

import cases from "../../testdata/agent-toml.json";
import type { AgentFields } from "../api/admin";
import { agentFromToml, previewLines } from "./agentToml";

// web/testdata/agent-toml.json holds texts of an agent's definition and what `ropeshift agent add --file` reads from
// each, which internal/cli holds the command to. Pasting the same text in the browser reads the same agent, or is
// refused in the same cases.
const table = cases as { name: string; toml: string; agent?: AgentFields; refused?: boolean }[];

describe("an agent pasted as TOML", () => {
  for (const one of table) {
    it(`reads as the command reads it: ${one.name}`, () => {
      const read = agentFromToml(one.toml);
      if (one.refused) {
        expect(read.error, "a refusal that says what is wrong").toBeTruthy();
        expect(read.agent).toBeUndefined();
      } else {
        expect(read.error).toBeUndefined();
        expect(read.agent).toEqual(one.agent);
      }
    });
  }

  it("names the line and column where the text stops being TOML", () => {
    expect(agentFromToml("[[agent]]\npurpose = \"never closed\n").error).toMatch(/line 2/);
    expect(agentFromToml("[[agent]]\nkind = \"a\"\nkind = \"b\"\n").error).toMatch(/line 3/);
  });

  it("names the key no agent has, wherever it stands", () => {
    expect(agentFromToml("colour = \"red\"\n").error).toBe("Unknown key colour.");
    expect(agentFromToml("[[agent]]\ncolour = \"red\"\n").error).toBe("Unknown key colour.");
    expect(agentFromToml("colour = \"red\"\n\n[[agent]]\nkind = \"lookup\"\n").error).toBe("Unknown key colour.");
    expect(agentFromToml("[[agent]]\n[agent.tools]\nfetch = [\"web\"]\n").error).toBe("Unknown key tools.fetch.");
  });

  it("says a text adds one agent, and how to write one", () => {
    expect(agentFromToml("[[agent]]\nname = \"a\"\n[[agent]]\nname = \"b\"\n").error).toBe("A text adds one agent, and this holds 2 [[agent]] tables.");
    expect(agentFromToml("[agent]\nkind = \"lookup\"\n").error).toMatch(/Write the agent as \[\[agent\]\]/);
  });

  it("names the key whose value is the wrong kind", () => {
    expect(agentFromToml("[[agent]]\nkind = 3\n").error).toBe("kind is text.");
    expect(agentFromToml("[[agent]]\nenabled = \"yes\"\n").error).toBe("enabled is true or false.");
    expect(agentFromToml("[[agent]]\n[agent.tools]\nallow = [1]\n").error).toBe("tools.allow is a list of text.");
    expect(agentFromToml("[[agent]]\n[agent.models]\nclaude-code = 3\n").error).toBe("models.claude-code is text.");
  });

  it("previews only what the text states, as the lines a roster change adds, each list and model on a line of its own", () => {
    const read = agentFromToml("[[agent]]\nkind = \"lookup\"\naccess = \"read_only\"\nclass = \"small\"\neffort = \"low\"\ntools = { allow = [\"read\", \"search\"], deny = [\"web\"] }\nmodels = { claude-code = \"haiku\" }\n");
    expect(previewLines("scout", read.agent as AgentFields)).toEqual([
      "[agents.scout]",
      "access = \"read_only\"",
      "class = \"small\"",
      "effort = \"low\"",
      "enabled = true",
      "kind = \"lookup\"",
      "models.claude-code = \"haiku\"",
      "tools.allow = \"read,search\"",
      "tools.deny = \"web\"",
    ]);
    // A text that states nothing previews only the agent's name and that it is enabled.
    expect(previewLines("scout", agentFromToml("").agent as AgentFields)).toEqual(["[agents.scout]", "enabled = true"]);
  });
});
