import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { FormEvent, KeyboardEvent, RefObject } from "react";

import { ApiError, followPane, maxPaneScrollback, paneRefusal, readPaneScrollback } from "../api/client";
import { readView } from "../api/client";
import type { PaneLane, PaneScreen, ShopFloor } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { onMac } from "../app/keys";
import { usePageActions } from "../app/pageActions";
import { addressOf, Link } from "../app/route";
import { WaitsOnWords } from "../floor/WaitsOn";
import { SpendPanel } from "../floor/SpendPanel";
import { laneLabel } from "../floor/states";
import { Ago, Page, useClock, ViewTime, words } from "../parts/parts";
import { Absent, Badge, CheckingChip, ConditionChip, EmptyState, Fact, Facts, Freshness, freshnessOf, Id, LaneBadge, Panel, TableRegion, TargetChip } from "../ui/display";
import type { TargetKind } from "../ui/display";
import { AskedQuestions } from "../planning/AskedQuestions";
import { useTalk } from "../talk/state";
import { Button } from "../ui/controls";
import { chordOf, isRelease, releaseKey } from "./chords";
import { KeyCaps, KeyComposer } from "./KeyComposer";
import { LaneMessages } from "./Messages";
import { findIn, markRuns } from "./find";
import { copyText, FindBar, PaneTools, selectContents } from "./PaneTools";
import { parseScreen, trimScreen } from "./screen";
import { PaneSender } from "./sender";
import { startingMs } from "./starting";

// retryMs is how long the page waits before it opens a pane's stream again: after the stream closed
// with nothing to say, or on a lane whose pane has not opened yet.
const retryMs = 3000;

// The refusals that can pass, so the page asks again.
const passing = new Set(["no_pane", "unreachable", "unavailable", "session_required"]);

// A lane that was just started answers not_found until it reaches the ledger. The page asks again after each
// wait in turn, repeating the last, until startingMs after the start, and then says the lane has not started.
const startingWaitsMs = [500, 1000, 2000, retryMs];

function neverStarted(lane: string): string {
  return `Lane ${lane} has not started: its worker has not reported it in ${startingMs / 60_000} minutes. The Floor shows whether the worker is connected. Reload this page to look again.`;
}

// The keys an empty field sends to the session, so a person can answer a menu without leaving it.
const fieldKeys: Record<string, string> = { Enter: "Enter", Escape: "Escape", ArrowUp: "Up", ArrowDown: "Down" };

// The keys the key row holds: the label it shows, its name where the label is a glyph, and the key sequence
// it sends. Keys... sends any other combination or sequence.
const rowKeys: { label: string; name?: string; keys: string }[] = [
  { label: "Esc", keys: "Escape" },
  { label: "Tab", keys: "Tab" },
  { label: "Shift-Tab", keys: "Shift-Tab" },
  { label: "Enter", keys: "Enter" },
  { label: "Ctrl-C", keys: "Ctrl-C" },
  { label: "Ctrl-O", keys: "Ctrl-O" },
  { label: "↑", name: "Up", keys: "Up" },
  { label: "↓", name: "Down", keys: "Down" },
  { label: "←", name: "Left", keys: "Left" },
  { label: "→", name: "Right", keys: "Right" },
];

type Shown =
  | { kind: "opening" }
  | { kind: "starting" }
  | { kind: "screen"; generation: number; screen: string }
  | { kind: "away"; generation: number; state: string; screen: string }
  | { kind: "refused"; message: string; screen: string };

