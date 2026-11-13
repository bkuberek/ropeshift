import type { GlyphName } from "../ui/Icon";

// The words, tone, and glyph for each state of both state machines, in the design's order, and the
// group of work orders a work order in that state stands in. These tables are what
// `every-state.test.tsx` holds to the state tables in the ledger, so a new state there fails that test
// until it has a row here and a view shows it. A state this build has no row for shows as its own name in
// the neutral tone.

// Tone is the color family a state reads in. Ready is the ink outline: it waits on nobody but is set to go.
export type Tone = "neutral" | "ready" | "active" | "success" | "warning" | "danger";

export type StateLook = { label: string; tone: Tone; glyph: GlyphName };

// WorkGroup is a tab of the work orders view, which lists a work order in its state's group.
export type WorkGroup = "open" | "in-progress" | "on-hold" | "stuck" | "review" | "completed";

export type WorkMeaning = StateLook & { home: WorkGroup };

export const workStates: Record<string, WorkMeaning> = {
  intake: { label: "Intake", tone: "neutral", glyph: "ring", home: "open" },
  planning: { label: "Planning", tone: "neutral", glyph: "ring", home: "open" },
  contract_review: { label: "Contract review", tone: "neutral", glyph: "ring", home: "open" },
  ready: { label: "Ready", tone: "ready", glyph: "target", home: "open" },
  claimed: { label: "Claimed", tone: "active", glyph: "dot", home: "in-progress" },
  running: { label: "Running", tone: "active", glyph: "dot", home: "in-progress" },
  parked: { label: "Parked", tone: "warning", glyph: "pause", home: "on-hold" },
  lapsed: { label: "Lapsed", tone: "warning", glyph: "ring", home: "stuck" },
  acceptance: { label: "Waiting for acceptance", tone: "warning", glyph: "half", home: "review" },
  integrating: { label: "Integrating", tone: "active", glyph: "half", home: "review" },
  blocked: { label: "Blocked", tone: "warning", glyph: "triangle", home: "stuck" },
  done: { label: "Done", tone: "success", glyph: "check", home: "completed" },
  failed: { label: "Failed", tone: "danger", glyph: "cross", home: "completed" },
  canceled: { label: "Canceled", tone: "neutral", glyph: "slash", home: "completed" },
};

export const laneStates: Record<string, StateLook> = {
  pending: { label: "Pending", tone: "neutral", glyph: "ring" },
  provisioning: { label: "Provisioning", tone: "active", glyph: "dot" },
  starting: { label: "Starting", tone: "active", glyph: "dot" },
  briefing: { label: "Briefing", tone: "active", glyph: "dot" },
  working: { label: "Working", tone: "active", glyph: "dot" },
  idle: { label: "Idle", tone: "neutral", glyph: "pause" },
  needs_decision: { label: "Needs a decision", tone: "warning", glyph: "person" },
  compacting: { label: "Compacting", tone: "active", glyph: "half" },
  handing_off: { label: "Handing off", tone: "active", glyph: "arrow" },
  parking: { label: "Parking", tone: "warning", glyph: "pause" },
  parked: { label: "Parked", tone: "warning", glyph: "pause" },
  backoff: { label: "Backing off", tone: "warning", glyph: "ring" },
  retiring: { label: "Retiring", tone: "neutral", glyph: "ring" },
  tripped: { label: "Tripped", tone: "danger", glyph: "triangle" },
  ended: { label: "Ended", tone: "neutral", glyph: "slash" },
};

export type ConditionLook = { label: string; tone: "neutral" | "warning" | "danger" };

// The flags a lane can carry beside its state, shown as outlined chips and never as a second badge.
export const conditions: Record<string, ConditionLook> = {
  stalled: { label: "Stalled", tone: "warning" },
  overdue: { label: "Overdue", tone: "warning" },
  waiting_external: { label: "Waiting on something outside", tone: "neutral" },
  near_budget: { label: "Near its budget", tone: "warning" },
  brief_unconfirmed: { label: "Brief not confirmed", tone: "neutral" },
  pack_oversized: { label: "Context pack too large", tone: "warning" },
  handoff_degraded: { label: "Handoff degraded", tone: "danger" },
  degraded_mode: { label: "Degraded mode", tone: "neutral" },
  unpushed_work: { label: "Unpushed work", tone: "neutral" },
  drifted: { label: "Drifted", tone: "warning" },
  day_capped: { label: "Day's generations used", tone: "danger" },
};

