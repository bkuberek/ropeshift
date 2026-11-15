import { describe, expect, it } from "vitest";

import { inMore, noWork, workProblems, workRequest } from "./newWork";
import type { WorkDraft } from "./newWork";

const draft = (changes: Partial<WorkDraft>): WorkDraft => ({ ...noWork, title: "Export the invoices", ...changes });

describe("the request a new work order sends", () => {
  it("is the title alone when that is all a member wrote, as the command sends it", () => {
    expect(workRequest("wo-1", draft({}))).toEqual({ id: "wo-1", title: "Export the invoices" });
  });

  it("numbers the criteria as the command does, one to a line, and drops the empty lines", () => {
    const request = workRequest("wo-1", draft({ criteria: " An export downloads.\n\n  Quotes are escaped. \n" }));
    expect(request.criteria).toEqual([
      { id: "c1", text: "An export downloads." },
      { id: "c2", text: "Quotes are escaped." },
    ]);
  });

  it("sends each field a member filled, and leaves out each they did not", () => {
    const request = workRequest(
      "wo-1",
      draft({
        kind: "plan",
        statement: " The export offers CSV. ",
        repository: "id-api",
        dependsOn: "wo-1, wo-2\nwo-1",
        requirements: "FR-A-1 FR-B.2",
        agent: "implementation",
        size: "small",
        priority: "3",
        id: " wo-form ",
      }),
    );
    expect(request).toEqual({
      id: "wo-form",
      kind: "plan",
      title: "Export the invoices",
      statement: "The export offers CSV.",
      repository_id: "id-api",
      depends_on: ["wo-1", "wo-2"],
      requirements: ["FR-A-1", "FR-B.2"],
      agent: "implementation",
      size: "small",
      priority: 3,
    });
  });

  it("leaves a change's kind to the API, which reads none as a change, and a priority of zero too", () => {
    const request = workRequest("wo-1", draft({ kind: "change", priority: "0" }));
    expect(request).not.toHaveProperty("kind");
    expect(request).not.toHaveProperty("priority");
  });

  it("reads a negative priority, which the command takes", () => {
    expect(workRequest("wo-1", draft({ priority: "-2" })).priority).toBe(-2);
  });
});

describe("what stops a new work order from being sent", () => {
  it("is nothing for a title, and the title is what it asks for first", () => {
    expect(workProblems(draft({}))).toEqual({});
    expect(workProblems(noWork).title).toBe("Give the work order a title.");
    expect(workProblems(draft({ title: "   " })).title).toBe("Give the work order a title.");
  });

  it("reads an ID, each dependency, and each requirement as the ledger does", () => {
    expect(workProblems(draft({ id: "wo 1" })).id).toMatch(/1 to 64 letters, digits/);
    expect(workProblems(draft({ id: "x".repeat(65) })).id).toMatch(/1 to 64 letters, digits/);
    expect(workProblems(draft({ id: "wo_1-a" }))).toEqual({});
    expect(workProblems(draft({ dependsOn: "wo-1 not/an/id" })).dependsOn).toMatch(/1 to 64 letters, digits/);
    expect(workProblems(draft({ dependsOn: "wo-1, wo-2" }))).toEqual({});
    expect(workProblems(draft({ requirements: "FR-A-1 FR@B" })).requirements).toMatch(/letters, digits, - _ and \./);
    expect(workProblems(draft({ requirements: "FR-A-1 FR_B.2" }))).toEqual({});
  });

  it("reads a priority as a whole number", () => {
    for (const bad of ["high", "1.5", "1e3", "--1", "9007199254740993"]) {
      expect(workProblems(draft({ priority: bad })).priority, bad).toBe("A priority is a whole number, such as 2.");
    }
    for (const good of ["", "0", "7", "-3", " 4 "]) {
      expect(workProblems(draft({ priority: good })).priority, good).toBeUndefined();
    }
  });

  it("says which problems sit under More, where a closed section would hide them", () => {
    expect(inMore({ title: "Give the work order a title." })).toBe(false);
    for (const field of ["id", "dependsOn", "requirements", "priority"] as const) {
      expect(inMore({ [field]: "a problem" })).toBe(true);
    }
  });
});
