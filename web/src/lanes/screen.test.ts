import { describe, expect, it } from "vitest";

import { parseScreen } from "./screen";

const esc = "\x1b";

describe("a pane's screen", () => {
  it("keeps plain text as it is, lines and all", () => {
    expect(parseScreen("one\n  two\n")).toEqual([{ text: "one\n  two\n", className: "" }]);
  });

  it("turns colour and emphasis into the palette's classes", () => {
    const runs = parseScreen(`${esc}[1mbold${esc}[22m ${esc}[31mred${esc}[39m ${esc}[4;34mlink${esc}[0m ${esc}[2mquiet${esc}[0m ${esc}[3mslant${esc}[m`);
    expect(runs).toEqual([
      { text: "bold", className: "sgr-bold" },
      { text: " ", className: "" },
      { text: "red", className: "sgr-refuse" },
      { text: " ", className: "" },
      { text: "link", className: "sgr-underline sgr-marker" },
      { text: " ", className: "" },
      { text: "quiet", className: "sgr-muted" },
      { text: " ", className: "" },
      { text: "slant", className: "sgr-italic" },
    ]);
  });

  it("swaps the colours of inverse text", () => {
    expect(parseScreen(`${esc}[7m> Yes${esc}[27m No`)).toEqual([
      { text: "> Yes", className: "sgr-inverse" },
      { text: " No", className: "" },
    ]);
  });

  it("reads 256 colours and true colour by their hue", () => {
    expect(parseScreen(`${esc}[38;5;9mbright red${esc}[38;5;244mgrey${esc}[38;2;40;90;220mblue${esc}[38;2;250;250;250mwhite${esc}[0m`)).toEqual([
      { text: "bright red", className: "sgr-refuse" },
      { text: "grey", className: "sgr-muted" },
      { text: "blue", className: "sgr-marker" },
      { text: "white", className: "" },
    ]);
    expect(parseScreen(`${esc}[38:2::200:30:30mred${esc}[0m`)).toEqual([{ text: "red", className: "sgr-refuse" }]);
  });

  it("leaves backgrounds to the page", () => {
    expect(parseScreen(`${esc}[44;48;5;17;48;2;1;2;3mtext${esc}[49m`)).toEqual([{ text: "text", className: "" }]);
  });

  it("drops every other escape, so none reaches the page as text", () => {
    const hostile = `a${esc}[2J${esc}[Hb${esc}]8;;https://example.com${esc}\\link${esc}]8;;${esc}\\c${esc}]0;title\x07d${esc}(Be\x07\x00f`;
    expect(parseScreen(hostile).map((run) => run.text).join("")).toBe("ablinkcdef");
  });

  it("carries markup as text", () => {
    expect(parseScreen("<img src=x onerror=alert(1)>")).toEqual([{ text: "<img src=x onerror=alert(1)>", className: "" }]);
  });
});
