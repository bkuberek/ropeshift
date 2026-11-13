import { useEffect, useId } from "react";
import type { ReactNode } from "react";
import { createPortal } from "react-dom";

import type { ConversationTargets } from "../api/talk";
import { TargetChip } from "../ui/display";
import { Icon } from "../ui/Icon";
import { moveInMenu, usePopover } from "../ui/overlays";
import type { Target, TalkKind } from "./target";

type Props = {
  targets: ConversationTargets | null;
  current: Target;
  // assistant is the conversation choosing the assistant row opens: the one the member is in, or else the newest.
  // With none, the row says so, and choosing it only switches the target.
  assistant: { id: string; name: string } | null;
  onChoose: (kind: TalkKind, id: string, name?: string) => void;
  onNew: () => void;
  // openSignal opens the menu each time it changes, which the T key does.
  openSignal: number;
};

type Row = { kind: TalkKind; id: string; main: string; note?: string; name?: string };

// TargetMenu is Switch target: the assistant, an orchestrator, a planning session, and a lane, each row with
// its target chip and which one it is, the checked one being what the member talks to. A target that is not
// running shows its state. Choosing is the only thing that switches the target.
export function TargetMenu({ targets, current, assistant, onChoose, onNew, openSignal }: Props) {
  const { open, setOpen, close, anchor, popover } = usePopover("end");
  const menu = useId();
  useEffect(() => {
    if (openSignal > 0) {
      setOpen(true);
    }
  }, [openSignal, setOpen]);
  useEffect(() => {
    if (open) {
      (popover.current?.querySelector<HTMLElement>('[aria-checked="true"]') ?? popover.current?.querySelector<HTMLElement>('[role="menuitemradio"]'))?.focus();
    }
  }, [open, popover]);

  const groups: { kind: TalkKind; title: string; rows: Row[] }[] = [
    { kind: "assistant", title: "Assistant", rows: [{ kind: "assistant", id: assistant?.id ?? "", main: assistant?.name ?? "No conversation yet" }] },
    {
      kind: "orchestrator",
      title: "Orchestrator",
      rows: (targets?.orchestrators ?? []).map((row) => ({ kind: "orchestrator", id: row.worker, main: row.name || row.worker, name: row.name, note: row.state === "running" ? undefined : row.state })),
    },
    {
      kind: "planning",
      title: "Planning session",
      rows: (targets?.planning ?? []).map((row) => ({ kind: "planning", id: row.lane, main: [row.work_order, row.lane].join(" · "), note: row.state === "working" ? undefined : row.state })),
    },
    {
      kind: "lane",
      title: "Lane",
      rows: (targets?.lanes ?? []).map((row) => ({ kind: "lane", id: row.lane, main: [row.lane, row.work_order].join(" · "), note: row.state === "working" ? undefined : row.state })),
    },
  ];

  const body: ReactNode = (
    <div className="popover target-menu" role="menu" id={menu} aria-label="Switch conversation target" ref={popover} onKeyDown={(event) => moveInMenu(event, popover.current, close)}>
      {groups.map((group) =>
        group.rows.length === 0 ? null : (
          <div key={group.kind} role="group" aria-labelledby={`${menu}-${group.kind}`}>
            <p className="overline menu-label" id={`${menu}-${group.kind}`} role="presentation">
              {group.title}
            </p>
            {group.rows.map((row) => {
              const checked = current.kind === row.kind && (row.kind === "assistant" || current.id === row.id);
              return (
                <button
                  key={`${row.kind}:${row.id}`}
                  type="button"
                  role="menuitemradio"
                  aria-checked={checked}
                  className="menu-item"
                  tabIndex={-1}
                  onClick={() => {
                    close();
                    onChoose(row.kind, row.id, row.name);
                  }}
                >
                  <TargetChip kind={row.kind} small />
                  <span className="truncate">{row.main}</span>
                  {row.note ? <span className="meta">{row.note}</span> : null}
                </button>
              );
            })}
          </div>
        ),
      )}
      <div className="menu-sep" role="separator" />
      <button
        type="button"
        role="menuitem"
        className="menu-item"
        tabIndex={-1}
        onClick={() => {
          close(false);
          onNew();
        }}
      >
        <Icon name="plus" />
        New assistant conversation
      </button>
    </div>
  );
  return (
    <>
      <button
        ref={anchor}
        type="button"
        className="btn btn-sm"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menu : undefined}
        aria-keyshortcuts="T"
        title="Switch target (T)"
        onClick={() => setOpen(!open)}
      >
        Switch target
        <Icon name="chevron-down" size="sm" />
      </button>
      {open ? createPortal(body, document.body) : null}
    </>
  );
}
