import { useEffect, useId, useMemo, useRef, useState } from "react";
import type { KeyboardEvent, ReactNode } from "react";
import { createPortal } from "react-dom";

import { searchRecords } from "../api/client";
import type { SearchRecord } from "../api/types";
import { lookOf, workStates, laneStates } from "../floor/states";
import { recoverActions, specs } from "../floor/recoverSpecs";
import type { IconName } from "../ui/Icon";
import { Icon } from "../ui/Icon";
import { Kbd } from "../ui/controls";
import { LookBadge } from "../ui/display";
import { useReturnFocus } from "../ui/overlays";
import type { Entry, Sources } from "./ranking";
import { arrange, parseQuery } from "./ranking";
import { rememberRecord } from "./prefs";
import { go, inProject } from "./route";

// The kinds of record the palette opens, each with its icon, its name, and the view that shows it. A
// kind with no view in this build is not offered.
const recordKinds: Record<string, { icon: IconName; name: string; to: (id: string) => string }> = {
  work_order: { icon: "stack", name: "Work order", to: (id) => `/work-order?id=${encodeURIComponent(id)}` },
  lane: { icon: "terminal", name: "Lane", to: (id) => `/lane?id=${encodeURIComponent(id)}` },
  plan: { icon: "plan", name: "Plan", to: (id) => `/plans?plan=${encodeURIComponent(id)}` },
  requirement: { icon: "trace", name: "Requirement", to: (id) => `/documents?kind=requirements&key=${encodeURIComponent(id)}` },
  decision: { icon: "decision", name: "Decision", to: (id) => `/decisions?id=${encodeURIComponent(id)}` },
  question: { icon: "question", name: "Question", to: (id) => `/questions?id=${encodeURIComponent(id)}` },
  document: { icon: "doc", name: "Document", to: (id) => `/documents?id=${encodeURIComponent(id)}` },
  conversation: { icon: "chat", name: "Conversation", to: () => "/conversations" },
};

// stateOf says a record's state: a badge for the two state machines, and its own words otherwise.
function stateOf(record: SearchRecord): ReactNode {
  if (!record.state) {
    return null;
  }
  if (record.kind === "work_order" || record.kind === "lane") {
    return <LookBadge look={lookOf(record.kind === "lane" ? laneStates : workStates, record.state)} plain />;
  }
  return <span>{record.state.replaceAll("_", " ")}</span>;
}

// recordEntry makes a search result a line of the palette, which opens the record's view.
export function recordEntry(record: SearchRecord, project: string, projectLabel: string, onConversation?: (id: string) => void): Entry | null {
  const kind = recordKinds[record.kind];
  if (!kind) {
    return null;
  }
  // A conversation opens in the surface, where choosing it is how a member talks to it.
  const open = record.kind === "conversation" && onConversation ? () => onConversation(record.id) : undefined;
  return {
    key: `record:${record.kind}:${record.id}`,
    group: "Records",
    id: record.id,
    title: record.title || record.id,
    icon: kind.icon,
    to: inProject(project, kind.to(record.id)),
    run: open,
    meta: (
      <>
        <span>{kind.name}</span>
        {stateOf(record)}
        <span>{projectLabel}</span>
      </>
    ),
  };
}

// workEntries offers what a member can do to a work order the search found, as the server says its state allows:
// start it, or recover it. Each opens the work order's page on the dialog of that action, which says what it
// does before anything is sent. Nothing here reads the work order's state, because the server decides.
export function workEntries(record: SearchRecord, project: string, projectLabel: string): Entry[] {
  if (record.kind !== "work_order") {
    return [];
  }
  const name = record.title || record.id;
  const offer = (key: string, title: string, slug: string, icon: IconName, keywords: string): Entry => ({
    key: `${key}:${record.id}`,
    group: "Actions",
    id: record.id,
    title: `${title}: ${name}`,
    icon,
    keywords,
    to: inProject(project, `/work-order?id=${encodeURIComponent(record.id)}&dialog=${slug}`),
    meta: <span>{projectLabel}</span>,
  });
  const actions = record.actions ?? [];
  const entries: Entry[] = [];
  if (actions.includes("claim") || actions.includes("start_lane")) {
    entries.push(offer("start", actions.includes("claim") ? "Start work" : "Start lane", "start", "play", "claim run worker lane"));
  }
  for (const action of recoverActions) {
    if (actions.includes(action) && action !== "archive_lane") {
      entries.push(offer(`action:${specs[action].slug}`, specs[action].label, specs[action].slug, specs[action].icon, "recover work"));
    }
  }
  return entries;
}

type PaletteProps = {
  open: boolean;
  onClose: () => void;
  project: string;
  projectLabel: string;
  sources: Omit<Sources, "records">;
  // onConversation opens a conversation found among the records, in the conversation surface.
  onConversation?: (id: string) => void;
};

const searchWaitMs = 120;

