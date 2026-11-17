// @vitest-environment node
import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

const css = readFileSync("src/styles/admin.css", "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

// declarations returns what the rule for one selector sets, or undefined when no rule has that selector.
const declarations = (selector: string) => {
  const rule = css.split("}").find((block) => block.split("{")[0]?.split(",").some((name) => name.trim() === selector));
  return rule?.split("{")[1]?.replace(/\s+/g, " ").trim();
};

// The dry run's table sits in a dialog no wider than 820 pixels, and its path and remote can be as long as a
// disk allows, so the columns that can wrap do, and the result never gives way to them.
describe("the register dialog's dry run table", () => {
  it("lets the path and the identity wrap anywhere, so they never push the result out of the dialog", () => {
    const wrap = declarations(".table.dry-run td.wrap");
    expect(wrap).toContain("white-space: normal");
    expect(wrap).toContain("overflow-wrap: anywhere");
  });

  it("keeps the result on one line", () => {
    expect(declarations(".table.dry-run td.result")).toContain("white-space: nowrap");
  });
});