export const conditionWords: Record<string, string> = Object.fromEntries(Object.entries(conditions).map(([name, look]) => [name, look.label]));

// The other records' states, each with the same three parts.
export const verdictStates: Record<string, StateLook> = {
  pass: { label: "Pass", tone: "success", glyph: "check" },
  fail: { label: "Fail", tone: "danger", glyph: "cross" },
  cannot_judge: { label: "Cannot judge", tone: "warning", glyph: "question" },
};

export const checkStates: Record<string, StateLook> = {
  passed: { label: "Passed", tone: "success", glyph: "check" },
  success: { label: "Passed", tone: "success", glyph: "check" },
  failed: { label: "Failed", tone: "danger", glyph: "cross" },
  failure: { label: "Failed", tone: "danger", glyph: "cross" },
  pending: { label: "Pending", tone: "warning", glyph: "half" },
  skipped: { label: "Skipped", tone: "neutral", glyph: "slash" },
};

export const milestoneStates: Record<string, StateLook> = {
  not_started: { label: "Not started", tone: "neutral", glyph: "ring" },
  waiting: { label: "Waiting", tone: "warning", glyph: "pause" },
  in_progress: { label: "In progress", tone: "active", glyph: "dot" },
  done: { label: "Done", tone: "success", glyph: "check" },
  existing: { label: "Existing", tone: "success", glyph: "check" },
};

export const requirementStates: Record<string, StateLook> = {
  met_by_existing: { label: "Met by existing code", tone: "success", glyph: "check" },
  planned: { label: "Planned", tone: "neutral", glyph: "ring" },
  in_progress: { label: "In progress", tone: "active", glyph: "dot" },
  in_review: { label: "In review", tone: "warning", glyph: "half" },
  integrating: { label: "Integrating", tone: "active", glyph: "half" },
  met: { label: "Met", tone: "success", glyph: "check" },
};

export const approvalStates: Record<string, StateLook> = {
  pending: { label: "Open", tone: "warning", glyph: "half" },
  open: { label: "Open", tone: "warning", glyph: "half" },
  granted: { label: "Granted", tone: "success", glyph: "check" },
  confirmed: { label: "Confirmed", tone: "success", glyph: "check" },
  approved: { label: "Approved", tone: "success", glyph: "check" },
  superseded: { label: "Superseded", tone: "neutral", glyph: "slash" },
  denied: { label: "Denied", tone: "neutral", glyph: "cross" },
  declined: { label: "Declined", tone: "neutral", glyph: "cross" },
  void: { label: "Void", tone: "neutral", glyph: "slash" },
  expired: { label: "Expired", tone: "neutral", glyph: "slash" },
  running: { label: "Running", tone: "active", glyph: "dot" },
  failed_to_run: { label: "Failed to run", tone: "danger", glyph: "cross" },
};

export const projectStates: Record<string, StateLook> = {
  active: { label: "Active", tone: "active", glyph: "dot" },
  archived: { label: "Archived", tone: "neutral", glyph: "slash" },
};

export const workerStates: Record<string, StateLook> = {
  observed: { label: "Connected", tone: "success", glyph: "dot" },
  last_reported: { label: "Last reported", tone: "neutral", glyph: "ring" },
  disconnected: { label: "Disconnected", tone: "danger", glyph: "cross" },
};

// lookOf finds a state's look in a table, and a state the table lacks shows as its own name, neutral.
export function lookOf(table: Record<string, StateLook>, state: string): StateLook {
  return table[state] ?? { label: state, tone: "neutral", glyph: "ring" };
}

// workLabel says a state to a person. A state this build has no words for still shows, as its own name.
export function workLabel(state: string): string {
  return workStates[state]?.label ?? state;
}

export function laneLabel(state: string): string {
  return laneStates[state]?.label ?? state;
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
