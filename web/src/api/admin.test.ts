import { describe, expect, it } from "vitest";

import { serveApi, testProject } from "../testing";
import { createPlanWork, defaultPlanTitle, maxPlanTitle, planCriterion, planTitle } from "./admin";

describe("a planning session's title", () => {
  it("is the first line of the goal that says anything", () => {
    expect(planTitle("\n  \nSaved searches v2\nWith alerts")).toBe("Saved searches v2");
  });

  it("is the session's own name when there is no goal", () => {
    expect(planTitle("")).toBe(defaultPlanTitle);
    expect(planTitle(" \n\t\n")).toBe(defaultPlanTitle);
  });

  it("is cut at 80 characters, counting a character as a person does", () => {
    const long = "é".repeat(maxPlanTitle + 10);
    expect([...planTitle(long)]).toHaveLength(maxPlanTitle);
    expect(planTitle("😀".repeat(maxPlanTitle + 1))).toBe("😀".repeat(maxPlanTitle));
  });
});

describe("creating the work order of a planning session", () => {
  it("records a plan work order with the goal, the title, and the one criterion `plan start` records", async () => {
    const sent = serveApi((call) => (call.method === "POST" ? { status: 201, body: { id: "wo", title: "Saved searches v2" } } : undefined));
    await createPlanWork({ id: "wo", goal: "Saved searches v2\nWith alerts", repository: "repo-1" });
    expect(sent).toHaveLength(1);
    expect(sent[0]).toMatchObject({ method: "POST", project: testProject, path: "/v1/work-orders" });
    expect(sent[0]?.body).toEqual({
      id: "wo",
      kind: "plan",
      title: "Saved searches v2",
      statement: "Saved searches v2\nWith alerts",
      repository_id: "repo-1",
      criteria: [{ id: "c1", text: planCriterion }],
    });
  });

  it("names no repository when none is chosen, and is no adoption", async () => {
    const sent = serveApi(() => ({ status: 201, body: { id: "wo", title: defaultPlanTitle } }));
    await createPlanWork({ id: "wo", goal: "", repository: "" });
    expect(sent[0]?.body).toEqual({ id: "wo", kind: "plan", title: defaultPlanTitle, statement: "", criteria: [{ id: "c1", text: planCriterion }] });
  });
});
