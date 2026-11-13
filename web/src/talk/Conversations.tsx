import { useId, useRef, useState } from "react";

import type { Conversation } from "../api/talk";
import type { Live } from "../api/useView";
import { useArrivalFocus } from "../app/route";
import { conversationStates, laneStates, lookOf } from "../floor/states";
import { agoLabel, agoWords } from "../needs/wait";
import { Button } from "../ui/controls";
import { Banner, LookBadge, TargetChip } from "../ui/display";
import { useConversationList, useTargets } from "./data";
import { useTalk } from "./state";
import { Surface } from "./Surface";
import type { TalkKind } from "./target";

// How long ago a conversation was active, which a list reads at a glance.
function Active({ at, now }: { at: number; now: number }) {
  const age = Math.max(0, now - at);
  return (
    <span className="item-wait">
      <span aria-hidden="true">{agoLabel(age)}</span>
      <span className="visually-hidden">{agoWords(age)}</span>
    </span>
  );
}

type Props = { live: Live; project: string; label: string; after: () => Promise<void>; archived: boolean };

// ConversationsPage is the whole of the conversation surface: the member's conversations with the assistant and
// the sessions they can talk to, beside the surface itself. Choosing in the list is choosing the target the dock
// holds too, because they are one surface, and so it starts a conversation's session (FR-CHAT-11).
export function ConversationsPage({ live, project, label, after, archived }: Props) {
  const talk = useTalk();
  const { target } = talk;
  const title = useRef<HTMLHeadingElement>(null);
  const heading = useId();
  useArrivalFocus(title);
  const list = useConversationList(live, project, true);
  const targets = useTargets(live, project);
  const [detail, setDetail] = useState(false);
  const conversations = list.view?.conversations ?? [];
  const current = target.kind === "assistant" ? (conversations.find((row) => row.id === target.id) ?? conversations[0]) : undefined;
  const now = Date.now();

  const choose = (kind: TalkKind, id: string, name?: string) => {
    talk.select(kind, id, { name, start: kind === "assistant" });
    setDetail(true);
  };

  const sessions: { kind: TalkKind; id: string; main: string; note: string }[] = [
    ...(targets.view?.orchestrators ?? []).map((row) => ({
      kind: "orchestrator" as const,
      id: row.worker,
      main: row.name || row.worker,
      note: `${row.name || row.worker} · serves ${row.projects.join(", ") || label}${row.state === "running" ? "" : ` · ${row.state}`}`,
    })),
    ...(targets.view?.planning ?? []).map((row) => ({ kind: "planning" as const, id: row.lane, main: row.title || row.work_order, note: [row.work_order, row.lane, lookOf(laneStates, row.state).label.toLowerCase()].join(" · ") })),
    ...(targets.view?.lanes ?? []).map((row) => ({
      kind: "lane" as const,
      id: row.lane,
      main: row.title || row.work_order,
      note: [row.lane, row.work_order, row.run_mode === "headless" ? "headless" : "interactive"].join(" · "),
    })),
  ];
  const chosenSession = (kind: TalkKind, id: string) => target.kind === kind && target.id === id;

  return (
    <div className={`split split-convo${detail ? " show-detail" : ""}`}>
      <section className="split-list" aria-labelledby={heading}>
        <div className="split-list-head">
          <div className="row">
            <h1 id={heading} ref={title} tabIndex={-1}>
              Conversations
            </h1>
            <span className="spacer" />
            <Button small icon="plus" disabled={archived} onClick={() => {
                talk.select("assistant", "");
                talk.requestNew();
                setDetail(true);
              }}>
              New
            </Button>
          </div>
        </div>
        <div className="split-list-body">
          {list.failure ? (
            <div className="convo-banner">
              <Banner tone="danger">{list.failure}</Banner>
            </div>
          ) : null}
          <p className="overline convo-list-group" id={`${heading}-yours`}>
            Your conversations
          </p>
          {list.view && conversations.length === 0 ? (
            <p className="small muted convo-list-group">None yet. Start typing in the conversation, or choose New.</p>
          ) : (
            <ul aria-labelledby={`${heading}-yours`}>
              {conversations.map((row: Conversation) => (
                <li key={row.id}>
                  <button type="button" className="item convo-item" aria-current={current?.id === row.id && target.kind === "assistant" ? "true" : undefined} onClick={() => choose("assistant", row.id)}>
                    <span className="item-title">{row.name}</span>
                    <Active at={row.last_active_at} now={now} />
                    <span className="item-meta">
                      <LookBadge look={lookOf(conversationStates, row.actual_state)} plain />
                      {row.open_cards > 0 ? <span>{row.open_cards === 1 ? "1 open card" : `${row.open_cards} open cards`}</span> : null}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
          <p className="overline convo-list-group" id={`${heading}-sessions`}>
            Sessions you can talk to
          </p>
          {targets.view && sessions.length === 0 ? (
            <p className="small muted convo-list-group">No orchestrator, planning session, or lane runs for {label} now.</p>
          ) : (
            <ul aria-labelledby={`${heading}-sessions`}>
              {sessions.map((row) => (
                <li key={`${row.kind}:${row.id}`}>
                  <button type="button" className="item convo-item" aria-current={chosenSession(row.kind, row.id) ? "true" : undefined} onClick={() => choose(row.kind, row.id, row.kind === "orchestrator" ? row.main : undefined)}>
                    <span>
                      <TargetChip kind={row.kind} small />
                    </span>
                    <span className="item-meta">{row.note}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>
      <section className="split-detail convo-detail" aria-label="The conversation">
        <Button small icon="chevron-left" className="back-to-list" onClick={() => setDetail(false)}>
          All conversations
        </Button>
        <Surface live={live} variant="page" projectLabel={label} after={after} />
      </section>
    </div>
  );
}
