import { useEffect, useRef } from "react";
import type { ReactNode } from "react";

import type { StreamEvent, TalkEntry } from "../api/talk";
import { addressOf, Link } from "../app/route";
import { deliveryStates } from "../floor/states";
import { useClock } from "../parts/parts";
import { Button } from "../ui/controls";
import { Actor, Banner, EmptyState, RecordChip, SkeletonRows, StateBadge, TargetChip } from "../ui/display";
import { Icon } from "../ui/Icon";
import { clockOf, You } from "../ui/resolution";
import { addressOfCitation } from "./citations";
import type { RecordKind } from "./citations";
import { assentWords, buildRows, markerWords } from "./entries";
import type { Stream as StreamRead } from "./data";
import { InlineItem } from "./InlineItems";
import { MessageText } from "./MessageText";
import type { TalkKind } from "./target";

// The kinds of record an event's subject may be, by the name the ledger gives its kind.
const subjects: Record<string, RecordKind> = {
  work_order: "work_order",
  lane: "lane",
  plan: "plan",
  contract: "contract",
  requirement: "requirement",
  decision: "decision",
  question: "question",
  document: "document",
  repository: "repository",
  approval: "approval",
};

function Subject({ kind, id }: { kind: string; id: string }) {
  const known = subjects[kind];
  return known ? <RecordChip id={id} to={addressOfCitation({ kind: known, id })} label={`${kind.replace("_", " ")} ${id}`} /> : <RecordChip id={id} />;
}

// EventLine is something that happened, with who did it, in the audit's words: the act, the record it was
// about, and a link to the audit.
function EventLine({ event, at, you }: { event: StreamEvent; at: number; you: string }) {
  return (
    <div className="event-line">
      <Icon name="info" size="sm" />
      <span>
        <Actor kind={event.actor_kind} id={event.actor_id} actorFor={event.actor_for} label={event.actor_label} delegateKind={event.delegate_kind} you={you} /> <span className="mono">{event.type}</span>{" "}
        <Subject kind={event.subject_kind} id={event.subject_id} />
      </span>
      <span className="subtle">{clockOf(at)}</span>
      <Link to={addressOf("/audit", { kind: event.type })}>Audit</Link>
    </div>
  );
}

// ActionLine is an operation the assistant ran for the member, as the audit records it.
function ActionLine({ entry, member }: { entry: TalkEntry; member: string }) {
  const action = entry.action;
  if (!action) {
    return null;
  }
  const [kind = "", id = ""] = (action.subject ?? "").split(/:(.*)/);
  return (
    <div className="event-line">
      <Icon name="play" size="sm" />
      <span>
        <strong>{action.label}</strong> {id ? <Subject kind={kind} id={id} /> : null}
      </span>
      <span className="subtle">
        as the assistant for {member} · {clockOf(entry.at)}
      </span>
      <Link to={addressOf("/audit", { actor: "delegate" })}>Audit</Link>
    </div>
  );
}

type Props = {
  entries: TalkEntry[];
  read: StreamRead;
  label: string;
  // chip is the kind of target whose replies these are.
  chip: TalkKind;
  // member is the member's name, and you their ID, so their own messages read as "You".
  member: string;
  you: string;
  conversation?: { id: string; name: string };
  working: boolean;
  empty: { title: string; body: string };
  notice?: string;
  after: () => Promise<void>;
};

