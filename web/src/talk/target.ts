import type { Conversation, ConversationTargets, OrchestratorTarget, SessionTarget } from "../api/talk";
import type { IconName } from "../ui/Icon";
import type { TargetKind } from "../ui/display";

// A member talks to one target at a time: the assistant, an orchestrator, a planning session, or a lane. The
// target belongs to a project, and so does each draft, so moving between views, projects, and targets never
// loses what a member wrote and never changes what they talk to (FR-CHAT-8, FR-CHAT-11).

export type TalkKind = TargetKind;

// id is the conversation of the assistant (empty for the newest one), the worker of an orchestrator, and the
// lane of a planning session or a lane.
export type Target = { project: string; kind: TalkKind; id: string };

export const assistantOf = (project: string): Target => ({ project, kind: "assistant", id: "" });

export const targetKey = (target: Target): string => `${target.project}\u0000${target.kind}\u0000${target.id}`;

export const sameTarget = (a: Target, b: Target): boolean => targetKey(a) === targetKey(b);

// Each kind's own label, icon, and one line on what it can do. Its color is the kind's token, in the styles.
export const kinds: Record<TalkKind, { label: string; icon: IconName; can: string }> = {
  assistant: { label: "Assistant", icon: "delegate", can: "Acts for you through the API. Operational actions run at once; decisions become cards you confirm." },
  orchestrator: {
    label: "Orchestrator",
    icon: "hub",
    can: "Claims work, works the question ladder, decides delegated approvals, and recovers stuck work within policy. A message wakes it once and grants it nothing.",
  },
  planning: { label: "Planning session", icon: "flag", can: "Writes the plan and its draft decisions on its own branch. Approving the plan stays yours." },
  lane: { label: "Lane", icon: "terminal", can: "Works within its work order's generated rules. A message reaches it once and grants it nothing." },
};

// TalkState is what the page remembers while it runs: the target of each project, and one draft for each
// target. A reload starts again at the assistant.
export type TalkState = { targets: Record<string, { kind: TalkKind; id: string }>; drafts: Record<string, string> };

export const empty: TalkState = { targets: {}, drafts: {} };

export function targetOf(state: TalkState, project: string): Target {
  const held = state.targets[project];
  return held ? { project, ...held } : assistantOf(project);
}

export function selectTarget(state: TalkState, target: Target): TalkState {
  return { ...state, targets: { ...state.targets, [target.project]: { kind: target.kind, id: target.id } } };
}

// A draft belongs to a target, and the assistant is one target whichever of its conversations the member is in,
// so what a member began writing to it stays when the conversation it opens is chosen.
const draftKey = (target: Target): string => targetKey(target.kind === "assistant" ? { ...target, id: "" } : target);

export const drafting = (state: TalkState, target: Target): string => state.drafts[draftKey(target)] ?? "";

export function rememberDraft(state: TalkState, target: Target, text: string): TalkState {
  const drafts = { ...state.drafts };
  if (text === "") {
    delete drafts[draftKey(target)];
  } else {
    drafts[draftKey(target)] = text;
  }
  return { ...state, drafts };
}

// What the header, the composer, and the list say of a target.
export type Described = {
  kind: TalkKind;
  name: string;
  which: string;
  can: string;
  // to is the composer's "To" line after the chip.
  to: string;
  // The composer's accessible label, its placeholder, and its send button.
  label: string;
  placeholder: string;
  send: string;
  streamLabel: string;
  // mode is how the target shows: its stream and a composer, or its live pane.
  mode: "stream" | "pane";
  // ended is why a composer is replaced, when the target takes no message now.
  ended?: string;
  lane?: SessionTarget;
  orchestrator?: OrchestratorTarget;
};

