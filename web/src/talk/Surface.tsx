import { useEffect, useId, useRef, useState } from "react";
import type { ReactNode } from "react";

import { useRead } from "../admin/parts";
import { readMembers } from "../api/admin";
import type { MemberList } from "../api/admin";
import { sendMessage } from "../api/talk";
import type { Conversation, TalkEntry } from "../api/talk";
import type { Live } from "../api/useView";
import { conversationStates, laneStates, lookOf } from "../floor/states";
import { LanePane } from "../lanes/LanePane";
import { Button, IconButton } from "../ui/controls";
import { Absent, Banner, LookBadge, TargetChip } from "../ui/display";
import { Icon } from "../ui/Icon";
import { Menu } from "../ui/overlays";
import { Composer } from "./Composer";
import { useConversationList, useLaneEnd, useStream, useTargets } from "./data";
import { InlineItem, openItems } from "./InlineItems";
import { useTalk } from "./state";
import { Stream } from "./Stream";
import { TargetMenu } from "./TargetMenu";
import { describeTarget } from "./target";
import { useAssistant } from "./useAssistant";

// How a conversation's session reads in the header: the member is told whether typing starts it.
const sessionWords: Record<string, string> = {
  starting: "Starting",
  running: "Ready",
  working: "Working",
  stopping: "Stopping",
  stopped: "Stopped · type to resume",
  failed: "Could not start",
};

const reasons: Record<string, string> = {
  member: "You stopped it.",
  another_conversation: "Another conversation started.",
  idle: "It sat idle.",
  signed_out: "You signed out.",
  deleted: "It was deleted.",
  no_worker: "No worker could run it.",
  could_not_start: "Its worker could not start it.",
  session_ended: "Its session ended.",
};

const minutes = (ms: number) => Math.max(1, Math.round(ms / 60_000));

// hasMessage says the member sent a message since the session last began, so the idle stop that holds is the
// longer one.
function hasMessage(entries: TalkEntry[]): boolean {
  let begun = -1;
  entries.forEach((entry, at) => {
    if (entry.kind === "marker" && (entry.marker === "started" || entry.marker === "cleared")) {
      begun = at;
    }
  });
  return entries.some((entry, at) => at > begun && entry.kind === "member");
}

function Facts({ items }: { items: ReactNode[] }) {
  return (
    <div className="convo-session">
      {items.map((item, at) => (
        <span key={at}>{item}</span>
      ))}
    </div>
  );
}

type Props = {
  live: Live;
  variant: "dock" | "page";
  onClose?: () => void;
  projectLabel: string;
  after: () => Promise<void>;
};

