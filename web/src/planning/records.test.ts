import { describe, expect, it } from "vitest";

import { categoryProblem, noQuestion, questionProblems, questionRequest } from "./records";

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