export type Facts = {
  projectLabel: string;
  // you is the member's name, which the assistant acts for.
  you: string;
  // targets is what the member can talk to, or null until it is read.
  targets: ConversationTargets | null;
  // conversation is the assistant's conversation the target names, or the newest one, or none.
  conversation?: Conversation | null;
  // laneEnd is why a lane that left the list ended, when the floor knows.
  laneEnd?: string;
};

const headless = (lane: SessionTarget | undefined) => lane?.run_mode === "headless";

export function describeTarget(target: Target, facts: Facts): Described {
  const { kind } = target;
  const base = { kind, can: kinds[kind].can };
  if (kind === "assistant") {
    return {
      ...base,
      name: facts.conversation?.name ?? "New conversation",
      which: `${facts.projectLabel} · your assistant, ${facts.you}`,
      to: `in ${facts.projectLabel}`,
      label: "Message to the assistant",
      placeholder: "Message the assistant…",
      send: "Send to the assistant",
      streamLabel: "Messages with the assistant",
      mode: "stream",
    };
  }
  if (kind === "orchestrator") {
    const found = facts.targets?.orchestrators.find((row) => row.worker === target.id);
    const name = found?.name || target.id;
    let ended: string | undefined;
    if (facts.targets && !found) {
      ended = `The orchestrator on ${name} does not serve this project.`;
    } else if (found && found.state !== "running") {
      ended = `The orchestrator on ${name} is not running. Start it from Workers.`;
    }
    return {
      ...base,
      name: `Orchestrator on ${name}`,
      which: `${name} · serves ${found?.projects.length ? found.projects.join(", ") : facts.projectLabel}`,
      to: `on ${name}`,
      label: `Message to the orchestrator on ${name}`,
      placeholder: `Message the orchestrator on ${name}…`,
      send: `Send to the orchestrator on ${name}`,
      streamLabel: `Event stream of the orchestrator on ${name}`,
      mode: "stream",
      ended,
      orchestrator: found,
    };
  }
  // A control that asks to talk to a lane may name a planning session, and the list says which it is.
  const inPlanning = facts.targets?.planning.find((row) => row.lane === target.id);
  const found = inPlanning ?? facts.targets?.lanes.find((row) => row.lane === target.id);
  const session: TalkKind = found ? (inPlanning ? "planning" : "lane") : kind;
  const noun = session === "planning" ? "planning session" : "lane";
  const mode = found && !headless(found) ? "pane" : !found && session === "planning" ? "pane" : "stream";
  const worker = found?.worker_name || found?.worker || "";
  const run = headless(found) ? "headless" : "interactive";
  let ended: string | undefined;
  if (facts.targets && !found) {
    ended = `${session === "planning" ? "Planning session" : "Lane"} ${target.id} ended${facts.laneEnd ? `: ${facts.laneEnd}` : ""}.`;
  }
  const which =
    session === "planning"
      ? [found?.work_order, target.id, worker].filter(Boolean).join(" · ")
      : [target.id, found?.work_order, worker, found ? run : ""].filter(Boolean).join(" · ");
  return {
    kind: session,
    can: kinds[session].can,
    name: found?.title || `${session === "planning" ? "Planning session" : "Lane"} ${target.id}`,
    which,
    to: session === "planning" ? which : [target.id, found?.work_order, found ? run : ""].filter(Boolean).join(" · "),
    label: `Message to ${noun} ${target.id}`,
    placeholder: `Message ${noun} ${target.id}…`,
    send: `Send to ${noun} ${target.id}`,
    streamLabel: `Event stream of ${noun} ${target.id}`,
    mode: ended ? "stream" : mode,
    ended,
    lane: found,
  };
}

// announcement is what a polite live region says when a switch happens.
export function announcement(target: Target, name = ""): string {
  switch (target.kind) {
    case "assistant":
      return "Talking to the assistant";
    case "orchestrator":
      return `Talking to the orchestrator on ${name || target.id}`;
    case "planning":
      return `Talking to planning session ${target.id}`;
    case "lane":
      return `Talking to lane ${target.id}`;
  }
}
