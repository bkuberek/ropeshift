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
