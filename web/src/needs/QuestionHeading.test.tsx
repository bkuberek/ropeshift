import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { fullQuestionTitle } from "./kinds";
import { QuestionHeading } from "./QuestionHeading";

const long = "Should the importer keep the original column names when the source file has duplicates, or rename each duplicate with a numeric suffix?";

describe("a question's heading", () => {
  it("is the question's own text, with no title attribute when nothing is cut", () => {
    const { container } = render(<QuestionHeading text="Which store holds the index?" />);
    const heading = container.querySelector("span");
    expect(heading?.textContent).toBe("Which store holds the index?");
    expect(heading?.hasAttribute("title")).toBe(false);
  });

  it("is cut when long, and holds the whole text as its title", () => {
    const { container } = render(<QuestionHeading text={long} />);
    const heading = container.querySelector("span");
    expect(heading?.textContent?.endsWith("…")).toBe(true);
    expect(heading?.textContent).not.toBe(long);
    expect(heading?.getAttribute("title")).toBe(long);
  });

  it("is A question when the text is empty", () => {
    const { container } = render(<QuestionHeading text="  " />);
    expect(container.querySelector("span")?.textContent).toBe("A question");
    expect(container.querySelector("span")?.hasAttribute("title")).toBe(false);
  });
});

describe("the full title of a question", () => {
  it("is the text on one line, uncut", () => {
    expect(fullQuestionTitle(`${long}\n  Again?`)).toBe(`${long} Again?`);
  });

  it("is A question when the text is empty", () => {
    expect(fullQuestionTitle("")).toBe("A question");
  });
});
