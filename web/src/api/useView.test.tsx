import { act, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { useView } from "./useView";

// A read of a costly view holds to one per gap however much a person hears, but asking for something else is
// not hearing more of the same, so it reads at once.

function Reader({ read, name, heard, gapMs }: { read: () => Promise<string>; name: string; heard: number; gapMs: number }) {
  const state = useView<string>(read, name, { heard, checkSession: async () => {} }, gapMs);
  return <p>{state.view ?? "nothing yet"}</p>;
}

describe("useView with a gap", () => {
  it("reads at once the first time and when it is asked for something else, and holds what it hears to the gap", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const read = vi.fn(async () => "read");
    const { rerender } = render(<Reader read={read} name="a" heard={0} gapMs={30_000} />);
    await waitFor(() => expect(read).toHaveBeenCalledTimes(1));

    rerender(<Reader read={read} name="a" heard={1} gapMs={30_000} />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(read, "what it heard, inside the gap").toHaveBeenCalledTimes(1);

    rerender(<Reader read={read} name="b" heard={1} gapMs={30_000} />);
    await waitFor(() => expect(read, "something else").toHaveBeenCalledTimes(2));

    rerender(<Reader read={read} name="b" heard={2} gapMs={30_000} />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(read, "what it heard of that, inside the gap").toHaveBeenCalledTimes(2);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(31_000);
    });
    expect(read, "what it heard, once the gap passed").toHaveBeenCalledTimes(3);
    expect(screen.getByText("read")).toBeTruthy();
    vi.useRealTimers();
  });
});

// Some of what a view reads changes with no ledger event to hear, as a forecast the daemon's tick keeps. While what
// it read says it waits for that, it asks again once a gap with nothing heard, and stops once it no longer waits.
function Waiting({ read }: { read: () => Promise<string> }) {
  const state = useView<string>(read, "waits", { heard: 0, checkSession: async () => {} }, 30_000, (view) => view === "pending");
  return <p>{state.view ?? "nothing yet"}</p>;
}

describe("useView while what it read waits", () => {
  it("asks again once a gap with nothing heard, until what it reads no longer waits", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      let answer = "pending";
      const read = vi.fn(async () => answer);
      render(<Waiting read={read} />);
      await waitFor(() => expect(screen.getByText("pending")).toBeTruthy());
      expect(read).toHaveBeenCalledTimes(1);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(31_000);
      });
      expect(read, "once a gap, though nothing was heard").toHaveBeenCalledTimes(2);
      answer = "made";
      await act(async () => {
        await vi.advanceTimersByTimeAsync(31_000);
      });
      await waitFor(() => expect(screen.getByText("made")).toBeTruthy());
      const asked = read.mock.calls.length;
      await act(async () => {
        await vi.advanceTimersByTimeAsync(120_000);
      });
      expect(read, "nothing more, once it no longer waits").toHaveBeenCalledTimes(asked);
    } finally {
      vi.useRealTimers();
    }
  });
});
