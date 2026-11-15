import { describe, expect, it } from "vitest";

import { categoryProblem, decisionIDProblem, decisionProblems, decisionRequest, listOf, noDecision, noQuestion, questionProblems, questionRequest } from "./records";

describe("a category's name", () => {
  it.each(["billing", "release-planning", "q3-goals", "a", "x".repeat(63)])("takes %s", (name) => {
    expect(categoryProblem(name)).toBe("");
  });

  it.each([
    ["Billing", "a capital"],
    ["release planning", "a space"],
    ["release_planning", "an underscore"],
    ["-billing", "a hyphen first"],
    ["3rd-party", "a digit first"],
    ["billing!", "a mark"],
    ["x".repeat(64), "more than 63 characters"],
  ])("refuses %s, which has %s", (name) => {
    expect(categoryProblem(name)).toMatch(/lowercase words joined by hyphens/);
  });

  it("asks for a name when there is none", () => {
    expect(categoryProblem("")).toBe("Give the category a name.");
  });
});

describe("a question's draft", () => {
  const written = { ...noQuestion, text: "  Which currency?  " };

  it("needs its text, and nothing else", () => {
    expect(questionProblems(noQuestion)).toEqual({ text: "Write the question." });
    expect(questionProblems({ ...noQuestion, text: "   " })).toEqual({ text: "Write the question." });
    expect(questionProblems(written)).toEqual({});
  });

  it("names a subject by its kind and a plain ID together", () => {
    expect(questionProblems({ ...written, subjectKind: "contract" })).toEqual({ subject: "Name the contract, or choose nothing in particular." });
    expect(questionProblems({ ...written, subjectKind: "contract", subjectID: "has space" })).toEqual({ subject: "An ID is 1 to 64 letters, digits, - and _." });
    expect(questionProblems({ ...written, subjectKind: "contract", subjectID: "x".repeat(65) })).toEqual({ subject: "An ID is 1 to 64 letters, digits, - and _." });
    expect(questionProblems({ ...written, subjectKind: "contract", subjectID: " c-1 " })).toEqual({});
  });

  it("sends what the command sends for the same flags, and leaves out what is empty", () => {
    expect(questionRequest("q-1", written)).toEqual({ id: "q-1", text: "Which currency?" });
    expect(questionRequest("q-1", { ...written, categories: ["data"], subjectKind: "work_order", subjectID: " wo-1 ", blocking: true })).toEqual({
      id: "q-1",
      text: "Which currency?",
      categories: ["data"],
      subject_kind: "work_order",
      subject_id: "wo-1",
      blocking: true,
    });
  });

  it("holds work only on a subject, which a question with none cannot", () => {
    expect(questionRequest("q-1", { ...written, blocking: true })).toEqual({ id: "q-1", text: "Which currency?" });
  });
});

describe("a list a member wrote", () => {
  it("is one entry to a line, with no empty entry and no repeat", () => {
    expect(listOf("requirement:FR-1\n\n  docs/a.md \nrequirement:FR-1\n")).toEqual(["requirement:FR-1", "docs/a.md"]);
    expect(listOf("")).toEqual([]);
  });

  it("reads IDs apart by a space or a comma when told to", () => {
    expect(listOf("d-1, d-2 d-3,d-1\nd-4", /[\s,]+/)).toEqual(["d-1", "d-2", "d-3", "d-4"]);
  });
});

describe("an ID a member names a decision by", () => {
  it.each(["", "decision-1", "a", "x".repeat(64), "PD-1", "pd_1", "apd-1"])("takes %j", (id) => {
    expect(decisionIDProblem(id)).toBe("");
  });

  it.each([["pd-"], ["pd-2f9a"], ["pd-0123456789abcdef0123456789"]])("refuses %s, which only plan approval names decisions under", (id) => {
    expect(decisionIDProblem(id)).toMatch(/may not start with pd-, which only plan approval names decisions under/);
  });

  it.each([["has space"], ["x".repeat(65)], ["a/b"], ["é"]])("refuses %j, which the ledger does not read as an ID", (id) => {
    expect(decisionIDProblem(id)).toBe("An ID is 1 to 64 letters, digits, - and _.");
  });
});

describe("a decision's draft", () => {
  const stated = { ...noDecision, statement: " Charge in euros. " };

  it("needs its statement, and says what is wrong with each ID a member wrote", () => {
    expect(decisionProblems(noDecision)).toEqual({ statement: "State what was decided." });
    expect(decisionProblems(stated)).toEqual({});
    expect(decisionProblems({ ...stated, id: "pd-1", questionID: "has space", noConflict: "d-1, bad/id" })).toMatchObject({
      id: expect.stringMatching(/pd-/),
      questionID: "An ID is 1 to 64 letters, digits, - and _.",
      noConflict: expect.stringMatching(/Each ID is 1 to 64/),
    });
  });

  it("sends what the command sends for the same flags, and leaves out what is empty", () => {
    expect(decisionRequest("generated", stated)).toEqual({ id: "generated", status: "decided", statement: "Charge in euros." });
    expect(decisionRequest("generated", { ...stated, assumed: true }).status).toBe("assumed");
  });

  it("names the decision by the ID a member wrote, and else by the one the dialog made", () => {
    expect(decisionRequest("generated", { ...stated, id: " mine " }).id).toBe("mine");
  });

  it("sends every field a member filled", () => {
    expect(
      decisionRequest("generated", {
        statement: "Charge in euros.",
        rationale: " Why. ",
        assumed: false,
        reversibility: "one_way",
        scope: " billing ",
        categories: ["data"],
        citations: "requirement:FR-1\ndocs/a.md",
        supersedes: "d-3",
        questionID: " q-1 ",
        noConflict: "d-1 d-2",
        id: "",
      }),
    ).toEqual({
      id: "generated",
      status: "decided",
      statement: "Charge in euros.",
      rationale: "Why.",
      citations: ["requirement:FR-1", "docs/a.md"],
      scope: "billing",
      reversibility: "one_way",
      categories: ["data"],
      supersedes: "d-3",
      question_id: "q-1",
      no_conflict: ["d-1", "d-2"],
    });
  });
});