// usePane follows a lane's pane. The server follows the lane from one generation's pane to the next, so
// the page holds one stream for the life of the page. A lane started at startedAt is one the ledger may not
// hold yet, so its not_found passes until startingMs after it, on a page that opened within that time; any
// other lane's not_found, and one a page reached again long after the start, is refused at once.
function usePane(lane: string, live: Live, startedAt: number): Shown {
  const [shown, setShown] = useState<Shown>({ kind: "opening" });
  const session = useRef(live.checkSession);
  session.current = live.checkSession;
  const started = useRef(startedAt);
  started.current = startedAt;

  useEffect(() => {
    let stream: EventSource | null = null;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let gone = false;
    let last = "";
    let waits = 0;
    const start = started.current;
    const recent = start > 0 && Date.now() - start < startingMs;
    const explain = async () => {
      const refused = await paneRefusal(lane);
      if (gone) {
        return;
      }
      const missing = refused?.code === "not_found" && recent && started.current > 0;
      if (missing && Date.now() - start < startingMs) {
        setShown({ kind: "starting" });
        retry = setTimeout(open, startingWaitsMs[Math.min(waits++, startingWaitsMs.length - 1)]);
        return;
      }
      if (refused) {
        setShown({ kind: "refused", message: missing ? neverStarted(lane) : refused.message, screen: last });
        if (refused.code === "session_required") {
          await session.current();
        }
      }
      if (!gone && (!refused || passing.has(refused.code))) {
        retry = setTimeout(open, retryMs);
      }
    };
    const open = () => {
      const opened = followPane(lane);
      stream = opened;
      opened.addEventListener("screen", (event) => {
        const snapshot = JSON.parse((event as MessageEvent<string>).data) as PaneScreen;
        last = snapshot.screen;
        setShown({ kind: "screen", generation: snapshot.generation, screen: snapshot.screen });
      });
      opened.addEventListener("lane", (event) => {
        const state = JSON.parse((event as MessageEvent<string>).data) as PaneLane;
        setShown({ kind: "away", generation: state.generation, state: state.state, screen: last });
        if (state.state === "ended") {
          opened.close();
        }
      });
      // A stream that broke opens again on its own. One the server refused is closed for good, and only
      // a read of the refusal says why.
      opened.onerror = () => {
        if (opened.readyState === EventSource.CLOSED) {
          opened.close();
          void explain();
        }
      };
    };
    open();
    return () => {
      gone = true;
      clearTimeout(retry);
      stream?.close();
    };
  }, [lane]);

  return shown;
}

function statusOf(shown: Shown): string {
  switch (shown.kind) {
    case "opening":
      return "Opening the pane.";
    case "starting":
      return "The lane is starting. Its pane shows here once its worker reports it.";
    case "screen":
      return `Live, generation ${shown.generation}.`;
    case "refused":
      return "The pane is not open.";
    case "away":
      if (shown.state === "parked") {
        return "The lane is parked, so it has no process. The pane comes back when it resumes.";
      }
      if (shown.state === "ended") {
        return "The lane has ended.";
      }
      return `The lane has no pane right now (${laneLabel(shown.state).toLowerCase()}, generation ${shown.generation}). It shows here when one opens.`;
  }
}

// How many lines of the scrollback a pane reads first, how near the top of the screen a scroll reads older
// lines, and how often the scrollback follows output while the member reads at the bottom.
const firstScrollback = 200;
const topSlack = 8;
const followScrollbackMs = 1000;

type History = { lines: string; size: number; limit: number; asked: number };

// resetStyle ends any colour or emphasis the scrollback left open, so none of it reaches the screen.
const resetStyle = "\x1b[0m";

const plainOf = (text: string) =>
  parseScreen(text)
    .map((run) => run.text)
    .join("");

const countLines = (lines: string) => (lines === "" ? 0 : lines.split("\n").length - 1);

const number = (n: number) => n.toLocaleString("en-US");

// ScreenKeys is how a screen that holds the keyboard sends each key, or null while the pane takes no keys:
// the person is detached, or the lane has no live pane.
type ScreenKeys = { send: (chord: string) => void } | null;

// Sends is what the pane's parts send through: keys, and lines of text.
type Sends = Pick<PaneSender, "keys" | "text">;

const plainKey = (event: KeyboardEvent) => !event.ctrlKey && !event.altKey && !event.metaKey && !event.shiftKey;

// bottomSlack is how near the bottom, in pixels, a screen counts as showing the newest line.
const bottomSlack = 4;

// The least height a filled screen takes, and the gap it leaves under the controls below it.
const fillLeast = 240;
const fillGap = 16;

