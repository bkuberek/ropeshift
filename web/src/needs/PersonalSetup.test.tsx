import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { NeedItem, PersonalSetupNeed } from "../api/types";
import { CurrentProject } from "../app/route";
import { serveApi, testProject } from "../testing";
import { cardKindOf, cardKinds, contextOf, filterOf, headingOf, kindOf } from "./kinds";
import { PersonalSetupCard } from "./PersonalSetup";

function need(piece: PersonalSetupNeed["piece"], recommendation: PersonalSetupNeed["recommendation"]): PersonalSetupNeed {
  return {
    worker: "worker-a",
    version: "2.1.330",
    piece,
    lanes: [
      { id: "lane-1", work_order: "wo-1", title: "Add the retry budget" },
      { id: "lane-2", work_order: "wo-2" },
    ],
    recommendation,
    recommendation_rationale: "it adds context, and the lane's generated rules still bound every tool call it leads to",
    actions: ["setup_accept"],
  };
}

const agent = need({ kind: "agent", name: "reviewer", from: "~/.claude/agents/reviewer.md" }, "accept");
const server = need({ kind: "mcp_server", name: "github", from: "user" }, "separate_directory");

function show(setup: PersonalSetupNeed, after: () => Promise<void> = async () => {}) {
  return render(
    <CurrentProject.Provider value={testProject}>
      <PersonalSetupCard need={setup} after={after} frame={{ title: "Your agent reviewer reached lanes on worker-a" }} />
    </CurrentProject.Provider>,
  );
}

describe("a piece of the member's own setup that leaked", () => {
  it("is a decision of its own kind, titled by the piece and the machine", () => {
    const item: NeedItem = { kind: "personal_setup", id: "worker-a/2.1.330/agent/reviewer", since: 0, personal_setup: agent };
    expect(cardKinds).toContain("personal-setup");
    expect(cardKindOf(item)).toBe("personal-setup");
    expect(filterOf(item)).toBe("decisions");
    expect(kindOf(item).name).toBe("Your CLI setup in lanes");
    expect(headingOf(item)).toBe("Your agent reviewer reached lanes on worker-a");
    expect(contextOf(item)).toBe("Worker worker-a · version 2.1.330 · 2 lanes");
  });

  it("names where the piece comes from and the lanes it reached, with the three choices", () => {
    show(agent);
    expect(screen.getByText("~/.claude/agents/reviewer.md")).toBeTruthy();
    const reached = within(screen.getByRole("list", { name: "Lanes it reached" }))
      .getAllByRole("listitem")
      .map((item) => item.textContent);
    expect(reached).toEqual(["lane-1 · Add the retry budget", "lane-2 · wo-2"]);
    const choices = within(screen.getByRole("list", { name: "Choices" }))
      .getAllByRole("listitem")
      .map((item) => item.textContent ?? "");
    expect(choices[0]).toMatch(/^Accept it for this machine's lanes: /);
    expect(choices[1]).toMatch(/^Run this machine's lanes in a separate configuration directory: not available yet, a follow-up adds it\./);
    expect(choices[2]).toMatch(/^Remove it from my own setup: /);
    expect(screen.getByText(/Lanes keep running while this waits/)).toBeTruthy();
  });

  it("leads with Accept when the card recommends accepting, and records it for the machine and version", async () => {
    const sent = serveApi((call) => (call.method === "POST" && call.path === "/v1/workers/worker-a/personal-setup/acceptances" ? { status: 204 } : undefined));
    const after = vi.fn(async () => {});
    show(agent, after);
    expect(screen.getByText(/Recommended: accept it for this machine's lanes, because it adds context/)).toBeTruthy();
    const accept = screen.getByRole("button", { name: "Accept it for this machine's lanes" });
    expect(accept.className).toMatch(/primary/);
    await userEvent.click(accept);
    await waitFor(() => expect(after).toHaveBeenCalled());
    expect(sent.find((call) => call.method === "POST")?.body).toEqual({ version: "2.1.330", kind: "agent", name: "reviewer" });
  });

  it("recommends the separate directory for an MCP server, so Accept does not lead", () => {
    show(server);
    expect(screen.getByText(/Recommended: run this machine's lanes in a separate configuration directory, because/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Accept it for this machine's lanes" }).className).not.toMatch(/primary/);
  });
});
