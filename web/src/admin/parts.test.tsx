import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { useDraft } from "./parts";

type Caps = { limit: string };

describe("useDraft", () => {
  it("never renders a draft that differs from a saved record that has just arrived", () => {
    const seen: { draft: Caps; saved: Caps }[] = [];
    const { rerender } = renderHook(
      ({ saved }: { saved: Caps }) => {
        const [draft, , stable] = useDraft<Caps>(saved);
        seen.push({ draft, saved: stable });
        return draft;
      },
      { initialProps: { saved: { limit: "" } } },
    );
    seen.length = 0;
    rerender({ saved: { limit: "3" } });
    // A field that renders with the first record's draft can be edited before the draft follows, and the
    // edit is then undone by the draft taking the record.
    expect(seen.length).toBeGreaterThan(0);
    for (const render of seen) {
      expect(render.draft).toEqual(render.saved);
    }
  });

  it("keeps an edited draft when a read brings the same record again", () => {
    const { result, rerender } = renderHook(({ saved }: { saved: Caps }) => useDraft<Caps>(saved), { initialProps: { saved: { limit: "3" } } });
    act(() => result.current[1]({ limit: "5" }));
    rerender({ saved: { limit: "3" } });
    expect(result.current[0]).toEqual({ limit: "5" });
  });

  it("resets the draft to a record that changed", () => {
    const { result, rerender } = renderHook(({ saved }: { saved: Caps }) => useDraft<Caps>(saved), { initialProps: { saved: { limit: "3" } } });
    act(() => result.current[1]({ limit: "5" }));
    rerender({ saved: { limit: "4" } });
    expect(result.current[0]).toEqual({ limit: "4" });
  });
});
