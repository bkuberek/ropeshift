import { useEffect, useId, useRef, useState } from "react";
import type { FormEvent, KeyboardEvent } from "react";

import { ApiError, followPane, paneKeys, paneRefusal, pressPaneKey, typeIntoPane } from "../api/client";
import type { PaneKey } from "../api/client";
import { readView } from "../api/client";
import type { PaneLane, PaneScreen, ShopFloor } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { usePageActions } from "../app/pageActions";
import { addressOf, Link } from "../app/route";
import { SpendPanel } from "../floor/SpendPanel";
import { laneLabel } from "../floor/states";
import { Ago, Page, useClock, ViewTime, words } from "../parts/parts";
import { Absent, Badge, ConditionChip, EmptyState, Fact, Facts, Freshness, freshnessOf, Id, LaneBadge, Panel, TableRegion, TargetChip } from "../ui/display";
import { AskedQuestions } from "../planning/AskedQuestions";
import { useTalk } from "../talk/state";
import { Button } from "../ui/controls";
import { LaneMessages } from "./Messages";
import { parseScreen, trimScreen } from "./screen";
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
const fieldKeys: Record<string, PaneKey> = { Enter: "Enter", Escape: "Escape", ArrowUp: "Up", ArrowDown: "Down" };

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
  // The command palette puts the keyboard in the field, and sends nothing.
  usePageActions(live ? [{ key: `action:pane-keys:${lane}`, group: "Actions", id: lane, title: `Send keys to the pane of lane ${lane}`, icon: "terminal", keywords: "type input session", run: () => field.current?.focus() }] : []);

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

// LanePane is a lane's live pane. A page that holds it may say the person is detached, which takes the
// typing away and leaves the screen, and the state the ledger gives the lane, which labels the last screen
// received when the lane is parked and has no process to show. A page sent to a lane that was just started
// gives the time of the start, so the pane waits for the ledger to hold it; 0 says the lane was not just started.
export function LanePane({ lane, live, attached = true, state = "", startedAt = 0 }: { lane: string; live: Live; attached?: boolean; state?: string; startedAt?: number }) {
  const shown = usePane(lane, live, startedAt);
  const screen = shown.kind === "screen" || shown.kind === "away" || shown.kind === "refused" ? shown.screen : "";
  const parked = shown.kind !== "screen" && (state === "parked" || (shown.kind === "away" && shown.state === "parked"));
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
      {parked && screen ? (
        <p className="pane-parked">
          <Badge tone="warning" glyph="pause">
            Parked
          </Badge>
          <span className="small muted">The last screen this page received.</span>
        </p>
      ) : null}
      {screen ? <Screen lane={lane} text={screen} /> : null}
      {attached ? <Typing lane={lane} live={shown.kind === "screen"} /> : null}
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
                    </span>
                  </Fact>
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
