import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { PageActionsProvider, usePageActions } from "./pageActions";
import type { Entry } from "./ranking";

const entry = (key: string): Entry => ({ key, group: "Actions", title: key });

function Part({ keys }: { keys: string[] }) {
  usePageActions(keys.map(entry));
  return null;
}

// A page has several parts that each list actions in the palette, so none may drop what another listed.
describe("the actions a page lists in the palette", () => {
  it("hold what every part of the page listed, and only what it lists while it stands", () => {
    const held: string[][] = [];
    const take = (entries: Entry[]) => held.push(entries.map((row) => row.key));
    const view = (parts: string[][]) => (
      <PageActionsProvider value={take}>
        {parts.map((keys) => (
          <Part key={keys.join(",")} keys={keys} />
        ))}
      </PageActionsProvider>
    );
    const { rerender, unmount } = render(view([["start"], ["cancel", "retry"]]));
    expect([...(held.at(-1) ?? [])].sort()).toEqual(["cancel", "retry", "start"]);
    rerender(view([["cancel", "retry"]]));
    expect(held.at(-1)).toEqual(["cancel", "retry"]);
    rerender(view([["start"], ["cancel", "retry"], ["release"]]));
    expect([...(held.at(-1) ?? [])].sort()).toEqual(["cancel", "release", "retry", "start"]);
    unmount();
    expect(held.at(-1)).toEqual([]);
  });

  it("change as a part lists something else", () => {
    const held: string[][] = [];
    const take = (entries: Entry[]) => held.push(entries.map((row) => row.key));
    const { rerender } = render(
      <PageActionsProvider value={take}>
        <Part keys={["a"]} />
      </PageActionsProvider>,
    );
    rerender(
      <PageActionsProvider value={take}>
        <Part keys={["b"]} />
      </PageActionsProvider>,
    );
    expect(held.at(-1)).toEqual(["b"]);
  });
});
