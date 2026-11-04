import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { DocumentText, blocksOf } from "./DocumentText";

const markup = `<img src=x onerror=alert(1)><script>alert(2)</script>`;

function show(body: string, title = "") {
  return render(<DocumentText body={body} title={title} />);
}

describe("blocksOf", () => {
  it("reads headings, paragraphs, lists, quotes, and rules", () => {
    expect(blocksOf("# One\n\nA line\nthat wraps.\n\n- a\n- b\n\n1. c\n\n> said\n\n---\n")).toEqual([
      { kind: "heading", level: 1, text: "One" },
      { kind: "paragraph", text: "A line that wraps." },
      { kind: "list", ordered: false, items: ["a", "b"] },
      { kind: "list", ordered: true, items: ["c"] },
      { kind: "quote", text: "said" },
      { kind: "rule" },
    ]);
  });

  it("keeps a fenced block whole, and reads nothing inside it", () => {
    expect(blocksOf("```go\n# not a heading\n\n- not a list\n```\n")).toEqual([{ kind: "code", text: "# not a heading\n\n- not a list" }]);
  });

  it("keeps a table's columns as they stand", () => {
    expect(blocksOf("| a | b |\n|---|---|\n| 1 | 2 |\n")).toEqual([{ kind: "code", text: "| a | b |\n|---|---|\n| 1 | 2 |" }]);
  });

  it("closes a fence the text left open", () => {
    expect(blocksOf("```\nnever closed")).toEqual([{ kind: "code", text: "never closed" }]);
  });

  it("reads Windows line ends", () => {
    expect(blocksOf("# One\r\n\r\nTwo\r\n")).toEqual([
      { kind: "heading", level: 1, text: "One" },
      { kind: "paragraph", text: "Two" },
    ]);
  });

  it("reads nothing from nothing", () => {
    expect(blocksOf("")).toEqual([]);
  });
});

describe("a document on the page", () => {
  it("keeps headings below the page's own title", () => {
    show("# One\n\n## Two\n\n###### Six\n");
    expect(screen.getByRole("heading", { name: "One" }).tagName).toBe("H2");
    expect(screen.getByRole("heading", { name: "Two" }).tagName).toBe("H3");
    expect(screen.getByRole("heading", { name: "Six" }).tagName).toBe("H4");
  });

  it("leaves out a first heading the page's title already says", () => {
    show("# The title\n\nBody\n", "The title");
    expect(screen.queryByRole("heading")).toBeNull();
    expect(screen.getByText("Body")).toBeTruthy();
  });

  it("shows inline code and emphasis", () => {
    show("Run `make check` **now**.\n");
    expect(screen.getByText("make check").tagName).toBe("CODE");
    expect(screen.getByText("now").tagName).toBe("STRONG");
  });

  it("shows a link as words and an address, and follows neither", () => {
    const { container } = show("See [the site](https://example.com/page) first.\n");
    expect(container.querySelector("a")).toBeNull();
    expect(container.textContent).toContain("the site");
    expect(container.textContent).toContain("https://example.com/page");
  });
});

// ATTACK: a document is text anyone with a commit wrote, so nothing in it may become part of the page.
describe("markup in a document", () => {
  it.each([
    ["a paragraph", markup],
    ["a heading", `# ${markup}`],
    ["a list", `- ${markup}`],
    ["a quote", `> ${markup}`],
    ["a fence", "```html\n" + markup + "\n```"],
    ["a table", `| ${markup} |`],
    ["inline code", "`" + markup + "`"],
    ["emphasis", `**${markup}**`],
  ])("stays text in %s", (_name, body) => {
    const { container } = show(body);
    expect(container.querySelector("img, script")).toBeNull();
    expect(container.textContent).toContain("<img src=x onerror=alert(1)>");
  });

  it.each([
    ["a script address", "[click me](javascript:alert(1))"],
    ["a data address", "[click me](data:text/html;base64,PHNjcmlwdD4=)"],
    ["a mixed case address", "[click me](JaVaScRiPt:alert(1))"],
    ["an address with a control character", "[click me](java\tscript:alert(1))"],
    ["a reference link", "[click me][1]\n\n[1]: javascript:alert(1)"],
    ["a bare address", "javascript:alert(1)"],
  ])("makes no link of %s", (_name, body) => {
    const { container } = show(body);
    expect(container.querySelector("a")).toBeNull();
  });

  it("loads no picture, whatever it names", () => {
    const { container } = show("![a pixel](http://attacker.example/pixel.png)\n");
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toContain("http://attacker.example/pixel.png");
  });

  it.each(["iframe", "svg", "object", "embed", "style", "link", "meta", "form", "base"])("builds no %s", (tag) => {
    const { container } = show(`<${tag} src="http://attacker.example" onload="alert(1)"></${tag}>\n`);
    expect(container.querySelector(tag)).toBeNull();
    expect(container.textContent).toContain(`<${tag}`);
  });

  it("gives text no way to reach an attribute", () => {
    const { container } = show(`# " onmouseover="alert(1)\n\n\`" onclick="alert(1)\`\n`);
    for (const element of container.querySelectorAll("*")) {
      expect(element.getAttributeNames().filter((name) => name.startsWith("on"))).toEqual([]);
    }
  });

  it("survives text that ends where a pattern would not", () => {
    for (const body of ["**", "`", "[", "[a](", "![", "```", "- ", "> ", "#", "####### seven"]) {
      expect(() => show(body)).not.toThrow();
    }
  });
});
