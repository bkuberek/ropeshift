import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

import { contrast, palette, themes } from "./palette";

const tokens = readFileSync("src/styles/tokens.css", "utf8");
const { light, dark, darkBySystem } = themes(tokens);

// Text needs 4.5:1, and a control's boundary or a meaningful graphic 3:1 (WCAG 2.2, 1.4.3 and 1.4.11).
const text = 4.5;
const boundary = 3;

const tones = ["active", "success", "warning", "danger"];
const targets = ["target-assistant", "target-orchestrator", "target-planning", "target-lane"];

// The pairs the design names, each with what it needs. Every one holds in both themes.
const pairs: [string, string, number][] = [
  ["text", "bg", text],
  ["text", "surface", text],
  ["text", "surface-2", text],
  ["text", "surface-3", text],
  ["text", "hover", text],
  ["text", "selected", text],
  ["text-2", "bg", text],
  ["text-2", "surface", text],
  ["text-2", "surface-2", text],
  ["text-2", "surface-3", text],
  ["text-2", "hover", text],
  ["text-2", "selected", text],
  ["text-3", "bg", text],
  ["text-3", "surface", text],
  ["text-3", "surface-2", text],
  ["text-3", "surface-3", text],
  ["text-3", "hover", text],
  ["text-3", "selected", text],
  ["on-ink", "ink", text],
  ["on-danger", "danger", text],
  ["border-strong", "surface", boundary],
  ["border-strong", "surface-2", boundary],
  ["border-strong", "bg", boundary],
  ["focus", "bg", boundary],
  ["focus", "surface", boundary],
  ["focus", "surface-2", boundary],
  ["neutral", "neutral-bg", text],
  ...tones.map((tone): [string, string, number] => [tone, `${tone}-bg`, text]),
  ...targets.map((target): [string, string, number] => [target, `${target}-bg`, text]),
  // A tone or a target colours words and edges on a plain surface too: a warning's value in a table, a
  // condition chip's outline, a target chip's edge.
  ...[...tones, ...targets, "neutral"].flatMap((foreground): [string, string, number][] => [
    [foreground, "surface", text],
    [foreground, "bg", text],
    [foreground, "surface-2", text],
  ]),
];

describe.each([
  ["light", light],
  ["dark", dark],
  ["dark by the system", darkBySystem],
])("the %s theme", (_name, theme) => {
  it("declares every color the pairs name", () => {
    for (const [foreground, background] of pairs) {
      expect(theme[foreground], foreground).toMatch(/^#[0-9a-f]{6}$/);
      expect(theme[background], background).toMatch(/^#[0-9a-f]{6}$/);
    }
  });

  it.each(pairs)("%s on %s reaches %d:1", (foreground, background, needs) => {
    expect(contrast(theme[foreground] ?? "", theme[background] ?? "")).toBeGreaterThanOrEqual(needs);
  });

  it("keeps the terminal pane dark in both themes, with its text readable on it", () => {
    expect(theme["terminal-bg"]).toBe("#0b0d10");
    expect(contrast(theme["terminal-text"] ?? "", theme["terminal-bg"] ?? "")).toBeGreaterThanOrEqual(text);
  });
});

describe("the two dark declarations", () => {
  it("are one palette", () => {
    expect(darkBySystem).toEqual(dark);
  });
});

describe("the measurement", () => {
  // The design's own figures, which the formula must reproduce.
  it.each([
    ["#15181c", "#f5f6f8", 16.47],
    ["#e7e9ec", "#0c0e11", 15.89],
    ["#7a828e", "#ffffff", 3.88],
    ["#ffffff", "#bf2a2f", 5.85],
  ])("%s on %s is %d:1", (foreground, background, ratio) => {
    expect(contrast(foreground, background)).toBeCloseTo(ratio, 2);
  });

  it("reads a theme's colors as the file declares them", () => {
    expect(palette("--text: #15181c;\n  --scrim: rgb(0 0 0 / 0.6);")).toEqual({ text: "#15181c" });
  });
});

describe("the website", () => {
  it("lends the application none of its typefaces or colors", () => {
    for (const name of ["Big Shoulders", "Source Serif", "--ground", "--hemp", "--marker", "#14302f", "#12302e"]) {
      expect(tokens).not.toContain(name);
    }
  });
});
