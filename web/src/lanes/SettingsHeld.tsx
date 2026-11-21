import type { SettingsChange } from "../api/types";
import { addressOf, Link } from "../app/route";
import { settingsWaiting } from "../needs/kinds";
import { plainLine } from "../planning/creation";

// SettingsHeld says what a lane held on its own settings waits on, and where a member ends the wait (FR-UI-13): the
// lane changed its own hooks or permissions, and its Needs you item offers the choices.
export function SettingsHeld({ lane, hold, small = false }: { lane: string; hold?: SettingsChange; small?: boolean }) {
  if (!hold) {
    return null;
  }
  return (
    <span className={small ? "cell-note" : undefined} role="status">
      {settingsWaiting} ({hold.keys.map(plainLine).join(", ")}).{" "}
      <Link to={addressOf("/needs-you", { item: `lane_settings:${lane}` })}>Choose in Needs you</Link>
    </span>
  );
}
