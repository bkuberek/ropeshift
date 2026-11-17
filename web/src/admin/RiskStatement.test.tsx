import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { RiskStatement, sourceLabel } from "./RiskStatement";

// The statement is the text `ropeshift worker risk` prints: paragraphs apart by a blank line, and the pages to read
// as a list of lines that begin with "- ". The dialog shows the same words as paragraphs and a list of links.
const statement = `Running this worker unattended means an orchestrator claims work.

Anthropic may rate-limit an account, and these terms can change.

Read the current pages before you acknowledge this:

- https://www.anthropic.com/legal/consumer-terms
- https://code.claude.com/docs/en/legal-and-compliance
- https://support.claude.com/en/articles/15036540
`;

describe("the stated risk", () => {
  it("shows each paragraph apart, with no list marker in the text", () => {
    const { container } = render(<RiskStatement text={statement} />);
    const paragraphs = [...container.querySelectorAll("p")].map((one) => one.textContent);
    expect(paragraphs).toEqual([
      "Running this worker unattended means an orchestrator claims work.",
      "Anthropic may rate-limit an account, and these terms can change.",
      "Read the current pages before you acknowledge this:",
    ]);
    expect(container.textContent).not.toContain("- https");
  });

  it("shows the pages as a list of links, labelled by host", () => {
    render(<RiskStatement text={statement} />);
    const list = screen.getByRole("list");
    const items = within(list).getAllByRole("listitem");
    expect(items).toHaveLength(3);
    const links = within(list).getAllByRole("link");
    expect(links.map((link) => link.getAttribute("href"))).toEqual([
      "https://www.anthropic.com/legal/consumer-terms",
      "https://code.claude.com/docs/en/legal-and-compliance",
      "https://support.claude.com/en/articles/15036540",
    ]);
    expect(links.map((link) => link.textContent)).toEqual([
      "anthropic.com (opens in a new tab)",
      "code.claude.com (opens in a new tab)",
      "support.claude.com (opens in a new tab)",
    ]);
  });

  it("opens each page in a new tab, with no opener and no referrer", () => {
    render(<RiskStatement text={statement} />);
    for (const link of screen.getAllByRole("link")) {
      expect(link.getAttribute("target")).toBe("_blank");
      expect(link.getAttribute("rel")).toBe("noreferrer noopener");
    }
  });

  it("keeps an item that is not a web address as text", () => {
    render(<RiskStatement text={"Read these:\n\n- the plan's terms\n- javascript:alert(1)\n"} />);
    expect(screen.queryByRole("link")).toBeNull();
    expect(screen.getAllByRole("listitem").map((one) => one.textContent)).toEqual(["the plan's terms", "javascript:alert(1)"]);
  });

  it("labels a page by its host, without a leading www", () => {
    expect(sourceLabel("https://www.example.com/a/b")).toBe("example.com");
    expect(sourceLabel("https://docs.example.com/a")).toBe("docs.example.com");
  });
});
