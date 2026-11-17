import { readHealth } from "./admin";
import type { Health } from "./admin";
import { useView } from "./useView";
import type { Live, ViewRead } from "./useView";

// The daemon reads the build installed at its own path once a minute, so asking more often learns nothing.
export const healthGapMs = 60_000;

// useHealth reads what the daemon says of the build it runs, and of a newer one installed beside it. It reads
// nothing before a member is signed in, and a read that fails leaves the last answer standing.
export function useHealth(live: Live, signedIn: boolean): ViewRead<Health | null> {
  return useView<Health | null>(() => (signedIn ? readHealth() : Promise.resolve(null)), `health:${signedIn}`, live, healthGapMs);
}
