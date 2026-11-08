import { useId } from "react";

import type { Lane, Orchestrator, ShopFloor as View, WorkerLine } from "../api/types";
import { Ago, Fact, Section, words } from "../parts/parts";
import { Freshness, LaneFacts } from "./LaneFacts";
import { laneLabel } from "./states";

function OrchestratorFacts({ orchestrator, freshness }: { orchestrator?: Orchestrator; freshness: string }) {
  if (!orchestrator) {
    return <p className="quiet">No orchestrator.</p>;
  }
  return (
    <div role="group" aria-label="Orchestrator">
      <dl className="facts">
        <Fact name="Orchestrator" mono>
          {orchestrator.id}
        </Fact>
        <Fact name="Wanted">{words(orchestrator.desired_state)}</Fact>
        <Fact name="Actual">
          {orchestrator.actual_state ? (
            <>
              {laneLabel(orchestrator.actual_state)} <Freshness freshness={freshness} />
            </>
          ) : (
            "Not reported"
          )}
        </Fact>
        <Fact name="Generation">{orchestrator.current_generation ?? "None yet"}</Fact>
        <Fact name="Runs">{orchestrator.run}</Fact>
        <Fact name="Breaker resets">{orchestrator.resets}</Fact>
      </dl>
    </div>
  );
}

// A heartbeat writes nothing about a worker, so the time beside its freshness is its last handshake,
// stated apart, and the word for its freshness comes from the server alone.
function WorkerCard({ line }: { line: WorkerLine }) {
  const heading = useId();
  const { worker } = line;
  const old = line.freshness !== "observed";
  return (
    <article className={old ? "card lane-card lane-old" : "card lane-card"} aria-labelledby={heading}>
      <h3 className="card-title" id={heading}>
        {worker.name || worker.id}
      </h3>
      <div className="card-body">
        <p className="mono quiet">{worker.id}</p>
        <dl className="facts">
          <Fact name="Seen">
            <Freshness freshness={line.freshness} />
          </Fact>
          {worker.last_handshake_at ? (
            <Fact name="Last handshake">
              <Ago at={worker.last_handshake_at} />
            </Fact>
          ) : null}
          <Fact name="Epoch">{worker.epoch}</Fact>
          <Fact name="Protocol">{worker.protocol}</Fact>
        </dl>
        <OrchestratorFacts orchestrator={line.orchestrator} freshness={line.freshness} />
      </div>
    </article>
  );
}

function LaneCard({ lane }: { lane: Lane }) {
  const heading = useId();
  const old = lane.freshness !== "observed";
  return (
    <li>
      <article className={old ? "card lane-card lane-old" : "card lane-card"} aria-labelledby={heading}>
        <h3 className="lane-id mono" id={heading}>
          {lane.id}
        </h3>
        {lane.work_order ? <p className="mono quiet">{lane.work_order}</p> : null}
        <LaneFacts lane={lane} named={false} />
      </article>
    </li>
  );
}

function Lanes({ title, note, lanes }: { title: string; note: string; lanes: Lane[] }) {
  return (
    <Section title={title}>
      <p className="quiet">{note}</p>
      {lanes.length ? (
        <ul className="board">
          {lanes.map((lane) => (
            <LaneCard key={lane.id} lane={lane} />
          ))}
        </ul>
      ) : (
        <p>None.</p>
      )}
    </Section>
  );
}

export function ShopFloor({ view }: { view: View }) {
  if (view.workers.length === 0 && view.lanes.length === 0) {
    return <p className="empty">No worker has joined.</p>;
  }
  const lanesOf = (freshness: string) => view.lanes.filter((lane) => lane.freshness === freshness);
  return (
    <>
      <Section title="Workers">
        <div className="board">
          {view.workers.map((line) => (
            <WorkerCard key={line.worker.id} line={line} />
          ))}
        </div>
      </Section>
      <Lanes title="Observed lanes" note="A worker confirmed these within the freshness window, or the ledger holds them itself." lanes={lanesOf("observed")} />
      <Lanes title="Last reported lanes" note="These are the last values a worker sent, and they may no longer hold." lanes={lanesOf("last_reported")} />
    </>
  );
}