// Stream is a conversation's entries as a log: the member's messages on the right, the target's on the left
// with its chip and time, event lines for what happened, inline cards for what waits on the member, and a
// notice for each marker, with a divider before each day.
export function Stream({ entries, read, label, chip, member, you, conversation, working, empty, notice, after }: Props) {
  const now = useClock();
  const log = useRef<HTMLDivElement>(null);
  const stuck = useRef(true);
  const rows = buildRows(entries, now);

  useEffect(() => {
    const box = log.current;
    if (box && stuck.current) {
      box.scrollTop = box.scrollHeight;
    }
  }, [rows.length, working, read.ready]);

  const named = (entry: TalkEntry): string => (entry.from?.id === you ? "You" : (entry.from?.label ?? entry.from?.id ?? "Member"));

  let body: ReactNode;
  if (!read.ready && !read.failure) {
    body = <SkeletonRows rows={4} />;
  } else if (read.ready && rows.length === 0) {
    body = <EmptyState icon="chat" title={empty.title}>{empty.body}</EmptyState>;
  } else {
    body = rows.map((row) => {
      if (row.type === "divider") {
        return (
          <p key={row.key} className="notice">
            {row.label}
          </p>
        );
      }
      if (row.type === "assent") {
        return (
          <div key={row.key} className="event-line">
            <Icon name="info" size="sm" />
            <span>{assentWords}</span>
          </div>
        );
      }
      const { entry } = row;
      const before = row.beforeClear ? <span className="before-clear">before the clear</span> : null;
      switch (entry.kind) {
        case "member": {
          const own = entry.from?.kind === "member" || !entry.from;
          return (
            <div key={row.key} className={`msg-block${own ? " from-me" : ""}${row.beforeClear ? " is-before-clear" : ""}`}>
              <div className="msg-meta">
                {own ? <strong>{named(entry)}</strong> : <Actor kind={entry.from?.kind ?? "member"} id={entry.from?.id ?? ""} label={entry.from?.label} delegateKind={entry.from?.delegate_kind} actorFor={member} you={you} />}
                <span>{clockOf(entry.at)}</span>
                {entry.message ? <StateBadge table={deliveryStates} state={entry.message.state} /> : null}
                {before}
              </div>
              <div className="bubble bubble-plain">{entry.body}</div>
            </div>
          );
        }
        case "reply":
          return (
            <div key={row.key} className={`msg-block${row.beforeClear ? " is-before-clear" : ""}`}>
              <div className="msg-meta">
                <TargetChip kind={chip} small />
                <span>{clockOf(entry.at)}</span>
                {before}
              </div>
              <div className="bubble">
                <MessageText text={entry.body ?? ""} />
              </div>
            </div>
          );
        case "event":
          return entry.event ? <EventLine key={row.key} event={entry.event} at={entry.at} you={you} /> : null;
        case "action":
          return <ActionLine key={row.key} entry={entry} member={member} />;
        case "marker":
          return (
            <p key={row.key} className="notice">
              {markerWords(entry.marker ?? "")}
            </p>
          );
        case "card":
        case "request":
        case "question":
          return (
            <div key={row.key} className={`inline-item${row.beforeClear ? " is-before-clear" : ""}`}>
              <InlineItem entry={entry} member={member} conversation={conversation} after={after} />
            </div>
          );
      }
      return null;
    });
  }

  return (
    <You.Provider value={you}>
      {read.failure ? (
        <div className="convo-banner">
          <Banner tone="danger">{read.ready ? `What this conversation shows is not current. ${read.failure}` : read.failure}</Banner>
        </div>
      ) : null}
      <div
        className="convo-stream"
        role="log"
        aria-label={label}
        aria-live="off"
        tabIndex={0}
        ref={log}
        aria-busy={!read.ready}
        onScroll={(event) => {
          const box = event.currentTarget;
          stuck.current = box.scrollHeight - box.scrollTop - box.clientHeight < 48;
        }}
      >
        {notice ? (
          <p className="notice" role="status">
            {notice}
          </p>
        ) : null}
        {read.more ? (
          <div className="convo-older">
            <Button small loading={read.loadingOlder} onClick={() => void read.loadOlder()}>
              Show earlier messages
            </Button>
          </div>
        ) : null}
        {body}
        {working ? (
          <p className="notice working" role="status">
            The assistant is working…
          </p>
        ) : null}
      </div>
    </You.Provider>
  );
}
