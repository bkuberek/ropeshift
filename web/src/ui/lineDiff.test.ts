import { describe, expect, it } from "vitest";

import { lineDiff } from "./lineDiff";

describe("lineDiff", () => {
  it("keeps the lines both sides share and marks what one side lacks", () => {
    expect(lineDiff("a\nb\nc", "a\nx\nc")).toEqual([
      { kind: "same", text: "a" },
      { kind: "del", text: "b" },
      { kind: "add", text: "x" },
      { kind: "same", text: "c" },
    ]);
  });

  it("reads an empty side as no lines", () => {
    expect(lineDiff("", "one")).toEqual([{ kind: "add", text: "one" }]);
    expect(lineDiff("one", "")).toEqual([{ kind: "del", text: "one" }]);
    expect(lineDiff("", "")).toEqual([]);
  });

  it("finds the longest run the sides share, not the first match", () => {
    expect(lineDiff("x\na\nb", "a\nb\nx").filter((line) => line.kind === "same").map((line) => line.text)).toEqual(["a", "b"]);
  });
});
