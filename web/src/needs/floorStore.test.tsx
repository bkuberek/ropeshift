import { act } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { ShopFloor } from "../api/types";
import { floorRecheckMs, floorSnapshot, refreshFloor, subscribeFloor } from "./floorStore";

// Every copy of the no-orchestrator notice follows one read of the shop floor, which a hidden tab does not make, and an
// answer that arrives late never overwrites a newer one.

const reads: { resolve: (floor: ShopFloor) => void; reject: (error: Error) => void }[] = [];

vi.mock("../api/client", () => ({
  readView: vi.fn(
    () =>
      new Promise<ShopFloor>((resolve, reject) => {
        reads.push({ resolve, reject });
      }),
  ),
}));

const floorOf = (name: string) => ({ as_of: 1, workers: [{ worker: { id: name, name } }] }) as unknown as ShopFloor;
const nameOf = () => (floorSnapshot().floor?.workers[0] as unknown as { worker: { name: string } } | undefined)?.worker.name;
const flush = () => act(async () => {});

function setVisibility(state: "visible" | "hidden") {
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
  document.dispatchEvent(new Event("visibilitychange"));
}

beforeEach(() => {
  reads.length = 0;
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
});

afterEach(() => {
  vi.useRealTimers();
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "visible" });
});

describe("the shared read of the floor", () => {
  it("is one read for every copy that follows it, and reads no faster than every five seconds", async () => {
    expect(floorRecheckMs).toBeGreaterThanOrEqual(5_000);
    const stops = [subscribeFloor(() => {}), subscribeFloor(() => {}), subscribeFloor(() => {})];
    expect(reads).toHaveLength(1);
    reads[0]?.resolve(floorOf("a"));
    await flush();
    await vi.advanceTimersByTimeAsync(floorRecheckMs);
    expect(reads).toHaveLength(2);
    reads[1]?.resolve(floorOf("b"));
    await flush();
    expect(nameOf()).toBe("b");
    stops.forEach((stop) => stop());
  });

  it("does not ask again while a read is still out", async () => {
    const stop = subscribeFloor(() => {});
    await vi.advanceTimersByTimeAsync(floorRecheckMs * 3);
    expect(reads).toHaveLength(1);
    stop();
  });

  it("reads nothing while the tab is hidden, and once when it is shown again", async () => {
    const stop = subscribeFloor(() => {});
    reads[0]?.resolve(floorOf("a"));
    await flush();
    setVisibility("hidden");
    await vi.advanceTimersByTimeAsync(floorRecheckMs * 6);
    expect(reads).toHaveLength(1);
    setVisibility("visible");
    expect(reads).toHaveLength(2);
    stop();
  });

  it("starts the next page's reads even when a read of the last page never answered", async () => {
    subscribeFloor(() => {})();
    const stop = subscribeFloor(() => {});
    expect(reads).toHaveLength(2);
    stop();
  });

  it("keeps the newer answer when an older read answers last", async () => {
    const stop = subscribeFloor(() => {});
    const forced = refreshFloor(true);
    expect(reads).toHaveLength(2);
    reads[1]?.resolve(floorOf("newer"));
    await forced;
    reads[0]?.resolve(floorOf("older"));
    await flush();
    expect(nameOf()).toBe("newer");
    stop();
  });

  it("keeps what it last read when a read fails", async () => {
    const stop = subscribeFloor(() => {});
    reads[0]?.resolve(floorOf("a"));
    await flush();
    await vi.advanceTimersByTimeAsync(floorRecheckMs);
    reads[1]?.reject(new Error("offline"));
    await flush();
    expect(nameOf()).toBe("a");
    stop();
  });

  it("drops its read when the last copy goes, so an old answer never shows on the next page", async () => {
    const stop = subscribeFloor(() => {});
    stop();
    reads[0]?.resolve(floorOf("late"));
    await flush();
    expect(floorSnapshot().floor).toBeUndefined();
  });
});
