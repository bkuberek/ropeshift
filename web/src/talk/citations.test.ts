import { describe, expect, it } from "vitest";

import { addressOfCitation, parseBlocks, parseInlines } from "./citations";

describe("a citation of a record", () => {
  it("reads each kind of record the assistant may cite", () => {
    const kinds = ["work_order", "lane", "plan", "contract", "requirement", "decision", "question", "document", "repository", "approval"];
    for (const kind of kinds) {
      expect(parseInlines(`See [[${kind}:abc-1]].`), kind).toEqual([{ type: "text", text: "See " }, { type: "record", kind, id: "abc-1" }, { type: "text", text: "." }]);
    }
  });

  it("reads a citation of code as its repository, path, line, and commit", () => {
    expect(parseInlines("At [[code:harbor-web:src/app.ts#L42@a1b2c3d]] it fails.")).toEqual([
      { type: "text", text: "At " },
      { type: "code-ref", repository: "harbor-web", path: "src/app.ts", line: 42, commit: "a1b2c3d" },
      { type: "text", text: " it fails." },
    ]);
  });

  it("leaves an unknown kind, and a malformed citation, as its text", () => {
    expect(parseInlines("[[wizard:abc]]")).toEqual([{ type: "text", text: "[[wizard:abc]]" }]);
    expect(parseInlines("[[code:harbor-web:src/app.ts]]")).toEqual([{ type: "text", text: "[[code:harbor-web:src/app.ts]]" }]);
    expect(parseInlines("[[work_order:]] and [[work_order:a b]]")).toEqual([{ type: "text", text: "[[work_order:]] and [[work_order:a b]]" }]);
    expect(parseInlines("[[work_order:<img src=x onerror=alert(1)>]]")).toEqual([{ type: "text", text: "[[work_order:<img src=x onerror=alert(1)>]]" }]);
  });

  it("reads inline code, and a citation inside it stays code", () => {
    expect(parseInlines("Run `go test` then see [[lane:l-1]], not `[[lane:l-2]]`.")).toEqual([
      { type: "text", text: "Run " },
      { type: "code", text: "go test" },
      { type: "text", text: " then see " },
      { type: "record", kind: "lane", id: "l-1" },
      { type: "text", text: ", not " },
      { type: "code", text: "[[lane:l-2]]" },
      { type: "text", text: "." },
    ]);
  });

  it("names the view each record opens, and never builds an address from text it did not check", () => {
    expect(addressOfCitation({ kind: "work_order", id: "WO-221" })).toBe("/work-order?id=WO-221");
    expect(addressOfCitation({ kind: "lane", id: "L-90" })).toBe("/lane?id=L-90");
    expect(addressOfCitation({ kind: "plan", id: "plan-1" })).toBe("/plans?plan=plan-1");
    expect(addressOfCitation({ kind: "requirement", id: "FR-CHAT-1" })).toBe("/documents?kind=requirements&key=FR-CHAT-1");
    expect(addressOfCitation({ kind: "decision", id: "D-4" })).toBe("/decisions?id=D-4");
    expect(addressOfCitation({ kind: "question", id: "Q-9" })).toBe("/questions?id=Q-9");
    expect(addressOfCitation({ kind: "document", id: "spec:factory-api" })).toBe("/documents?id=spec%3Afactory-api");
    expect(addressOfCitation({ kind: "repository", id: "harbor-web" })).toBe("/repositories?repo=harbor-web");
    expect(addressOfCitation({ kind: "approval", id: "ap-1" })).toBe("/needs-you?item=approval%3Aap-1");
    expect(addressOfCitation({ kind: "contract", id: "C-15" })).toBe("/plans");
  });
});

describe("a message's blocks", () => {
  it("reads paragraphs, a list, and a fenced code block, and nothing else", () => {
    const text = ["First line", "still the first.", "", "Second paragraph.", "", "- one", "- two `x`", "", "```ts", "const a = 1;", "```", "", "1. step", "2. step"].join("\n");
    expect(parseBlocks(text)).toEqual([
      { type: "paragraph", inlines: [{ type: "text", text: "First line\nstill the first." }] },
      { type: "paragraph", inlines: [{ type: "text", text: "Second paragraph." }] },
      {
        type: "list",
        ordered: false,
        items: [[{ type: "text", text: "one" }], [{ type: "text", text: "two " }, { type: "code", text: "x" }]],
      },
      { type: "fence", language: "ts", text: "const a = 1;" },
      { type: "list", ordered: true, items: [[{ type: "text", text: "step" }], [{ type: "text", text: "step" }]] },
    ]);
  });

  it("keeps markup that is not in the subset as text, HTML included", () => {
    const blocks = parseBlocks("**bold** <b>x</b> [a link](https://example.com) # not a heading");
    expect(blocks).toEqual([{ type: "paragraph", inlines: [{ type: "text", text: "**bold** <b>x</b> [a link](https://example.com) # not a heading" }] }]);
  });

  it("closes a fence the message left open at its end", () => {
    expect(parseBlocks("```\nrm -rf /")).toEqual([{ type: "fence", language: "", text: "rm -rf /" }]);
  });

  it("reads nothing from an empty message", () => {
    expect(parseBlocks("")).toEqual([]);
    expect(parseBlocks("\n\n")).toEqual([]);
  });
});
