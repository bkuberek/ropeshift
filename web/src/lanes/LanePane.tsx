import { useEffect, useId, useRef, useState } from "react";
import type { FormEvent, KeyboardEvent } from "react";

import { ApiError, followPane, paneKeys, paneRefusal, pressPaneKey, typeIntoPane } from "../api/client";
import type { PaneKey } from "../api/client";
import { readView } from "../api/client";
import type { PaneLane, PaneScreen, ShopFloor } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { addressOf, Link } from "../app/route";
import { laneLabel } from "../floor/states";
import { Ago, Page, useClock, ViewTime, words } from "../parts/parts";
import { Absent, ConditionChip, EmptyState, Fact, Facts, Id, LaneBadge, Panel, TableRegion, TargetChip } from "../ui/display";
import { parseScreen, trimScreen } from "./screen";

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
      {parseScreen(trimScreen(text)).map((run, at) => (
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
    <form className="pane-input" onSubmit={submit} data-keys="own">
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
      <div className="row-tight pane-keys" role="group" aria-label="Press a key">
        {paneKeys.map((key) => (
          <button
            key={key}
            className="btn btn-sm"
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
        <p className="msg msg-error refusal" role="alert">
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
    <section className="pane" aria-label={`Pane of lane ${lane}`} data-keys="own">
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

// LanePage is one lane: its facts, its generations, what it left behind, and its terminal pane, as a
// terminal attached to it shows it.
export function LanePage({ id, live }: { id: string; live: Live }) {
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
                <LanePane lane={id} live={live} />
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
          </div>
          <aside className="stack" aria-label="Lane facts">
            <Panel title="Facts">
              {lane ? (
                <Facts>
                  <Fact name="State">
                    <span className="row-tight">
                      <LaneBadge state={lane.actual_state} />
                      {(lane.conditions ?? []).map((condition) => (
                        <ConditionChip key={condition} name={condition} />
                      ))}
                    </span>
                  </Fact>
                  <Fact name="Seen">
                    <span className={lane.freshness === "observed" ? "fresh" : "fresh reported"}>
                      {lane.freshness === "observed" ? "Observed" : "Last reported"} <Ago at={lane.as_of} />
                    </span>
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
