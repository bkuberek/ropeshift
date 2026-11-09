import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { golden } from "../testing";
import { readWake, WakeWords } from "./WakeWords";

// The table internal/parking holds its own words to, which `ropeshift top` says in its short form.
type Said = { name: string; expression: string; words: string; short: string };

const table = golden<Said[]>("wake-words");

function sentence(expression: string, props: { word?: "when" | "until"; capital?: boolean } = {}) {
  return render(
    <p data-testid="said">
      <WakeWords expression={expression} {...props} />
    </p>,
  ).getByTestId("said");
}

describe("a wake in words", () => {
  it.each(table)("says $name as the daemon's table does", ({ expression, words }) => {
    expect(sentence(expression).textContent).toBe(words);
  });

  it("covers every kind of term the table holds", () => {
    expect(table.length).toBeGreaterThanOrEqual(9);
    for (const row of table) {
      expect(readWake(row.expression), row.name).not.toBeNull();
    }
  });

  it("links a question to where it stands", () => {
    sentence(`{"any_of":[{"all_of":[{"question_answered":"q-signing-key"}]},{"human_resume":true}]}`);
    expect(screen.getByRole("link", { name: "q-signing-key" }).getAttribute("href")).toBe("/questions?id=q-signing-key");
  });

  it("waits until, inside a sentence of its own", () => {
    const said = sentence(`{"any_of":[{"all_of":[{"question_answered":"q-1"}]},{"human_resume":true}]}`, { word: "until", capital: false });
    expect(said.textContent).toBe("until question q-1 is answered, or until a person resumes it");
  });

  it("says what it cannot read, and shows it as text", () => {
    const hostile = `{"moon_phase":"<img src=x onerror=alert(1)>"}`;
    const said = sentence(hostile);
    expect(said.textContent).toBe(`Something this build cannot read: ${hostile}`);
    expect(said.querySelector("img")).toBeNull();
  });

  it.each([
    ["a bare name", "human_resume"],
    ["a resume that says false", `{"human_resume":false}`],
    ["a time that is no whole number", `{"at":1.5}`],
    ["an empty choice", `{"any_of":[]}`],
    ["two terms in one object", `{"at":1,"human_resume":true}`],
    ["a named fact with no name", `{"question_answered":""}`],
    ["a term this build has no word for", `{"any_of":[{"human_resume":true},{"moon_phase":"full"}]}`],
    ["a nesting deeper than the language allows", `${`{"all_of":[`.repeat(8)}{"human_resume":true}${"]}".repeat(8)}`],
  ])("reads nothing from %s, as the daemon's evaluator reads nothing", (_name, expression) => {
    expect(readWake(expression)).toBeNull();
  });
});