// fitScreen sizes a screen so the pane, with the controls under the screen, fills what the page's scroll
// area shows below the screen's top, as the screen sits with the page scrolled to its top.
function fitScreen(pre: HTMLElement, pane: HTMLElement): void {
  const scroller = pre.closest<HTMLElement>(".main") ?? document.documentElement;
  const shown = scroller === document.documentElement ? 0 : scroller.getBoundingClientRect().top;
  const top = pre.getBoundingClientRect().top - shown + scroller.scrollTop;
  const below = pane.getBoundingClientRect().bottom - pre.getBoundingClientRect().bottom;
  pre.style.height = `${Math.max(fillLeast, Math.round(scroller.clientHeight - top - below - fillGap))}px`;
}

// Screen is the pane's screen, with the toolbar above it. A click on the screen, or Enter while it has
// focus, captures the keys: every key then goes to the session, Tab, Shift-Tab, Escape, and Ctrl
// combinations included, and single-key shortcuts are off. The release key, a click outside the pane, or
// Release gives the keys back and leaves focus on the screen, so Tab moves on from there (WCAG 2.1.2,
// https://www.w3.org/TR/WCAG21/#no-keyboard-trap). Command shortcuts on a Mac stay with the browser and the
// page. Opening a page never captures keys.
//
// The screen reads the newest lines of the tmux scrollback when it opens, and shows the newest line; scrolling
// up to the top reads older lines, up to all that tmux keeps, and keeps the member's place. While the member
// reads at the bottom, the scrollback follows the lines that scroll off the screen. Copy all copies the whole
// scrollback and the screen.
//
// The screen opens at the newest line and stays there as output arrives, until the member scrolls up; it
// then stays where they are and offers Jump to latest, and scrolling back to the bottom follows the newest
// line again. Select all, and the select-all shortcut while the screen has focus, select the pane's text
// and nothing else on the page; Search, and the find shortcut while the screen has focus, search it. While
// the screen holds the keys those shortcuts, written with Ctrl, go to the session, and the Command ones on
// a Mac stay with the page. A filled screen takes the height the page shows under it, and Full view gives
// the pane the whole view.
function Screen({
  lane,
  text,
  keys,
  fill,
  full,
  onFull,
  pane,
}: {
  lane: string;
  text: string;
  keys: ScreenKeys;
  fill: boolean;
  full: boolean;
  onFull: () => void;
  pane: RefObject<HTMLElement | null>;
}) {
  const screen = useRef<HTMLPreElement>(null);
  const [captured, setCaptured] = useState(false);
  const following = useRef(true);
  const [away, setAway] = useState(false);
  const [said, setSaid] = useState("");
  const [searching, setSearching] = useState(false);
  const [query, setQuery] = useState("");
  const [matchCase, setMatchCase] = useState(false);
  const [current, setCurrent] = useState(0);
  const [history, setHistory] = useState<History | null>(null);
  const [unread, setUnread] = useState("");
  const runs = parseScreen(history?.lines ? history.lines + resetStyle + trimScreen(text) : trimScreen(text));
  const plain = runs.map((run) => run.text).join("");
  const starts = searching ? findIn(plain, query, matchCase) : [];
  const at = starts.length === 0 ? 0 : Math.min(current, starts.length - 1);
  useEffect(() => {
    if (!keys) {
      setCaptured(false);
    }
  }, [keys]);
  const holding = captured && keys !== null;
  useEffect(() => {
    screen.current?.querySelector(".pane-match.is-current")?.scrollIntoView?.({ block: "nearest" });
  }, [at, query, matchCase, searching]);

  const toLatest = () => {
    const pre = screen.current;
    if (pre) {
      pre.scrollTop = pre.scrollHeight - pre.clientHeight;
    }
  };
  useLayoutEffect(() => {
    const pre = screen.current;
    const frame = pane.current;
    if (!pre || !frame) {
      return;
    }
    const fit = () => {
      if (fill && !full) {
        fitScreen(pre, frame);
      } else {
        pre.style.height = "";
      }
      if (following.current) {
        toLatest();
      }
    };
    fit();
    // The page's scroll area and the pane resize the screen: the view, and the controls under the screen.
    if (typeof ResizeObserver === "undefined") {
      window.addEventListener("resize", fit);
      return () => window.removeEventListener("resize", fit);
    }
    const sizes = new ResizeObserver(fit);
    sizes.observe(pre.closest<HTMLElement>(".main") ?? document.documentElement);
    sizes.observe(frame);
    return () => sizes.disconnect();
  }, [fill, full, pane]);
  useLayoutEffect(() => {
    if (following.current) {
      toLatest();
    }
  }, [text]);
  // The scrollback follows the screen while the member reads at the bottom, and a scroll to the top reads
  // older lines.
  const scrolled = () => {
    const pre = screen.current;
    if (!pre) {
      return;
    }
    following.current = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - bottomSlack;
    setAway(!following.current);
    if (history && pre.scrollTop <= topSlack && countLines(history.lines) < history.size && history.asked < history.limit) {
      read(Math.min(history.asked * 2, history.limit, maxPaneScrollback), "place");
    }
  };
  // Jump to latest goes once it has jumped, so it leaves focus on the screen.
  const jump = () => {
    following.current = true;
    setAway(false);
    toLatest();
    screen.current?.focus();
  };

  const reading = useRef(false);
  const place = useRef<"bottom" | { fromBottom: number } | null>(null);
  const followTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const read = (asked: number, keep: "bottom" | "place") => {
    if (reading.current) {
      return;
    }
    reading.current = true;
    readPaneScrollback(lane, asked)
      .then((got) => {
        const pre = screen.current;
        place.current = (keep === "bottom" && following.current) || !pre ? "bottom" : { fromBottom: pre.scrollHeight - pre.scrollTop };
        setUnread("");
        setHistory({ lines: got.lines, size: got.size, limit: got.limit, asked });
      })
      .catch((error: unknown) => setUnread(error instanceof ApiError ? error.message : String(error)))
      .finally(() => {
        reading.current = false;
      });
  };
  useEffect(() => {
    read(firstScrollback, "bottom");
    return () => clearTimeout(followTimer.current);
    // A pane reads its scrollback once when it opens, and the reads after that follow the member.
  }, [lane]);
  useLayoutEffect(() => {
    const pre = screen.current;
    const at = place.current;
    place.current = null;
    if (pre && at) {
      pre.scrollTop = at === "bottom" ? pre.scrollHeight - pre.clientHeight : pre.scrollHeight - at.fromBottom;
    }
  }, [history]);
  useEffect(() => {
    if (!history || followTimer.current || !following.current) {
      return;
    }
    followTimer.current = setTimeout(() => {
      followTimer.current = undefined;
      if (following.current) {
        read(history.asked, "bottom");
      }
    }, followScrollbackMs);
    // Each change of the screen may scroll lines into the scrollback.
  }, [text]);

  const copyAll = () => {
    readPaneScrollback(lane, Math.min(history?.limit ?? maxPaneScrollback, maxPaneScrollback))
      .then((got) => copyText(plainOf(got.lines) + plainOf(trimScreen(text)), "the scrollback and the screen"))
      .then(setSaid, (error: unknown) => setSaid(error instanceof ApiError ? error.message : String(error)));
  };

  const release = () => {
    setCaptured(false);
    screen.current?.focus();
  };

  const selectAll = () => {
    screen.current?.focus();
    selectContents(screen.current);
  };
  const closeSearch = () => {
    setSearching(false);
    screen.current?.focus();
  };
  const move = (by: number) => setCurrent(starts.length === 0 ? 0 : (at + by + starts.length) % starts.length);
  // A click that ends a selection leaves the keys with the page, so the selection can be copied.
  const click = () => {
    const selection = window.getSelection();
    if (keys && (!selection || selection.isCollapsed)) {
      setCaptured(true);
    }
  };
  // pageShortcut is select all and find, which the page keeps for the screen while the keys are not held,
  // and, on a Mac, while they are.
  const pageShortcut = (event: KeyboardEvent<HTMLPreElement>) => {
    const mod = onMac() ? event.metaKey : event.ctrlKey;
    const key = event.key.toLowerCase();
    if (!mod || event.altKey || event.shiftKey) {
      return;
    }
    if (key === "a") {
      event.preventDefault();
      selectContents(screen.current);
    } else if (key === "f") {
      event.preventDefault();
      setSearching(true);
    }
  };
  const keyDown = (event: KeyboardEvent<HTMLPreElement>) => {
    if (keys && holding) {
      if (isRelease(event)) {
        event.preventDefault();
        event.stopPropagation();
        setCaptured(false);
        return;
      }
      const chord = chordOf(event);
      if (chord !== null) {
        event.preventDefault();
        event.stopPropagation();
        keys.send(chord);
        return;
      }
    } else if (keys && event.key === "Enter" && plainKey(event)) {
      event.preventDefault();
      setCaptured(true);
      return;
    }
    pageShortcut(event);
  };

  return (
    <div className={holding ? "pane-frame is-captured" : "pane-frame"}>
      <PaneTools
        onCopyScreen={() => void copyText(plainOf(trimScreen(text)), "the screen").then(setSaid)}
        onSelectAll={selectAll}
        onSearch={() => (searching ? closeSearch() : setSearching(true))}
        searching={searching}
        full={full}
        onFull={onFull}
        said={said}
      />
      {searching ? (
        <FindBar
          words={query}
          matchCase={matchCase}
          current={at}
          count={starts.length}
          onWords={(next) => {
            setQuery(next);
            setCurrent(0);
          }}
          onMatchCase={(next) => {
            setMatchCase(next);
            setCurrent(0);
          }}
          onMove={move}
          onClose={closeSearch}
        />
      ) : null}
      <div className="pane-history row-tight">
        <span className="small muted">
          {unread
            ? `The scrollback could not be read: ${unread}`
            : history
              ? `Showing ${number(countLines(history.lines))} of the ${number(history.limit)} lines tmux keeps`
              : "Reading the scrollback."}
        </span>
        <button className="btn btn-sm" type="button" onClick={copyAll}>
          Copy all
        </button>
      </div>
      {holding ? (
        <div className="pane-capture">
          <span>
            Keys go to the session. <KeyCaps sequence={releaseKey} /> releases.
          </span>
          <span className="spacer" />
          <button className="btn btn-sm" type="button" onMouseDown={(event) => event.preventDefault()} onClick={release}>
            Release
          </button>
        </div>
      ) : null}
      {/* The whole screen changes at once, so a screen reader reads it when asked and the status line says
          what changed. While it holds the keys it is an application, so a screen reader passes them on, and
          its name says how to leave. */}
      <pre
        ref={screen}
        className={fill && !full ? "pane-screen is-filled" : "pane-screen"}
        role={holding ? "application" : "log"}
        aria-label={holding ? `Screen of lane ${lane}. Keys go to the session; ${releaseKey} releases them.` : `Screen of lane ${lane}`}
        aria-live="off"
        tabIndex={0}
        title={keys ? `Click, or press Enter, to send keys to the session. ${releaseKey} releases them.` : undefined}
        onClick={click}
        onKeyDown={keyDown}
        onScroll={scrolled}
        onBlur={() => setCaptured(false)}
      >
        {markRuns(runs, starts, query.length).map((run, place) =>
          run.match === null ? (
            <span key={place} className={run.className || undefined}>
              {run.text}
            </span>
          ) : (
            <mark key={place} className={run.match === at ? "pane-match is-current" : "pane-match"}>
              <span className={run.className || undefined}>{run.text}</span>
            </mark>
          ),
        )}
      </pre>
      {away ? (
        <button className="btn btn-sm pane-latest" type="button" onClick={jump}>
          Jump to latest
        </button>
      ) : null}
    </div>
  );
}

