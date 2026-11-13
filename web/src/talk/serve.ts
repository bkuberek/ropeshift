import type { Conversation, ConversationList, ConversationTargets, TalkEntry, TalkPage } from "../api/talk";
import type { Sent } from "../testing";
import { golden, refusal, serveViews } from "../testing";

// What a test of the conversation surface serves: a conversation, what the member can talk to, and the
// streams, from what a real ledger wrote. A test changes what stands here, and what a change answers.
export type Talking = {
  conversations: Conversation[];
  workers: ConversationList["workers"];
  entries: TalkEntry[];
  targets: ConversationTargets;
  lane: TalkPage;
  orchestrator: TalkPage;
  // answers is what a change of the conversation answers, by "METHOD path".
  answers: Record<string, (call: Sent) => { status: number; body?: unknown } | undefined>;
};

export function talking(): Talking {
  const list = golden<ConversationList>("conversations");
  return {
    conversations: list.conversations,
    workers: list.workers,
    entries: golden<TalkPage>("conversation-entries").entries,
    targets: golden<ConversationTargets>("conversation-targets"),
    lane: golden<TalkPage>("lane-stream"),
    orchestrator: golden<TalkPage>("orchestrator-stream"),
    answers: {},
  };
}

export const aConversation = (over: Partial<Conversation> = {}): Conversation => ({ ...golden<ConversationList>("conversations").conversations[0]!, ...over });

// serveTalk answers the conversation routes from `state`, and everything else as `serveViews` does. It returns
// the calls the page made.
export function serveTalk(state: Talking) {
  return serveViews((call) => {
    const answered = state.answers[`${call.method} ${call.path}`]?.(call);
    if (answered) {
      return answered;
    }
    if (call.method === "GET") {
      if (call.path === "/v1/conversation-targets") {
        return { status: 200, body: { ...state.targets, assistant: { ...state.targets.assistant, conversation: state.conversations[0] ?? null, workers: state.workers } } };
      }
      if (call.path === "/v1/conversations") {
        return { status: 200, body: { as_of: state.targets.as_of, conversations: state.conversations, workers: state.workers } };
      }
      if (/^\/v1\/conversations\/[^/]+\/entries/.test(call.path)) {
        return { status: 200, body: { entries: state.entries, as_of: state.targets.as_of } };
      }
      if (/^\/v1\/lanes\/[^/]+\/stream/.test(call.path)) {
        return { status: 200, body: state.lane };
      }
      if (/^\/v1\/orchestrators\/[^/]+\/stream/.test(call.path)) {
        return { status: 200, body: state.orchestrator };
      }
      if (call.path === "/v1/members") {
        return { status: 200, body: golden("members") };
      }
    }
    if (call.method === "POST") {
      if (/^\/v1\/conversations(\/|$)/.test(call.path) || call.path === "/v1/messages" || /\/decide$/.test(call.path)) {
        const conversation = state.conversations[0];
        return { status: 201, body: { conversation, stopped: [], message: { id: "sent", state: "queued" }, entry: {} } };
      }
    }
    return undefined;
  });
}

export { refusal };
