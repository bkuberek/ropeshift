import { describe, expect, it } from "vitest";

import { findIn, markRuns } from "./find";

describe("finding text in a pane", () => {
  it("finds every place the words occur, apart from case unless asked", () => {
    expect(findIn("Closed berths, closed for now. CLOSED.", "closed", false)).toEqual([0, 15, 31]);
    expect(findIn("Closed berths, closed for now. CLOSED.", "closed", true)).toEqual([15]);
    expect(findIn("aaaa", "aa", false)).toEqual([0, 2]);
    expect(findIn("anything", "", false)).toEqual([]);
  });

  it("marks each match across the runs of colour it spans, and keeps the text", () => {
    const runs = [
      { text: "a clo", className: "" },
      { text: "sed berth", className: "sgr-bold" },
    ];
    const marked = markRuns(runs, [2], 6);
    expect(marked.map((run) => run.text).join("")).toBe("a closed berth");
    expect(marked).toEqual([
      { text: "a ", className: "", match: null },
      { text: "clo", className: "", match: 0 },
      { text: "sed", className: "sgr-bold", match: 0 },
      { text: " berth", className: "sgr-bold", match: null },
    ]);
  });

  it("leaves the runs whole with nothing to mark", () => {
    const runs = [{ text: "plain", className: "" }];
    expect(markRuns(runs, [], 0)).toEqual([{ text: "plain", className: "", match: null }]);
  });
});