// Typing is the key row, the composer it opens, and the line field. Everything goes through the pane's one
// sender, so no key overtakes the text before it.
function Typing({ lane, live, sender }: { lane: string; live: boolean; sender: Sends }) {
  const field = useRef<HTMLInputElement>(null);
  const keysButton = useRef<HTMLButtonElement>(null);
  const hint = useId();
  const composer = useId();
  const [text, setText] = useState("");
  const [enter, setEnter] = useState(true);
  const [composing, setComposing] = useState(false);
  // The command palette puts the keyboard in the field, and sends nothing.
  usePageActions(live ? [{ key: `action:pane-keys:${lane}`, group: "Actions", id: lane, title: `Send keys to the pane of lane ${lane}`, icon: "terminal", keywords: "type input session", run: () => field.current?.focus() }] : []);

  const press = (keys: string) => {
    if (live) {
      sender.keys(...keys.split(" "));
    }
    field.current?.focus();
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!live || text === "") {
      return;
    }
    const line = text;
    setText("");
    sender.text(line, enter);
    field.current?.focus();
  };
  const keyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    const key = fieldKeys[event.key];
    if (key && text === "" && plainKey(event)) {
      event.preventDefault();
      press(key);
    }
  };

  return (
    <div className="pane-input">
      <div className="row-tight pane-keys" role="group" aria-label="Press a key">
        {rowKeys.map((key) => (
          <button
            key={key.keys}
            className="btn btn-sm"
            type="button"
            aria-label={key.name}
            disabled={!live}
            // A press with the pointer leaves the keyboard in the field.
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => press(key.keys)}
          >
            {key.label}
          </button>
        ))}
        <button ref={keysButton} className="btn btn-sm" type="button" aria-expanded={composing} aria-controls={composer} onClick={() => setComposing((was) => !was)}>
          Keys…
        </button>
      </div>
      <div id={composer}>
        {composing ? (
          <KeyComposer
            live={live}
            onSend={(chords) => sender.keys(...chords)}
            onClose={() => {
              setComposing(false);
              keysButton.current?.focus();
            }}
          />
        ) : null}
      </div>
      <form className="pane-line" onSubmit={submit} data-keys="own">
        <label className="field">
          <span className="label">Type into the session</span>
          <input
            ref={field}
            className="input mono"
            type="text"
            value={text}
            autoComplete="off"
            spellCheck={false}
            aria-describedby={hint}
            onChange={(event) => setText(event.target.value)}
            onKeyDown={keyDown}
          />
        </label>
        <p className="muted" id={hint}>
          With the field empty, Enter, Escape, and the up and down arrows go to the session.
        </p>
        <label className="pane-check check-row">
          <input type="checkbox" checked={enter} onChange={(event) => setEnter(event.target.checked)} />
          Press Enter after the text
        </label>
        <div className="row">
          <button className="btn btn-primary" type="submit" disabled={!live || text === ""}>
            Send
          </button>
        </div>
      </form>
    </div>
  );
}

