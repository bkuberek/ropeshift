import type { Lane } from "../api/types";
import { Ago, Fact, Id, words } from "../parts/parts";
import { conditionWords, laneLabel } from "./states";

// Freshness says how far to believe a value (FR-UI-5). What a worker confirmed reads as observed, and
// what it said before going quiet reads as last reported, in a shape of its own. Beside a value it is a
// note, and `alone` makes it the value of a fact that states nothing else.
export function Freshness({ freshness, at, alone = false }: { freshness: string; at?: number; alone?: boolean }) {
  const observed = freshness === "observed";
  return (
    <span className={[alone ? "" : "fresh", observed ? "" : "fresh-old"].filter(Boolean).join(" ") || undefined}>
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
          <Fact name="Lane">
            <Id value={lane.id} />
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
          <Fact name="Worker">
            <Id value={lane.worker_id} />
          </Fact>
        ) : null}
      </dl>
      <Conditions conditions={lane.conditions} />
    </div>
  );
}
