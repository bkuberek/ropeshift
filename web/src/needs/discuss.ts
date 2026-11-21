import { useContext } from "react";

import type { Place } from "../api/client";
import { readConversationTargets } from "../api/talk";
import type { QuestionItem } from "../api/types";
import { CurrentProject, ItemProject } from "../app/route";
import type { Talk } from "../talk/state";
import { useTalk } from "../talk/state";
import type { TalkKind } from "../talk/target";

// aboutQuestion starts a message about a question, so the session reads the message as about that question.
export const aboutQuestion = (id: string) => `About question ${id}: `;

const oneLine = (text: string) => text.replace(/\s+/g, " ").trim();

// sessionOf is the conversation of the session that asked a question: the planning session or the lane, while it
// runs. Any other question, and one whose session ended, is the assistant's to discuss.
async function sessionOf(lane: string): Promise<{ kind: TalkKind; id: string } | null> {
  if (!lane) {
    return null;
  }
  try {
    const targets = await readConversationTargets();
    if (targets.planning.some((row) => row.lane === lane)) {
      return { kind: "planning", id: lane };
    }
    return targets.lanes.some((row) => row.lane === lane) ? { kind: "lane", id: lane } : null;
  } catch {
    return null;
  }
}

// discuss opens the conversation with the session that asked, with the question pinned at its top. A message there
// never answers the question; its card does. A draft the member already wrote there stays as they wrote it.
async function discuss(talk: Talk, project: string, item: QuestionItem): Promise<void> {
  const session = await sessionOf(item.lane ?? "");
  const pinned = `Question ${item.id}: ${oneLine(item.text)}. A message here never answers it: its card in Needs you does.`;
  const ended = item.lane ? `Lane ${item.lane} is no longer running, so the assistant takes it. ` : "No session that asked it is running, so the assistant takes it. ";
  const target = session ?? { kind: "assistant" as const, id: "" };
  talk.select(target.kind, target.id, { open: true, focus: true, notice: session ? pinned : `${ended}${pinned}` });
  const at = { project, ...target };
  if (talk.draftOf(at).trim() === "") {
    talk.setDraft(at, aboutQuestion(item.id));
  }
}

// useDiscuss is what Discuss does on a question's card, or undefined where it has nothing to open: in the
// conversation the question already stands in, and for a question of a project the page does not show.
export function useDiscuss(place?: Place): ((item: QuestionItem) => void) | undefined {
  const talk = useTalk();
  const current = useContext(CurrentProject);
  const project = useContext(ItemProject);
  if (place === "conversation" || (project !== "" && project !== current)) {
    return undefined;
  }
  return (item) => void discuss(talk, current, item);
}
