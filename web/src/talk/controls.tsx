import { createContext, useContext } from "react";

import { useRead } from "../admin/parts";
import { readConversationTargets } from "../api/talk";
import type { SessionTarget } from "../api/talk";
import type { Live } from "../api/useView";
import { Button } from "../ui/controls";
import { useTalk } from "./state";

// The controls other views carry to talk to a target: they switch the target, open the dock on it, and put the
// cursor in its message box. Nothing else on a view does (FR-CHAT-11).

// Talkable is the lanes a member can talk to now, by lane, which says whether one runs headless.
export const Talkable = createContext<Map<string, SessionTarget>>(new Map());

// useTalkable reads the lanes and planning sessions a member can talk to. A read that fails leaves none, so a
// view offers no control it cannot back.
export function useTalkable(live: Live, project: string): Map<string, SessionTarget> {
  const read = useRead(() => readConversationTargets(), `talkable:${project}`, live);
  const sessions = new Map<string, SessionTarget>();
  for (const row of [...(read.view?.planning ?? []), ...(read.view?.lanes ?? [])]) {
    sessions.set(row.lane, row);
  }
  return sessions;
}

// TalkToLane is a lane row's control: Message for a lane that runs headless, and Open pane for one that runs
// in a pane. A lane that cannot be talked to has none.
export function TalkToLane({ lane }: { lane: string }) {
  const talk = useTalk();
  const found = useContext(Talkable).get(lane);
  if (!found) {
    return null;
  }
  const headless = found.run_mode === "headless";
  return (
    <Button small icon="chat" aria-label={headless ? `Message lane ${lane}` : `Open pane of lane ${lane}`} onClick={() => talk.select("lane", lane, { open: true, focus: true })}>
      {headless ? "Message" : "Open pane"}
    </Button>
  );
}