// spoken is an overlay Full view leaves uncovered: the toasts, whose messages must still be announced
// (WCAG 2.2 SC 4.1.3) and dismissed while they show above it.
const spoken = ".toasts";

// inertOutside makes everything outside an element inert, from the element up to the body, so nothing Full
// view covers takes focus (WCAG 2.2 SC 2.4.11), and returns how to undo it. What holds the toasts is
// covered around them, what was inert stays inert, and what opens later, such as the command palette, is
// not covered.
function inertOutside(element: HTMLElement): () => void {
  const made: Element[] = [];
  const cover = (node: Element) => {
    if (node.hasAttribute("inert") || node.matches(spoken)) {
      return;
    }
    if (node.querySelector(spoken)) {
      for (const child of node.children) {
        cover(child);
      }
      return;
    }
    node.setAttribute("inert", "");
    made.push(node);
  };
  for (let at: Element = element; at.parentElement && at !== document.body; at = at.parentElement) {
    for (const sibling of at.parentElement.children) {
      if (sibling !== at) {
        cover(sibling);
      }
    }
  }
  return () => {
    for (const node of made) {
      node.removeAttribute("inert");
    }
  };
}

// LanePane is a lane's live pane. A page that holds it may say the person is detached, which takes the
// typing away and leaves the screen, and the state the ledger gives the lane, which labels the last screen
// received when the lane is parked and has no process to show. A page sent to a lane that was just started
// gives the time of the start, so the pane waits for the ledger to hold it; 0 says the lane was not just started.
// The target colours the ring the screen takes while it holds the keys.
// A page that fills the pane gives its screen the height the page shows under it. Full view gives the pane
// the whole view, with everything it covers inert, and its toolbar's Exit full view leaves it. Escape stays
// with the pane, so it can still reach the session.
export function LanePane({
  lane,
  live,
  attached = true,
  state = "",
  startedAt = 0,
  target = "lane",
  fill = false,
}: {
  lane: string;
  live: Live;
  attached?: boolean;
  state?: string;
  startedAt?: number;
  target?: TargetKind;
  fill?: boolean;
}) {
  const shown = usePane(lane, live, startedAt);
  const pane = useRef<HTMLElement>(null);
  const [full, setFull] = useState(false);
  const [failure, setFailure] = useState("");
  // Each input clears the last refusal, and a refusal of this one shows in its place.
  const sender = useMemo<Sends>(() => {
    const pane = new PaneSender(lane, setFailure);
    return {
      keys: (...chords) => {
        setFailure("");
        pane.keys(...chords);
      },
      text: (text, enter) => {
        setFailure("");
        pane.text(text, enter);
      },
    };
  }, [lane]);
  const typing = attached && shown.kind === "screen";
  const keys = useMemo<ScreenKeys>(() => (typing ? { send: (chord) => sender.keys(chord) } : null), [typing, sender]);
  const screen = shown.kind === "screen" || shown.kind === "away" || shown.kind === "refused" ? shown.screen : "";
  const parked = shown.kind !== "screen" && (state === "parked" || (shown.kind === "away" && shown.state === "parked"));
  useEffect(() => (full && pane.current ? inertOutside(pane.current) : undefined), [full]);
  return (
    <section ref={pane} className={full ? `pane is-full t-${target}` : `pane t-${target}`} aria-label={`Pane of lane ${lane}`} data-keys="own">
      <p className="pane-status" role="status">
        {statusOf(shown)}
      </p>
      {shown.kind === "refused" ? (
        <p className="refusal" role="alert">
          {shown.message}
        </p>
      ) : null}
      {parked && screen ? (
        <p className="pane-parked">
          <Badge tone="warning" glyph="pause">
            Parked
          </Badge>
          <span className="small muted">The last screen this page received.</span>
        </p>
      ) : null}
      {screen ? <Screen lane={lane} text={screen} keys={keys} fill={fill} full={full} onFull={() => setFull((was) => !was)} pane={pane} /> : null}
      {attached ? <Typing lane={lane} live={typing} sender={sender} /> : null}
      {failure ? (
        <p className="msg msg-error refusal" role="alert">
          {failure}
        </p>
      ) : null}
    </section>
  );
}

