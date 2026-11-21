import type { WaitsOn } from "../api/types";
import { Lasted } from "../parts/parts";

// WaitsOnWords says whom a lane waits on to go on, the blocking question it waits on, and since when it asked: the
// orchestrator while the question climbs the ladder, and you once it reached you (FR-ORCH-1).
export function WaitsOnWords({ waits }: { waits: WaitsOn }) {
  return (
    <span>
      Waits on {waits.who === "member" ? "you" : "the orchestrator"}, for {waits.question}, since <Lasted at={waits.since} />
    </span>
  );
}
