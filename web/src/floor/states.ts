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
  blocked: { label: "Blocked", home: "stuck" },
  done: { label: "Done", home: "completed" },
  failed: { label: "Failed", home: "completed" },
  canceled: { label: "Canceled", home: "completed" },
};

// workLabel says a state to a person. A state this build has no words for still shows, as its own name.
export function workLabel(state: string): string {
  return workStates[state]?.label ?? state;
}

// stateCounts lists counts in the machine's order, and anything else after it, so nothing is hidden.
export function stateCounts(states: Record<string, number>): [string, number][] {
  const known = Object.keys(workStates).filter((state) => states[state]);
  const rest = Object.keys(states).filter((state) => !workStates[state]);
  return [...known, ...rest].map((state) => [state, states[state] ?? 0]);
}