// LanePage is one lane: its facts, its generations, what it left behind, and its terminal pane, as a
// terminal attached to it shows it.
export function LanePage({ id, live }: { id: string; live: Live }) {
  const talk = useTalk();
  const floor = useView<ShopFloor>(() => readView<ShopFloor>("shop-floor"), "lane:floor", live);
  const now = useClock();
  const crumbs = (
    <>
      <Link to="/floor">Floor</Link>
      <span aria-hidden="true">›</span>
      <span>{id || "None"}</span>
    </>
  );
  if (!id) {
    return (
      <Page title="No such lane" crumbs={crumbs}>
        <div className="panel">
          <EmptyState icon="lanes" title="No lane named" action={<Link to="/floor">The floor</Link>} />
        </div>
      </Page>
    );
  }
  const lane = floor.view?.lanes.find((row) => row.id === id);
  return (
    <Page
      title={
        <>
          Lane <span className="title-id">{id}</span>
        </>
      }
      crumbs={crumbs}
      note={lane ? lane.work_order_title || lane.work_order : undefined}
      actions={
        <Button icon="chat" onClick={() => talk.select("lane", id, { open: true, focus: true })}>
          Talk to the lane
        </Button>
      }
    >
      <ViewTime.Provider value={{ asOf: floor.view?.as_of ?? 0, elapsed: Math.max(0, now - floor.readAt) }}>
        <div className="grid grid-main-side lane-page">
          <div className="stack">
            <Panel title="Pane" flush>
              <div className="panel-body">
                <p className="muted small">
                  A terminal attached with <span className="mono">ropeshift lane attach {id}</span> shows the same conversation, and what you type here reaches it
                  too.
                </p>
                <LanePane lane={id} live={live} fill />
              </div>
            </Panel>
            {lane?.generations?.length ? (
              <Panel title="Generations" flush>
                <TableRegion label="Generations">
                  <table className="table stack-on-phone">
                    <thead>
                      <tr>
                        <th scope="col" className="r">Generation</th>
                        <th scope="col">Started</th>
                        <th scope="col">Why it started</th>
                        <th scope="col">Why it ended</th>
                      </tr>
                    </thead>
                    <tbody>
                      {lane.generations.map((generation) => (
                        <tr key={generation.number}>
                          <td data-label="Generation" className="r">{generation.number}</td>
                          <td data-label="Started"><Ago at={generation.started_at} /></td>
                          <td data-label="Why it started">{words(generation.start_reason)}</td>
                          <td data-label="Why it ended">{generation.end_reason ? words(generation.end_reason) : <Absent>still running</Absent>}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </TableRegion>
              </Panel>
            ) : null}
            <LaneMessages lane={id} live={live} />
            <AskedQuestions live={live} lane={id} />
          </div>
          <aside className="stack" aria-label="Lane facts">
            {lane ? <SpendPanel id={lane.work_order} live={live} /> : null}
            <Panel title="Facts">
              {lane ? (
                <Facts>
                  <Fact name="State">
                    <span className="row-tight">
                      <LaneBadge state={lane.actual_state} />
                      {(lane.conditions ?? []).map((condition) => (
                        <ConditionChip key={condition} name={condition} />
                      ))}
                      {lane.checking ? <CheckingChip check={lane.checking} /> : null}
                    </span>
                  </Fact>
                  {lane.waits_on ? (
                    <Fact name="Waits on">
                      <WaitsOnWords waits={lane.waits_on} />
                    </Fact>
                  ) : null}
                  <Fact name="Seen">
                    <Freshness kind={freshnessOf(lane.freshness)} at={lane.as_of} source="the lane's worker" />
                  </Fact>
                  <Fact name="Work order">
                    <Link to={addressOf("/work-order", { id: lane.work_order })}>{lane.work_order}</Link>
                  </Fact>
                  <Fact name="Liveness">{words(lane.liveness)}</Fact>
                  {lane.model_reach ? <Fact name="Model">{words(lane.model_reach)}</Fact> : null}
                  {lane.ledger_reach ? <Fact name="Ledger">{words(lane.ledger_reach)}</Fact> : null}
                  <Fact name="Generation">{lane.generation}</Fact>
                  <Fact name="Worker">{lane.worker_id ? <Id value={lane.worker_id} /> : <Absent>none yet</Absent>}</Fact>
                  <Fact name="Target">
                    <TargetChip kind="lane" small>
                      Lane {lane.id}
                    </TargetChip>
                  </Fact>
                </Facts>
              ) : floor.view ? (
                <p className="muted">The floor lists no lane {id} now.</p>
              ) : (
                <p className="muted">Reading…</p>
              )}
            </Panel>
            {lane?.artifacts?.length ? (
              <Panel title="Artifacts">
                <ul className="criteria" aria-label="Artifacts">
                  {lane.artifacts.map((artifact) => (
                    <li key={artifact.id}>
                      {words(artifact.kind)} <span className="mono">{artifact.uri}</span>
                    </li>
                  ))}
                </ul>
              </Panel>
            ) : null}
          </aside>
        </div>
      </ViewTime.Provider>
    </Page>
  );
}
