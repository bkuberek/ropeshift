import { describe, expect, it } from "vitest";

import type { Difference } from "../api/types";
import { changeLines, refKind, refName, versionLinks } from "./planParts";

describe("a difference between plan versions", () => {
  it("reads a reference as the kind of record it names and the ID inside the file", () => {
    expect(refKind("contract:core")).toBe("contract");
    expect(refName("contract:core")).toBe("core");
    expect(refName("requirements:factory")).toBe("factory");
    expect(refKind("plain")).toBe("");
    expect(refName("plain")).toBe("plain");
  });

  it("lists each added, changed, and removed record once, in that order, with its kind", () => {
    const difference: Difference = { base: 5, added: ["milestone:m3", "contract:c15"], changed: ["spec:api"], removed: ["spec:old"] };
    expect(changeLines(difference).map((line) => [line.change, line.kind, line.name])).toEqual([
      ["added", "milestone", "m3"],
      ["added", "contract", "c15"],
      ["changed", "spec", "api"],
      ["removed", "spec", "old"],
    ]);
  });

  it("says what changed inside a changed contract", () => {
    const difference: Difference = {
      base: 5,
      changed: ["contract:core"],
      contracts: [{ ref: "contract:core", criteria: { added: ["c2"], changed: ["c1"] }, work_orders: { removed: ["w9"] } }],
    };
    const [line] = changeLines(difference);
    expect(line?.inside).toEqual(["Criteria added: c2", "Criteria changed: c1", "Work orders removed: w9"]);
  });

  it("is empty for a version that differs in nothing", () => {
    expect(changeLines({ base: 5 })).toEqual([]);
    expect(changeLines({})).toEqual([]);
  });
});

describe("the links between a plan's versions", () => {
  const versions = [
    { version: 6, state: "pending", approval: "a6" },
    { version: 5, state: "approved", approval: "a5" },
    { version: 4, state: "superseded", approval: "a4" },
    { version: 3 },
  ];

  it("names the newer version a superseded one stands behind", () => {
    expect(versionLinks(versions, 4)?.newer).toBe(6);
    expect(versionLinks(versions, 6)?.newer).toBeUndefined();
    expect(versionLinks(versions, 3)?.newer).toBe(6);
  });

  it("finds the version in force and the one that waits", () => {
    expect(versionLinks(versions, 4)).toMatchObject({ approved: 5, waiting: 6 });
    expect(versionLinks([{ version: 1, state: "denied" }], 1)).toMatchObject({ approved: undefined, waiting: undefined });
  });

  it("is nothing for a version the plan never had", () => {
    expect(versionLinks(versions, 9)).toBeUndefined();
  });
});
