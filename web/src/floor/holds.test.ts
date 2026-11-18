import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "vitest";

import { holdWord, holdWords } from "./holds";

// The reasons the ledger names, read from the Go source that defines them.
const ledger = [...readFileSync(resolve(process.cwd(), "../internal/workorders/start.go"), "utf8").matchAll(/^\s*Hold\w+\s+HoldReason = "([a-z_]+)"/gm)].map((found) => found[1] as string);

describe("the words for a start hold", () => {
  it("has a phrase for every reason the ledger names, and none it does not", () => {
    expect(ledger.length).toBeGreaterThan(0);
    expect(Object.keys(holdWords).sort()).toEqual([...ledger].sort());
  });

  it("gives each reason its own plain phrase, not its name", () => {
    const phrases = Object.values(holdWords);
    expect(new Set(phrases).size).toBe(phrases.length);
    for (const [reason, phrase] of Object.entries(holdWords)) {
      expect(phrase).not.toContain("_");
      expect(phrase).not.toBe(reason);
    }
  });

  it("shows a reason this build has no phrase for as its own name", () => {
    expect(holdWord("waiting_for_merge")).toBe("Waiting for a merge");
    expect(holdWord("something_new")).toBe("something_new");
  });
});