// Palette finds any view, record, or action. The field is a combobox over a listbox: arrow keys move,
// Enter runs, Escape closes and returns focus. An action navigates or opens its dialog; the palette
// never decides anything.
export function Palette({ open, onClose, project, projectLabel, sources, onConversation }: PaletteProps) {
  const [typed, setTyped] = useState("");
  const [records, setRecords] = useState<Entry[]>([]);
  const [offered, setOffered] = useState<Entry[]>([]);
  const [failure, setFailure] = useState("");
  const [active, setActive] = useState(0);
  const field = useRef<HTMLInputElement>(null);
  const list = useId();
  useReturnFocus(open);

  useEffect(() => {
    if (open) {
      setTyped("");
      setActive(0);
      field.current?.focus();
    }
  }, [open]);

  const query = parseQuery(typed);
  const words = query.scope === "actions" ? "" : query.text;
  useEffect(() => {
    if (!open || !words || !project) {
      setRecords([]);
      setOffered([]);
      setFailure("");
      return;
    }
    let current = true;
    const timer = setTimeout(() => {
      searchRecords(words)
        .then((found) => {
          if (current) {
            setRecords(found.records.map((record) => recordEntry(record, project, projectLabel, onConversation)).filter((entry): entry is Entry => entry !== null));
            setOffered(found.records.flatMap((record) => workEntries(record, project, projectLabel)));
            setFailure("");
          }
        })
        .catch((error: unknown) => {
          if (current) {
            setRecords([]);
            setOffered([]);
            setFailure(error instanceof Error ? error.message : String(error));
          }
        });
    }, searchWaitMs);
    return () => {
      current = false;
      clearTimeout(timer);
    };
  }, [open, words, project, projectLabel, onConversation]);

  // An action found by a search stands beside the shell's, unless the page in view lists the same one.
  const groups = useMemo(() => {
    const listed = new Set(sources.actions.map((entry) => entry.title));
    return arrange(typed, { ...sources, actions: [...sources.actions, ...offered.filter((entry) => !listed.has(entry.title))], records });
  }, [typed, sources, records, offered]);
  const flat = groups.flatMap((group) => group.entries);
  const chosen = flat[Math.min(active, flat.length - 1)];

  useEffect(() => setActive(0), [typed]);

  if (!open) {
    return null;
  }

  const run = (entry: Entry | undefined) => {
    if (!entry) {
      return;
    }
    onClose();
    if (entry.group === "Records" && entry.to) {
      rememberRecord({ kind: entry.key.split(":")[1] ?? "", id: entry.id ?? "", title: entry.title, project, to: entry.to });
    }
    if (entry.run) {
      entry.run();
    } else if (entry.to) {
      go(entry.to);
    }
  };

  const keys = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      onClose();
    } else if (event.key === "ArrowDown") {
      event.preventDefault();
      setActive((at) => (flat.length ? (at + 1) % flat.length : 0));
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      setActive((at) => (flat.length ? (at - 1 + flat.length) % flat.length : 0));
    } else if (event.key === "Enter") {
      event.preventDefault();
      run(chosen);
    } else if (event.key === "Tab") {
      event.preventDefault();
    }
  };

  const optionId = (entry: Entry) => `${list}-${entry.key.replace(/[^\w-]/g, "_")}`;

  return createPortal(
    <div className="scrim scrim-palette" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
      <div className="palette" role="dialog" aria-modal="true" aria-label="Command palette" onKeyDown={keys}>
        <div className="palette-input">
          <Icon name="search" />
          <input
            ref={field}
            type="text"
            role="combobox"
            aria-label="Search views, records, and actions"
            aria-expanded="true"
            aria-controls={list}
            aria-autocomplete="list"
            aria-activedescendant={chosen ? optionId(chosen) : undefined}
            autoComplete="off"
            spellCheck={false}
            placeholder="Search or run a command…"
            value={typed}
            onChange={(event) => setTyped(event.target.value)}
          />
          <Kbd>Esc</Kbd>
        </div>
        <div className="palette-list" role="listbox" id={list} aria-label="Results">
          {groups.map((group) => (
            <div key={group.name} role="group" aria-labelledby={`${list}-${group.name}`}>
              <p className="palette-group overline" id={`${list}-${group.name}`} role="presentation">
                {group.name}
              </p>
              {group.entries.map((entry) => (
                <div
                  key={entry.key}
                  id={optionId(entry)}
                  role="option"
                  aria-selected={entry === chosen}
                  className="palette-item"
                  onMouseMove={() => setActive(flat.indexOf(entry))}
                  onClick={() => run(entry)}
                >
                  {entry.icon ? <Icon name={entry.icon} /> : null}
                  {entry.id && entry.group === "Records" ? <span className="id">{entry.id}</span> : null}
                  <span className="truncate">{entry.title}</span>
                  {entry.meta ? <span className="meta">{entry.meta}</span> : null}
                </div>
              ))}
            </div>
          ))}
          {flat.length === 0 ? <p className="palette-empty">{failure ? `Record search failed: ${failure}` : "Nothing matches. Try fewer words, or > for actions and # for records."}</p> : null}
        </div>
        <div className="palette-foot" aria-hidden="true">
          <span>
            <Kbd>↑</Kbd>
            <Kbd>↓</Kbd> move
          </span>
          <span>
            <Kbd>↵</Kbd> open
          </span>
          <span>
            <Kbd>&gt;</Kbd> actions only
          </span>
          <span>
            <Kbd>#</Kbd> records only
          </span>
        </div>
      </div>
    </div>,
    document.body,
  );
}
