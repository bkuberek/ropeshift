// How a lane ranks on the floor: lanes that run, idle ones among them, first, then lanes that wait on a person, then parked lanes, then the
// rest. An ended lane is not ranked: the floor holds it in its own group below the live ones.
const working = new Set(["provisioning", "starting", "briefing", "working", "idle", "compacting", "handing_off"]);
const parked = new Set(["parking", "parked"]);

export function laneRank(state: string): number {
  if (working.has(state)) {
    return 0;
  }
  if (state === "needs_decision") {
    return 1;
  }
  return parked.has(state) ? 2 : 3;
}

// splitLanes puts the live lanes in rank order, each rank keeping the order the server listed, and the ended
// lanes apart in the order the server listed them.
export function splitLanes<T extends { lane: { actual_state: string } }>(rows: T[]): { live: T[]; ended: T[] } {
  const ended = rows.filter((row) => row.lane.actual_state === "ended");
  const live = rows
    .filter((row) => row.lane.actual_state !== "ended")
    .map((row, index) => ({ row, index }))
    .sort((a, b) => laneRank(a.row.lane.actual_state) - laneRank(b.row.lane.actual_state) || a.index - b.index)
    .map(({ row }) => row);
  return { live, ended };
}
