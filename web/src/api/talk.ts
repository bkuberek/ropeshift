import { newID } from "../admin/logic";
import { call, project } from "./client";
import type { Approval } from "./types";

// What the conversation surface reads and sends: the member's conversations with the assistant, what the
// member can talk to, and the streams of a conversation, a lane, and an orchestrator. Every value here is
// data, and text an agent wrote stays text.

export type AssistantWorker = { id: string; name: string; available: boolean; reason?: string };

// Where an assistant's session stands, as its worker last reported it.
export type SessionState = "starting" | "running" | "working" | "stopping" | "stopped" | "failed";

export type Conversation = {
  id: string;
  member_id: string;
  name: string;
  worker_id?: string;
  desired_state: "running" | "stopped";
  actual_state: SessionState;
  run_lane?: string;
  current_generation: number;
  stopped_reason?: string;
  created_at: number;
  last_active_at: number;
  worker_name?: string;
  open_cards: number;
};

export type ConversationList = { as_of: number; conversations: Conversation[]; workers: AssistantWorker[] };

// Where a message stands between the ledger and the session it is for.
export type DeliveryState = "queued" | "at_worker" | "handed_off" | "admitted" | "replied" | "refused" | "expired";

export type TalkMessage = {
  id: string;
  to: { kind?: string; lane: string; worker?: string; conversation?: string; generation: number };
  from: { kind: string; id: string; seq: number };
  kind: string;
  body: string;
  created_at: number;
  state: DeliveryState;
  reason?: string;
  reply?: string;
};

export type TalkFrom = { kind: string; id: string; label?: string; delegate_kind?: string };

export type StreamEvent = {
  rev: number;
  type: string;
  subject_kind: string;
  subject_id: string;
  actor_kind: string;
  actor_id: string;
  occurred_at?: number;
  recorded_at: number;
  actor_for?: string;
  actor_label?: string;
  delegate_kind?: string;
};

export type TalkAction = { operation: string; label: string; subject?: string; outcome: "ran" | "card"; approval?: string; rev?: number; planning_lane?: string };

// A question as a stream holds it: the record, the batch it reaches a person in, and where a member's
// answer came from.
export type TalkQuestionRecord = {
  id: string;
  text: string;
  status: string;
  categories?: string[];
  blocking?: boolean;
  options?: string[];
  recommendation?: string;
  escalation_reason?: string;
  asked_by?: string;
};

export type TalkQuestion = { question: TalkQuestionRecord; batch?: string; channel?: string };

export type EntryKind = "member" | "reply" | "event" | "action" | "card" | "request" | "question" | "marker";

export type TalkEntry = {
  id: string;
  at: number;
  kind: EntryKind;
  from?: TalkFrom;
  body?: string;
  message?: TalkMessage;
  event?: StreamEvent;
  action?: TalkAction;
  approval?: Approval;
  question?: TalkQuestion;
  marker?: string;
  assent?: boolean;
  before_clear?: boolean;
};

export type TalkPage = { entries: TalkEntry[]; next?: string; as_of: number };

export type OrchestratorTarget = { worker: string; name: string; state: string; desired_state: string; generation: number; projects: string[] };

export type SessionTarget = {
  lane: string;
  work_order: string;
  title?: string;
  kind: string;
  worker?: string;
  worker_name?: string;
  generation: number;
  run_mode?: string;
  state: string;
};

export type AssistantTarget = { conversation: Conversation | null; workers: AssistantWorker[]; idle_stop_ms: number; warm_idle_stop_ms: number };

export type ConversationTargets = {
  as_of: number;
  assistant: AssistantTarget;
  orchestrators: OrchestratorTarget[];
  planning: SessionTarget[];
  lanes: SessionTarget[];
};

export type ConversationOpened = { conversation: Conversation; stopped: Conversation[] };

export type ConversationSent = { conversation: Conversation; entry: TalkEntry; message: TalkMessage; stopped: Conversation[] };

const conversation = (id: string, verb = "") => project(`/conversations/${encodeURIComponent(id)}${verb ? `/${verb}` : ""}`);

const page = (before: string) => (before ? `?${new URLSearchParams({ before })}` : "");

export const talkPaths = {
  conversations: () => project("/conversations"),
  conversation,
  entries: (id: string, before = "") => `${conversation(id, "entries")}${page(before)}`,
  targets: () => project("/conversation-targets"),
  messages: () => project("/messages"),
  laneStream: (lane: string, before = "") => `${project(`/lanes/${encodeURIComponent(lane)}/stream`)}${page(before)}`,
  orchestratorStream: (worker: string, before = "") => `${project(`/orchestrators/${encodeURIComponent(worker)}/stream`)}${page(before)}`,
};

export const readConversations = () => call<ConversationList>("GET", talkPaths.conversations());

export const readConversationTargets = () => call<ConversationTargets>("GET", talkPaths.targets());

export const readConversationEntries = (id: string, before = "") => call<TalkPage>("GET", talkPaths.entries(id, before));

export const readLaneStream = (lane: string, before = "") => call<TalkPage>("GET", talkPaths.laneStream(lane, before));

export const readOrchestratorStream = (worker: string, before = "") => call<TalkPage>("GET", talkPaths.orchestratorStream(worker, before));

// The ID is the client's, so a retry makes one conversation. A worker is named only when the member chose
// one among several.
export const createConversation = (id: string, name = "", worker = "") =>
  call<ConversationOpened>("POST", talkPaths.conversations(), { id, ...(name ? { name } : {}), ...(worker ? { worker } : {}) });

// Opening starts the session and resumes what it had, and sends nothing: it is the warm start a member's
// first keystroke makes.
export const openConversation = (id: string, worker = "") => call<ConversationOpened>("POST", talkPaths.conversation(id, "open"), worker ? { worker } : {});

export const stopConversation = (id: string) => call<Conversation>("POST", talkPaths.conversation(id, "stop"), {});

export const renameConversation = (id: string, name: string) => call<Conversation>("POST", talkPaths.conversation(id, "rename"), { name });

export const deleteConversation = (id: string) => call<void>("POST", talkPaths.conversation(id, "delete"), {});

export const compactConversation = (id: string) => call<Conversation>("POST", talkPaths.conversation(id, "compact"), {});

export const clearConversation = (id: string) => call<Conversation>("POST", talkPaths.conversation(id, "clear"), {});

// A member's message to the assistant opens a stopped conversation first. Its ID is the client's.
export const sendToAssistant = (id: string, body: string, worker = "", messageID = newID()) =>
  call<ConversationSent>("POST", talkPaths.conversation(id, "messages"), { id: messageID, body, ...(worker ? { worker } : {}) });

// A message to a lane, a planning session, or an orchestrator is an instruction that decides nothing.
export type MessageTo = { lane: string } | { kind: "orchestrator"; worker: string };

export const sendMessage = (to: MessageTo, body: string, messageID = newID()) =>
  call<TalkMessage>("POST", talkPaths.messages(), { id: messageID, to, kind: "instruction", body });
