// The words for each state of both state machines, in the design's order, and the view where a person
// finds what stands in it. These tables are what `every-state.test.tsx` holds to the state tables in
// the ledger, so a new state there fails that test until it has a row here and a view shows it.

export type WorkMeaning = {
  label: string;
  // home is the view that lists a work order in this state.
  home: "plans" | "in-progress" | "on-hold" | "stuck" | "completed";
};

export const workStates: Record<string, WorkMeaning> = {
  intake: { label: "Intake", home: "plans" },
  planning: { label: "Planning", home: "plans" },
  contract_review: { label: "Contract review", home: "plans" },
  ready: { label: "Ready", home: "plans" },
  claimed: { label: "Claimed", home: "in-progress" },
  running: { label: "Running", home: "in-progress" },
  parked: { label: "Parked", home: "on-hold" },
  lapsed: { label: "Lapsed", home: "stuck" },
  acceptance: { label: "Waiting for acceptance", home: "completed" },
  integrating: { label: "Waiting to merge", home: "completed" },
  blocked: { label: "Blocked", home: "stuck" },
  done: { label: "Done", home: "completed" },
  failed: { label: "Failed", home: "completed" },
  canceled: { label: "Canceled", home: "completed" },
};

export const laneStates: Record<string, string> = {
  pending: "Pending",
  provisioning: "Provisioning",
  starting: "Starting",
  briefing: "Briefing",
  working: "Working",
  idle: "Idle",
  needs_decision: "Needs a decision",
  compacting: "Compacting",
  handing_off: "Handing off",
  parking: "Parking",
  parked: "Parked",
  retiring: "Retiring",
  backoff: "Backing off",
  tripped: "Tripped",
  ended: "Ended",
};

// The flags a lane can carry beside its state.
export const conditionWords: Record<string, string> = {
  stalled: "Stalled",
  overdue: "Overdue",
  waiting_external: "Waiting on something outside",
  near_budget: "Near its budget",
  brief_unconfirmed: "Brief not confirmed",
  pack_oversized: "Context pack too large",
  handoff_degraded: "Handoff degraded",
  degraded_mode: "Degraded mode",
  unpushed_work: "Unpushed work",
  drifted: "Drifted",
  day_capped: "Day's generations used",
};

// workLabel says a state to a person. A state this build has no words for still shows, as its own name.
export function workLabel(state: string): string {
  return workStates[state]?.label ?? state;
}

export function laneLabel(state: string): string {
  return laneStates[state] ?? state;
}

// stateCounts lists counts in the machine's order, and anything else after it, so nothing is hidden.
export function stateCounts(states: Record<string, number>): [string, number][] {
  const known = Object.keys(workStates).filter((state) => states[state]);
  const rest = Object.keys(states).filter((state) => !workStates[state]);
  return [...known, ...rest].map((state) => [state, states[state] ?? 0]);
}

// everyCount lists every state of a table, with a zero where it holds nothing, so a person reads the
// whole machine, and whatever else the server counted after it.
export function everyCount(table: Record<string, unknown>, counts: Record<string, number>): [string, number][] {
  const rest = Object.keys(counts).filter((state) => !(state in table));
  return [...Object.keys(table), ...rest].map((state) => [state, counts[state] ?? 0]);
}
