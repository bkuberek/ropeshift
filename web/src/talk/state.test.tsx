import { act, render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { TalkProvider, useTalk } from "./state";
import type { Talk } from "./state";

// hold renders the provider for one project and hands the test its controls.
function hold(project: string) {
  const seen: { talk: Talk | null } = { talk: null };
  const Probe = () => {
    seen.talk = useTalk();
    return null;
  };
  const view = render(
    <TalkProvider project={project} openDock={() => {}}>
      <Probe />
    </TalkProvider>,
  );
  return {
    talk: () => seen.talk as Talk,
    in: (next: string) =>
      view.rerender(
        <TalkProvider project={next} openDock={() => {}}>
          <Probe />
        </TalkProvider>,
      ),
  };
}

describe("a request to start a conversation's session", () => {
  it("is answered once, for the conversation it was made for", () => {
    const held = hold("harbor");
    act(() => held.talk().select("assistant", "conv-1", { start: true }));
    expect(held.talk().takeAsk("resume", "harbor", "conv-2")).toBe(false);
    expect(held.talk().takeAsk("resume", "harbor", "conv-1")).toBe(false);
    act(() => held.talk().select("assistant", "conv-1", { start: true }));
    expect(held.talk().takeAsk("resume", "harbor", "conv-1")).toBe(true);
    expect(held.talk().takeAsk("resume", "harbor", "conv-1")).toBe(false);
  });

  it("is dropped by choosing another target", () => {
    const held = hold("harbor");
    act(() => held.talk().select("assistant", "conv-1", { start: true }));
    act(() => held.talk().select("lane", "L-88"));
    expect(held.talk().takeAsk("resume", "harbor", "conv-1")).toBe(false);
  });

  it("is dropped by changing the project, so another project's newest conversation never starts", () => {
    const held = hold("harbor");
    act(() => held.talk().select("assistant", "conv-1", { start: true }));
    held.in("kestrel");
    expect(held.talk().takeAsk("resume", "kestrel", "conv-1")).toBe(false);
    expect(held.talk().takeAsk("resume", "harbor", "conv-1")).toBe(false);
  });

  it("starts nothing for a conversation no one named", () => {
    const held = hold("harbor");
    act(() => held.talk().select("assistant", "", { start: true }));
    expect(held.talk().takeAsk("resume", "harbor", "")).toBe(false);
  });
});

describe("a request for a new conversation", () => {
  it("is answered once, in the project it was made in, and dropped by changing it", () => {
    const held = hold("harbor");
    act(() => held.talk().requestNew());
    expect(held.talk().takeAsk("new", "kestrel", "")).toBe(false);
    act(() => held.talk().requestNew());
    expect(held.talk().takeAsk("new", "harbor", "")).toBe(true);
    expect(held.talk().takeAsk("new", "harbor", "")).toBe(false);
    act(() => held.talk().requestNew());
    held.in("kestrel");
    expect(held.talk().takeAsk("new", "kestrel", "")).toBe(false);
  });
});
