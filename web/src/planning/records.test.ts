import { describe, expect, it } from "vitest";

import { categoryProblem } from "./records";

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
