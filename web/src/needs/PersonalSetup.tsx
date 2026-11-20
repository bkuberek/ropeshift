import { acceptPersonalSetup } from "../api/client";
import type { PersonalSetupNeed } from "../api/types";
import { addressOf, Link } from "../app/route";
import { plainLine } from "../planning/creation";
import { Button } from "../ui/controls";
import { DecisionCard, DecisionSection } from "../ui/DecisionCard";
import { Fact, Facts, Refusal } from "../ui/display";
import type { Frame } from "./frame";
import { pieceWords, setupKind } from "./kinds";
import { useAct } from "./useAct";

// setupHelp is the guide that says where each piece of a member's own agent CLI setup is set up, and how to keep it out
// of lanes or give a project's lanes what they need.
export const setupHelp = "https://github.com/bkuberek/ropeshift/blob/main/docs/settings-and-agents.md#your-own-agent-cli-setup";

// The choices of a leak, as the recommendation names them.
const choiceWords: Record<PersonalSetupNeed["recommendation"], string> = {
  accept: "accept it for this machine's lanes",
  separate_directory: "run this machine's lanes in a separate configuration directory",
};

// PersonalSetupCard is one piece of the member's own agent CLI setup that reached a machine's lanes on one CLI version
// despite the flags that keep it out (ADR-0040). Lanes keep running while it waits. Accepting it is the card's one act,
// and it leads only when it is what the card recommends; the separate configuration directory is not built yet, and
// removing the piece is the member's own change, which ropeshift never makes.
export function PersonalSetupCard({ need, after, frame }: { need: PersonalSetupNeed; after: () => Promise<void>; frame: Frame }) {
  const { act, busy, failure } = useAct(after);
  const piece = need.piece;
  const kind = pieceWords[piece.kind] ?? piece.kind;
  const accept = () => void act(() => acceptPersonalSetup(need.worker, { version: need.version, kind: piece.kind, name: piece.name }));
  return (
    <DecisionCard
      kind={setupKind}
      project={frame.project}
      title={frame.title}
      provenance={frame.provenance}
      binding={`Bound to worker ${plainLine(need.worker)} on version ${plainLine(need.version)}. Accepting it applies to that machine and version only.`}
      actions={
        need.actions.includes("setup_accept") ? (
          <Button variant={need.recommendation === "accept" ? "primary" : "secondary"} loading={busy} disabled={busy} data-focus={`Accept ${kind} ${plainLine(piece.name)}`} onClick={accept}>
            Accept it for this machine's lanes
          </Button>
        ) : undefined
      }
    >
      <p>
        Lanes on this machine loaded your own {kind} {plainLine(piece.name)}, though the flags they start with keep your own setup out. Lanes keep running while this
        waits, with it named on each.
      </p>
      <Facts>
        <Fact name="Piece">
          {kind} {plainLine(piece.name)}
        </Fact>
        {piece.from ? (
          <Fact name="Set up in" mono>
            {plainLine(piece.from)}
          </Fact>
        ) : null}
        <Fact name="Worker" mono>
          {plainLine(need.worker)}
        </Fact>
        <Fact name="Version" mono>
          {plainLine(need.version)}
        </Fact>
      </Facts>
      <DecisionSection title="Lanes it reached">
        <ul className="criteria" aria-label="Lanes it reached">
          {need.lanes.map((lane) => (
            <li key={lane.id}>
              <Link to={addressOf("/lane", { id: lane.id })}>{lane.id}</Link> · {lane.title ? plainLine(lane.title) : lane.work_order}
            </li>
          ))}
        </ul>
      </DecisionSection>
      <DecisionSection title="Choices">
        <ul className="criteria" aria-label="Choices">
          <li>
            <strong>Accept it for this machine's lanes</strong>: it counts as on for this machine's lanes on this version, and is not raised again there.
          </li>
          <li>
            <strong>Run this machine's lanes in a separate configuration directory</strong>: not available yet, a follow-up adds it. Lanes would run with a
            configuration directory ropeshift owns, which needs a second sign-in.
          </li>
          <li>
            <strong>Remove it from my own setup</strong>: change your own configuration where it is set up, which ropeshift never edits. A lane started after it
            loads clean.{" "}
            <a href={setupHelp} target="_blank" rel="noreferrer">
              Where each piece is set up
            </a>
            .
          </li>
        </ul>
        <p>
          Recommended: {choiceWords[need.recommendation] ?? plainLine(need.recommendation)}
          {need.recommendation_rationale ? `, because ${plainLine(need.recommendation_rationale)}` : ""}.
        </p>
      </DecisionSection>
      {failure ? <Refusal>{failure}</Refusal> : null}
    </DecisionCard>
  );
}