// Surface is the conversation: the target's header, its stream or its live pane, and the composer. It is the
// same surface in the dock on every view and in full on the Conversations view, and it reads only: opening it
// starts no session, and closing it stops none.
export function Surface({ live, variant, onClose, projectLabel, after }: Props) {
  const talk = useTalk();
  const { target } = talk;
  const heading = useId();
  const field = useRef<HTMLTextAreaElement>(null);
  const targets = useTargets(live, target.project);
  const isAssistant = target.kind === "assistant";
  const list = useConversationList(live, target.project, isAssistant);
  const conversations = list.view?.conversations ?? [];
  const conversation: Conversation | null = isAssistant ? (conversations.find((row) => row.id === target.id) ?? conversations[0] ?? null) : null;
  const stream = useStream(target, conversation?.id ?? "", live);
  const known = conversation?.member_id || targets.view?.assistant.conversation?.member_id || "";
  const members = useRead<MemberList | null>(() => (known ? Promise.resolve(null) : readMembers()), `talk:members:${target.project}:${known}`, live);
  const listReady = !isAssistant || list.view !== null;
  const ready = stream.ready && listReady;
  const lastMember = [...stream.entries].reverse().find((entry) => entry.kind === "member" && entry.from?.kind === "member");
  const only = members.view?.members.length === 1 && members.view.enrolled <= 1 ? (members.view.members[0]?.id ?? "") : "";
  const you = known || only;
  const youName = lastMember?.from?.label || you || "you";
  const lane = [...(targets.view?.planning ?? []), ...(targets.view?.lanes ?? [])].find((row) => row.lane === target.id);
  const gone = Boolean(targets.view) && (target.kind === "lane" || target.kind === "planning") && !lane;
  const laneEnd = useLaneEnd(live, target.project, gone ? target.id : "");
  const described = describeTarget(target, { projectLabel, you: youName, targets: targets.view, conversation, laneEnd });

  const refresh = () => {
    stream.again();
    list.again();
    targets.again();
  };
  const assistant = useAssistant({ conversation, workers: list.view?.workers ?? targets.view?.assistant.workers ?? [], refresh });

  // What a member sends a lane, a planning session, or an orchestrator is one instruction that grants it nothing.
  const [sending, setSending] = useState(false);
  const [refusal, setRefusal] = useState("");
  const targetKey = `${target.project}:${target.kind}:${target.id}`;
  useEffect(() => setRefusal(""), [targetKey]);
  const draft = talk.draftOf(target);
  const send = async () => {
    const body = draft.trim();
    if (!body) {
      return;
    }
    if (isAssistant) {
      if (await assistant.send(body)) {
        talk.setDraft(target, "");
      }
      return;
    }
    setSending(true);
    setRefusal("");
    try {
      await sendMessage(target.kind === "orchestrator" ? { kind: "orchestrator", worker: target.id } : { lane: target.id }, body);
      talk.setDraft(target, "");
      refresh();
    } catch (error) {
      setRefusal(error instanceof Error ? error.message : String(error));
    } finally {
      setSending(false);
    }
  };

  const state = conversation?.actual_state ?? "stopped";
  const asleep = isAssistant && (!conversation || state === "stopped" || state === "failed");

  // Something elsewhere asks for the composer, the switcher, or a new conversation.
  const [menuSignal, setMenuSignal] = useState(0);
  const newConversation = assistant.newConversation;
  useEffect(() => {
    if (talk.take("focus")) {
      const pane = document.querySelector<HTMLElement>(".convo .pane-input input");
      (field.current ?? pane)?.focus();
    }
  }, [talk.focusRequest, described.mode, ready]);
  useEffect(() => {
    if (talk.take("menu")) {
      setMenuSignal((was) => was + 1);
    }
  }, [talk.menuRequest]);
  useEffect(() => {
    if (talk.take("new")) {
      newConversation();
    }
  }, [talk.newRequest]);
  // Choosing a conversation starts its session, once the list says which one it is and that it sleeps.
  const resume = assistant.resume;
  useEffect(() => {
    if (listReady && conversation && asleep && talk.take("resume")) {
      resume();
    }
  }, [talk.resumeRequest, listReady, conversation?.id, asleep]);

  // The assistant starts a planning session at the member's request, and the member is switched to it.
  const seen = useRef<{ conversation: string; ids: Set<string> } | null>(null);
  useEffect(() => {
    if (!isAssistant || !conversation || !stream.ready) {
      return;
    }
    if (!seen.current || seen.current.conversation !== conversation.id) {
      seen.current = { conversation: conversation.id, ids: new Set(stream.entries.map((entry) => entry.id)) };
      return;
    }
    for (const entry of stream.entries) {
      if (seen.current.ids.has(entry.id)) {
        continue;
      }
      seen.current.ids.add(entry.id);
      const planning = entry.kind === "action" ? entry.action?.planning_lane : undefined;
      if (planning) {
        const order = (entry.action?.subject ?? "").split(/:(.*)/)[1] || "a work order";
        talk.select("planning", planning, { notice: `The assistant started a planning session on ${order} (lane ${planning}). You are now talking to it.` });
      }
    }
  }, [isAssistant, conversation?.id, stream.entries, stream.ready]);
  // A notice a switch left goes when the member leaves its target.
  useEffect(() => {
    const here = target;
    return () => talk.clearNotice(here);
  }, [targetKey]);

  const working = isAssistant && state === "working";
  const policy = targets.view?.assistant;
  const sessionFacts: ReactNode[] = [];
  if (isAssistant) {
    sessionFacts.push(<LookBadge look={{ ...lookOf(conversationStates, conversation ? state : "stopped"), label: sessionWords[conversation ? state : "stopped"] ?? state }} />);
    if (conversation?.worker_name || conversation?.worker_id) {
      sessionFacts.push(`on ${conversation.worker_name || conversation.worker_id}`);
    }
    if (conversation && conversation.actual_state !== "stopped") {
      sessionFacts.push(`generation ${conversation.current_generation}`);
      sessionFacts.push(
        <>
          context <Absent>unavailable</Absent>
        </>,
      );
    }
    if (policy && conversation && (state === "running" || state === "working")) {
      sessionFacts.push(
        hasMessage(stream.entries)
          ? `stops after ${minutes(policy.idle_stop_ms)} min idle`
          : `stops after ${minutes(policy.warm_idle_stop_ms)} min if no message is sent`,
      );
    }
    if (state === "failed" && conversation?.stopped_reason) {
      sessionFacts.push(reasons[conversation.stopped_reason] ?? conversation.stopped_reason);
    }
  } else if (target.kind === "orchestrator" && described.orchestrator) {
    sessionFacts.push(<LookBadge look={lookOf({ running: { label: "Running", tone: "active", glyph: "dot" }, stopped: { label: "Stopped", tone: "neutral", glyph: "pause" } }, described.orchestrator.state)} />);
    sessionFacts.push(`on ${described.orchestrator.name || described.orchestrator.worker}`);
    sessionFacts.push(`generation ${described.orchestrator.generation}`);
  } else if (lane) {
    sessionFacts.push(<LookBadge look={lookOf(laneStates, lane.state)} />);
    sessionFacts.push(`on ${lane.worker_name || lane.worker || "no machine yet"}`);
    sessionFacts.push(`generation ${lane.generation}`);
    sessionFacts.push(
      <>
        context <Absent>unavailable</Absent>
      </>,
    );
  }

  const usable = (list.view?.workers ?? []).filter((worker) => worker.available);
  const noWorker = isAssistant && list.view !== null && usable.length === 0;
  const unavailable = (list.view?.workers ?? []).filter((worker) => !worker.available);

  const items: { label: string; onSelect: () => void; disabled?: boolean; disabledWhy?: string }[] = [
    { label: "Compact context", onSelect: assistant.compact, disabled: !conversation || !(state === "running" || state === "working"), disabledWhy: "The session is not running. Type to resume it." },
    { label: "Clear context", onSelect: assistant.clear, disabled: !conversation || !(state === "running" || state === "working"), disabledWhy: "The session is not running. Type to resume it." },
    { label: "Rename…", onSelect: assistant.askRename, disabled: !conversation, disabledWhy: "There is no conversation yet." },
    { label: "Stop session", onSelect: assistant.stop, disabled: !conversation || asleep || state === "stopping", disabledWhy: "The session is not running." },
    { label: "Delete conversation…", onSelect: assistant.askDelete, disabled: !conversation, disabledWhy: "There is no conversation yet." },
  ];

  const pane = described.mode === "pane" && !described.ended;
  const strip = pane ? openItems(stream.entries) : [];
  const notice = talk.noticeOf(target);
  const itemMember = youName;
  // A decision made inline resolves the same record in Needs you, so both read again.
  const decided = async () => {
    await after();
    stream.again();
  };

  return (
    <section className={`convo t-${described.kind}`} aria-labelledby={heading} data-surface={variant}>
      <div className="convo-head">
        <div className="convo-head-top">
          <TargetChip kind={described.kind} />
          <h2 id={heading} className="truncate convo-name">
            {described.name}
          </h2>
          <span className="spacer" />
          <TargetMenu
            targets={targets.view}
            current={target}
            assistant={conversation ?? targets.view?.assistant.conversation ?? null}
            openSignal={menuSignal}
            onChoose={(kind, id, name) => talk.select(kind, id, { name, start: kind === "assistant" })}
            onNew={assistant.newConversation}
          />
          {isAssistant ? <Menu label="Conversation actions" items={items} /> : null}
          {variant === "dock" ? <IconButton icon="x" label="Close the conversation panel" shortcut="." small onClick={onClose} /> : null}
        </div>
        <p className="convo-which">{described.which}</p>
        <p className="convo-can">{described.can}</p>
        {sessionFacts.length > 0 ? (
          <div className="convo-session-row">
            <Facts items={sessionFacts} />
            {isAssistant && conversation && asleep ? (
              <Button small onClick={assistant.resume} loading={assistant.busy}>
                Resume
              </Button>
            ) : null}
          </div>
        ) : null}
      </div>

      {noWorker ? (
        <div className="convo-banner">
          <Banner tone="warning">
            No worker can run the assistant now.
            {unavailable.length > 0 ? ` ${unavailable.map((worker) => `${worker.name} ${worker.reason ?? "is unavailable"}`).join("; ")}.` : " No worker is registered."} A conversation stays readable, and
            starts again when a worker can run it.
          </Banner>
        </div>
      ) : null}

      {pane ? (
        <div className="convo-term">
          {notice ? (
            <p className="notice convo-notice" role="status">
              {notice}
            </p>
          ) : null}
          {strip.length > 0 ? (
            <div className="convo-strip" role="region" aria-label="Waiting on you in this session">
              {strip.map((entry) => (
                <InlineItem key={entry.id} entry={entry} member={itemMember} after={decided} />
              ))}
            </div>
          ) : null}
          <div className={`pane-strip t-${described.kind}`}>
            <Icon name="attach" size="sm" />
            <span>This session is interactive: type in the pane.</span>
          </div>
          <LanePane lane={target.id} live={live} attached state={lane?.state} />
        </div>
      ) : (
        <>
          <Stream
            entries={stream.entries}
            read={{ ...stream, ready }}
            label={described.streamLabel}
            chip={described.kind}
            member={itemMember}
            you={you}
            conversation={conversation ? { id: conversation.id, name: conversation.name } : undefined}
            working={working}
            notice={notice}
            empty={
              isAssistant
                ? { title: "Ask the assistant", body: "Start typing and the assistant starts with you. It acts for you through the API: operational actions run at once, and decisions become cards you confirm." }
                : { title: "Nothing here yet", body: "What this session does, and what you send it, appears here." }
            }
            after={decided}
          />
          <Composer
            described={described}
            draft={draft}
            onDraft={(text) => talk.setDraft(target, text)}
            onSend={() => void send()}
            busy={sending || assistant.busy}
            failure={isAssistant ? assistant.failure : refusal}
            field={field}
            onKeystroke={isAssistant ? assistant.warm : undefined}
          />
        </>
      )}
      {assistant.dialogs}
    </section>
  );
}

