import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { LaneSetup } from "../api/types";
import { RunsWith } from "./RunsWith";

const clean: LaneSetup = {
  generation: 2,
  version: "2.1.330",
  model: "claude-opus-5-5",
  tools: ["Read", "Bash", "mcp__ropeshift__lane_report"],
  mcp_servers: [
    { name: "ropeshift", from: "ropeshift" },
    { name: "docs", from: "repository" },
  ],
  agents: ["general-purpose", "heavy-implementation"],
  skills: ["simplify"],
  plugins: [],
};

function factOf(name: string): HTMLElement {
  const term = screen.getByText(name, { selector: "dt" });
  return term.nextElementSibling as HTMLElement;
}

describe("Runs with", () => {
  it("says a clean lane loaded none of the member's own setup, and what it did load", () => {
    render(<RunsWith setup={clean} />);
    const panel = screen.getByRole("region", { name: "Runs with" });
    expect(within(panel).getByText("Your CLI setup", { selector: "dt" })).toBeTruthy();
    expect(factOf("Your CLI setup").textContent).toBe("none loaded");
    expect(factOf("Model").textContent).toBe("claude-opus-5-5");
    expect(factOf("MCP servers").textContent).toBe("ropeshift (ropeshift), docs (the repository's)");
    expect(factOf("Agents").textContent).toBe("2");
    expect(factOf("Skills").textContent).toBe("1");
    expect(factOf("Plugins").textContent).toBe("none");
    expect(factOf("Generation").textContent).toBe("2");
    expect(factOf("Version").textContent).toBe("2.1.330");
  });

  it("lists each piece of the member's own setup that reached the lane, with where it is set up", () => {
    render(
      <RunsWith
        setup={{
          ...clean,
          personal: [
            { kind: "mcp_server", name: "github", from: "user" },
            { kind: "agent", name: "reviewer", from: "~/.claude/agents/reviewer.md" },
          ],
        }}
      />,
    );
    const items = within(factOf("Your CLI setup")).getAllByRole("listitem").map((item) => item.textContent);
    expect(items).toEqual(["MCP server github, from user", "Agent reviewer, from ~/.claude/agents/reviewer.md"]);
  });

  it("says a lane whose session reported nothing has not said what it loaded", () => {
    render(<RunsWith />);
    expect(factOf("Your CLI setup").textContent).toBe("not reported");
  });
});
