import type { Lane } from "../api/types";
import { Ago, Fact, words } from "../parts/parts";
import { conditionWords, laneLabel } from "./states";

// Freshness says how far to believe a value (FR-UI-5). What a worker confirmed reads as observed, and
// what it said before going quiet reads as last reported, in a shape of its own.
export function Freshness({ freshness, at }: { freshness: string; at?: number }) {
  const observed = freshness === "observed";
  return (
    <span className={observed ? "fresh" : "fresh fresh-old"}>
      {observed ? "Observed" : "Last reported"}
      {at ? (
        <>
          {" "}
          <Ago at={at} />
        </>
      ) : null}
    </span>
  );
}

export function Conditions({ conditions }: { conditions?: string[] }) {
  if (!conditions?.length) {
    return null;
  }
  return (
    <ul className="tags" aria-label="Conditions">
      {conditions.map((condition) => (
        <li key={condition} className="tag-strong">
          {conditionWords[condition] ?? condition}
        </li>
      ))}
    </ul>
  );
}

// LaneFacts states what a lane last said of itself, and how far that goes. `named` leaves out its ID
// for a card that heads with it.
export function LaneFacts({ lane, named = true }: { lane: Lane; named?: boolean }) {
  return (
    <div className="lane-facts">
      <dl className="facts">
        {named ? (
          <Fact name="Lane" mono>
            {lane.id}
          </Fact>
        ) : null}
        <Fact name="State">{laneLabel(lane.actual_state)}</Fact>
        <Fact name="Seen">
          <Freshness freshness={lane.freshness} at={lane.as_of} />
        </Fact>
        <Fact name="Liveness">{words(lane.liveness)}</Fact>
        {lane.model_reach ? <Fact name="Model">{words(lane.model_reach)}</Fact> : null}
        {lane.ledger_reach ? <Fact name="Ledger">{words(lane.ledger_reach)}</Fact> : null}
        <Fact name="Generation">{lane.generation}</Fact>
        {lane.worker_id ? (
          <Fact name="Worker" mono>
            {lane.worker_id}
          </Fact>
        ) : null}
      </dl>
      <Conditions conditions={lane.conditions} />
    </div>
  );
}
