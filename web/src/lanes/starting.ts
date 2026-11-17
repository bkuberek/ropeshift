// A lane a person just started reaches the ledger only when the worker holding its claim reads its desired
// state, a heartbeat or two later, so until then its pane answers not_found. The page that started the lane says
// so in the history entry it moves to, with the time of the start, and the pane there waits for the lane for as
// long as startingMs after it. A lane an address names any other way, such as one that ended or a misspelled one,
// is one the ledger holds already or never will, so its pane is refused at once.
export const startingMs = 120_000;

type Started = { startedLane: string; startedAt: number };

// startedEntry is the history state of a move to the page of a lane that was just started.
export function startedEntry(lane: string): Started {
  return { startedLane: lane, startedAt: Date.now() };
}

// startedAt is when the history entry the page is at says a lane was started, or 0 when it says nothing of it.
export function startedAt(lane: string): number {
  let state: unknown;
  try {
    state = window.history.state;
  } catch {
    return 0;
  }
  const entry = state as Partial<Started> | null;
  return lane !== "" && entry?.startedLane === lane && typeof entry.startedAt === "number" ? entry.startedAt : 0;
}
