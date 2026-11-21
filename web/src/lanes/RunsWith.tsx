import type { LaneSetup, PersonalItem, SetupServer } from "../api/types";
import { Absent, Fact, Facts, Panel } from "../ui/display";

// pieceWords names each kind of the member's own setup as a person reads it.
const pieceWords: Record<string, string> = {
  mcp_server: "MCP server",
  plugin: "Plugin",
  agent: "Agent",
  skill: "Skill",
  command: "Command",
  instruction: "Instruction file",
};

// personalLine says one piece of the member's own setup and where it is set up.
export function personalLine(item: PersonalItem): string {
  const kind = pieceWords[item.kind] ?? item.kind;
  return item.from ? `${kind} ${item.name}, from ${item.from}` : `${kind} ${item.name}`;
}

// serverWords says where an MCP server came from: ropeshift's own, the repository's .mcp.json, or a scope the CLI named.
function serverWords(server: SetupServer): string {
  return `${server.name} (${server.from === "repository" ? "the repository's" : server.from || "unknown"})`;
}

// RunsWith is what a lane's latest session loaded at its start, as its agent CLI reported it. A lane runs clean of the
// member's own CLI setup, so the first line says whether any of it reached the lane anyway, and names each piece that did.
export function RunsWith({ setup }: { setup?: LaneSetup }) {
  if (!setup) {
    return (
      <Panel title="Runs with">
        <Facts>
          <Fact name="Your CLI setup">
            <Absent>not reported</Absent>
          </Fact>
        </Facts>
        <p className="muted small">Its sessions said nothing of what they loaded. A session in a pane reports nothing.</p>
      </Panel>
    );
  }
  const personal = setup.personal ?? [];
  const servers = setup.mcp_servers ?? [];
  const plugins = setup.plugins ?? [];
  return (
    <Panel title="Runs with">
      <Facts>
        <Fact name="Your CLI setup">
          {personal.length === 0 ? (
            "none loaded"
          ) : (
            <ul className="criteria" aria-label="Your CLI setup that loaded">
              {personal.map((item) => (
                <li key={`${item.kind}:${item.name}`}>{personalLine(item)}</li>
              ))}
            </ul>
          )}
        </Fact>
        {setup.model ? <Fact name="Model">{setup.model}</Fact> : null}
        <Fact name="MCP servers">{servers.length ? servers.map(serverWords).join(", ") : <Absent>none</Absent>}</Fact>
        <Fact name="Agents">{(setup.agents ?? []).length}</Fact>
        <Fact name="Skills">{(setup.skills ?? []).length}</Fact>
        <Fact name="Plugins">{plugins.length ? plugins.join(", ") : <Absent>none</Absent>}</Fact>
        {setup.version ? <Fact name="Version">{setup.version}</Fact> : null}
        <Fact name="Generation">{setup.generation}</Fact>
      </Facts>
    </Panel>
  );
}
