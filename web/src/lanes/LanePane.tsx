import { useEffect, useId, useRef, useState } from "react";
import type { FormEvent, KeyboardEvent } from "react";

import { ApiError, followPane, paneKeys, paneRefusal, pressPaneKey, typeIntoPane } from "../api/client";
import type { PaneKey } from "../api/client";
import type { PaneLane, PaneScreen } from "../api/types";
import type { Live } from "../api/useView";
import { Link } from "../app/route";
import { laneLabel } from "../floor/states";
import { Page } from "../parts/parts";
import { parseScreen } from "./screen";

// retryMs is how long the page waits before it opens a pane's stream again: after the stream closed
// with nothing to say, or on a lane whose pane has not opened yet.
const retryMs = 3000;

// The refusals that can pass, so the page asks again.
const passing = new Set(["no_pane", "unreachable", "unavailable", "session_required"]);

// The keys an empty field sends to the session, so a person can answer a menu without leaving it.
const fieldKeys: Record<string, PaneKey> = { Enter: "Enter", Escape: "Escape", ArrowUp: "Up", ArrowDown: "Down" };

type Shown =
  | { kind: "opening" }
  | { kind: "screen"; generation: number; screen: string }
  | { kind: "away"; generation: number; state: string; screen: string }
  | { kind: "refused"; message: string };

// usePane follows a lane's pane. The server follows the lane from one generation's pane to the next, so
// the page holds one stream for the life of the page.
function usePane(lane: string, live: Live): Shown {
  const [shown, setShown] = useState<Shown>({ kind: "opening" });
  const session = useRef(live.checkSession);
  session.current = live.checkSession;

  useEffect(() => {
    let stream: EventSource | null = null;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let gone = false;
    let last = "";
    const explain = async () => {
      const refused = await paneRefusal(lane);
      if (gone) {
        return;
      }
      if (refused) {
        setShown({ kind: "refused", message: refused.message });
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

function Screen({ lane, text }: { lane: string; text: string }) {
  return (
    // The whole screen changes at once, so a screen reader reads it when asked and the status line says
    // what changed.
    <pre className="pane-screen" role="log" aria-label={`Screen of lane ${lane}`} aria-live="off" tabIndex={0}>
      {parseScreen(text).map((run, at) => (
        <span key={at} className={run.className || undefined}>
          {run.text}
        </span>
      ))}
    </pre>
  );
}

// Typing sends one input after another, in order, so no key overtakes the text before it.
function Typing({ lane, live }: { lane: string; live: boolean }) {
  const field = useRef<HTMLInputElement>(null);
  const hint = useId();
  const [text, setText] = useState("");
  const [enter, setEnter] = useState(true);
  const [failure, setFailure] = useState("");
  const queue = useRef<Promise<void>>(Promise.resolve());

  const send = (input: () => Promise<unknown>) => {
    setFailure("");
    queue.current = queue.current.then(async () => {
      try {
        await input();
      } catch (error) {
        setFailure(error instanceof ApiError ? error.message : String(error));
      }
    });
    field.current?.focus();
  };
  const press = (key: PaneKey) => {
    if (live) {
      send(() => pressPaneKey(lane, key));
    }
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!live || text === "") {
      return;
    }
    const line = text;
    setText("");
    send(() => typeIntoPane(lane, line, enter));
  };
  const keyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    const key = fieldKeys[event.key];
    if (key && text === "" && !event.shiftKey && !event.altKey && !event.ctrlKey && !event.metaKey) {
      event.preventDefault();
      press(key);
    }
  };

  return (
    <form className="pane-input" onSubmit={submit}>
      <label className="field">
        <span className="field-label">Type into the session</span>
        <input
          ref={field}
          type="text"
          value={text}
          autoComplete="off"
          spellCheck={false}
          aria-describedby={hint}
          onChange={(event) => setText(event.target.value)}
          onKeyDown={keyDown}
        />
      </label>
      <p className="quiet" id={hint}>
        With the field empty, Enter, Escape, and the up and down arrows go to the session.
      </p>
      <label className="pane-check">
        <input type="checkbox" checked={enter} onChange={(event) => setEnter(event.target.checked)} />
        Press Enter after the text
      </label>
      <div className="actions">
        <button className="button button-strong" type="submit" disabled={!live || text === ""}>
          Send
        </button>
      </div>
      <div className="actions pane-keys" role="group" aria-label="Press a key">
        {paneKeys.map((key) => (
          <button
            key={key}
            className="button"
            type="button"
            disabled={!live}
            // A press with the pointer leaves the keyboard in the field.
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => press(key)}
          >
            {key}
          </button>
        ))}
      </div>
      {failure ? (
        <p className="refusal" role="alert">
          {failure}
        </p>
      ) : null}
    </form>
  );
}

export function LanePane({ lane, live }: { lane: string; live: Live }) {
  const shown = usePane(lane, live);
  const screen = shown.kind === "screen" || shown.kind === "away" ? shown.screen : "";
  return (
    <section className="pane" aria-label={`Pane of lane ${lane}`}>
      <p className="pane-status" role="status">
        {statusOf(shown)}
      </p>
      {shown.kind === "refused" ? (
        <p className="refusal" role="alert">
          {shown.message}
        </p>
      ) : null}
      {screen ? <Screen lane={lane} text={screen} /> : null}
      <Typing lane={lane} live={shown.kind === "screen"} />
    </section>
  );
}

// LanePage is an interactive lane's pane, as a terminal attached to it shows it.
export function LanePage({ id, live }: { id: string; live: Live }) {
  if (!id) {
    return (
      <Page title="No such lane">
        <p className="back">
          <Link to="/shop-floor">The shop floor</Link>
        </p>
      </Page>
    );
  }
  return (
    <Page title="Lane" note={id}>
      <p className="lead">
        This is the lane's terminal pane. A terminal attached with <span className="mono">ropeshift lane attach {id}</span> shows the same
        conversation, and what you type here reaches it too.
      </p>
      <LanePane lane={id} live={live} />
    </Page>
  );
}
