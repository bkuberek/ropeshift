import { readView } from "../api/client";
import type { ShopFloor } from "../api/types";

// Every copy of the no-orchestrator notice follows this one read of the shop floor. A page can hold many copies, one for
// each batch of questions that no orchestrator judged and one for each list that waits, and each reading for itself
// would read the floor many times over for one answer.

export type FloorSnapshot = { floor: ShopFloor | undefined; readAt: number };

const nothing: FloorSnapshot = { floor: undefined, readAt: 0 };

// A worker reports its orchestrator's state a moment after a start, and no ledger event announces that, so the floor is
// read again this often while any copy follows it. It is never read faster.
export const floorRecheckMs = 5_000;

let snapshot = nothing;
const listeners = new Set<() => void>();
let timer: ReturnType<typeof setInterval> | undefined;
// asked numbers each read as it starts, and applied is the newest one whose answer was taken, so an older read that
// answers last never overwrites a newer answer.
let asked = 0;
let applied = 0;
let out = 0;
// round counts the times the last copy went, so a read that was out then does not count against the next page's.
let round = 0;

export const floorSnapshot = () => snapshot;

// refreshFloor reads the floor and keeps the answer for every copy. A read still out is not asked again, unless the
// caller knows the floor just changed, as after a start. A failed read leaves what was last read standing.
export async function refreshFloor(force = false): Promise<void> {
  if (out > 0 && !force) {
    return;
  }
  const mine = ++asked;
  const mineRound = round;
  out += 1;
  try {
    const floor = await readView<ShopFloor>("shop-floor");
    if (mine > applied) {
      applied = mine;
      snapshot = { floor, readAt: Date.now() };
      listeners.forEach((listener) => listener());
    }
  } catch {
    // The copies keep what was last read until a read answers.
  } finally {
    if (mineRound === round) {
      out -= 1;
    }
  }
}

// A hidden tab shows nobody the notice, so it reads nothing and reads once when it is shown again.
function follow() {
  clearInterval(timer);
  timer = document.visibilityState === "hidden" ? undefined : setInterval(() => void refreshFloor(), floorRecheckMs);
}

function shown() {
  follow();
  if (document.visibilityState !== "hidden") {
    void refreshFloor();
  }
}

function start() {
  void refreshFloor();
  follow();
  document.addEventListener("visibilitychange", shown);
}

function stop() {
  clearInterval(timer);
  timer = undefined;
  document.removeEventListener("visibilitychange", shown);
  // What is still out answers nobody, and the next page starts from nothing.
  applied = asked;
  round += 1;
  out = 0;
  snapshot = nothing;
}

// subscribeFloor follows the shared read for one copy. The first copy starts it, and the last one ends it.
export function subscribeFloor(listener: () => void): () => void {
  listeners.add(listener);
  if (listeners.size === 1) {
    start();
  } else if (snapshot.floor === undefined || Date.now() - snapshot.readAt >= floorRecheckMs) {
    void refreshFloor();
  }
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) {
      stop();
    }
  };
}
