import { decideLaneSettings } from "../api/client";
import type { LaneSettingsNeed } from "../api/types";
import { addressOf, Link } from "../app/route";
import { plainLine } from "../planning/creation";
import { Button } from "../ui/controls";
import { DecisionCard, DecisionSection } from "../ui/DecisionCard";
import { Fact, Facts, Refusal } from "../ui/display";
import type { Frame } from "./frame";
import { settingsKind, settingsWaiting } from "./kinds";
import { useAct } from "./useAct";

// The choices of a lane held on its own settings, as the buttons and the recommendation name them.
export const settingsChoiceWords: Record<LaneSettingsNeed["recommendation"], string> = {
  base: "Run with the repository's settings (base)",
  lane: "Run with this lane's settings",
  stop: "Stop",
};

// LaneSettingsCard is a lane whose worktree changed the project settings its agent CLI reads, in a key that runs code or
// widens access, which parked before its session started (ADR-0040). It is in the security category, so only a member
// answers it. It names the keys and what changed of each, and offers the choices the item lists, the repository's
// settings first and recommended; each binds to the change the member read. A hold from a script a hook or a setting of
// the repository runs offers no restore, because putting the script back would revert the lane's work, and says so.
export function LaneSettingsCard({ need, after, frame }: { need: LaneSettingsNeed; after: () => Promise<void>; frame: Frame }) {
  const { act, busy, failure } = useAct(after);
  const change = need.change;
  const choose = (choice: LaneSettingsNeed["recommendation"]) => () => void act(() => decideLaneSettings(need.work_order.id, { lane: need.lane, digest: change.digest, choice }));
  const offers = (action: LaneSettingsNeed["actions"][number]) => need.actions.includes(action);
  return (
    <DecisionCard
      kind={settingsKind}
      project={frame.project}
      title={frame.title}
      provenance={frame.provenance}
      binding="Bound to this change of the lane's settings. A lane that changes them again is held again."
      actions={
        <>
          {offers("settings_stop") ? (
            <Button variant="danger-outline" disabled={busy} onClick={choose("stop")}>
              {settingsChoiceWords.stop}
            </Button>
          ) : null}
          {offers("settings_lane") ? (
            <Button variant="secondary" disabled={busy} onClick={choose("lane")}>
              {settingsChoiceWords.lane}
            </Button>
          ) : null}
          {offers("settings_base") ? (
            <Button variant="primary" loading={busy} disabled={busy} onClick={choose("base")}>
              {settingsChoiceWords.base}
            </Button>
          ) : null}
        </>
      }
    >
      <p>
        {settingsWaiting}. Lane {plainLine(need.lane)} changed what its next session would run, outside every rule ropeshift gives it, so it stopped before
        that session started. Nothing runs until a member chooses.
      </p>
      <Facts>
        <Fact name="Lane" mono>
          <Link to={addressOf("/lane", { id: need.lane })}>{plainLine(need.lane)}</Link>
        </Fact>
        <Fact name="Work order">{need.work_order.title ? plainLine(need.work_order.title) : need.work_order.id}</Fact>
        <Fact name="Category">Security, which only a member decides</Fact>
        <Fact name="Keys changed" mono>
          {change.keys.map(plainLine).join(", ")}
        </Fact>
        <Fact name="Files" mono>
          {change.files.map(plainLine).join(", ")}
        </Fact>
      </Facts>
      <DecisionSection title="What changed">
        <ul className="criteria" aria-label="What changed">
          {(change.summary ?? []).map((line, at) => (
            <li key={at} className="mono">
              {plainLine(line)}
            </li>
          ))}
        </ul>
      </DecisionSection>
      <DecisionSection title="Choices">
        <ul className="criteria" aria-label="Choices">
          {offers("settings_base") ? (
            <li>
              <strong>{settingsChoiceWords.base}</strong>: the lane's settings files are put back as the commit it began from holds them before its next session
              starts, and the lane is told so. Its own change to them leaves the worktree.
            </li>
          ) : null}
          {offers("settings_lane") ? (
            <li>
              <strong>{settingsChoiceWords.lane}</strong>: its next session runs with what it wrote, recorded as your decision for this change.
            </li>
          ) : null}
          {offers("settings_stop") ? (
            <li>
              <strong>{settingsChoiceWords.stop}</strong>: its work is blocked with the reason settings_changed, for you to resolve or replan.
            </li>
          ) : null}
        </ul>
        {change.scripts?.length ? (
          <p>
            No restore is offered: the lane changed {change.scripts.map(plainLine).join(", ")}, which a hook or a setting of the repository runs, and putting it
            back would revert the lane's work. Run the lane with its version only if you trust it, or stop it.
          </p>
        ) : null}
        <p>
          Recommended: {settingsChoiceWords[need.recommendation] ?? plainLine(need.recommendation)}
          {need.recommendation_rationale ? `, because ${plainLine(need.recommendation_rationale)}` : ""}.
        </p>
      </DecisionSection>
      {failure ? <Refusal>{failure}</Refusal> : null}
    </DecisionCard>
  );
}
