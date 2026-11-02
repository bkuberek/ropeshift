import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

const tokens = readFileSync("src/styles/tokens.css", "utf8");

// sections are the two themes as the file declares them, so a test reads what ships.
function palette(block: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [, name, value] of block.matchAll(/--(\w+):\s*(#[0-9a-f]{6});/g)) {
    if (name && value) {
      out[name] = value;
    }
  }
  return out;
}

const light = palette(tokens.slice(0, tokens.indexOf("@media (min-width: 40rem)")));
const dark = palette(tokens.slice(tokens.indexOf(":root[data-theme=\"dark\"]")));
const darkBySystem = palette(tokens.slice(tokens.indexOf("prefers-color-scheme: dark"), tokens.indexOf(":root[data-theme=\"dark\"]")));

function luminance(hex: string): number {
  const channels = [1, 3, 5].map((at) => Number.parseInt(hex.slice(at, at + 2), 16) / 255);
  const [r, g, b] = channels.map((value) => (value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4)) as [number, number, number];
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number];
  return (hi + 0.05) / (lo + 0.05);
}

const text = ["ink", "muted", "marker", "refuse"];
const surfaces = ["ground", "lane"];

describe.each([
  ["light", light],
  ["dark", dark],
  ["dark by the system", darkBySystem],
])("the %s theme", (_name, theme) => {
  it("declares every color", () => {
    for (const name of [...text, ...surfaces, "hemp"]) {
      expect(theme[name], name).toMatch(/^#/);
    }
  });

  it.each(text.flatMap((foreground) => surfaces.map((surface) => [foreground, surface] as const)))("%s on %s meets AA for text", (foreground, surface) => {
    expect(contrast(theme[foreground] ?? "", theme[surface] ?? "")).toBeGreaterThanOrEqual(4.5);
  });

  it("gives a filled button's text AA", () => {
    expect(contrast(theme.ground ?? "", theme.ink ?? "")).toBeGreaterThanOrEqual(4.5);
    expect(contrast(theme.ground ?? "", theme.marker ?? "")).toBeGreaterThanOrEqual(4.5);
  });

  it("draws the strand and the focus ring where a person can see them", () => {
    for (const surface of surfaces) {
      expect(contrast(theme.hemp ?? "", theme[surface] ?? "")).toBeGreaterThanOrEqual(3);
      expect(contrast(theme.marker ?? "", theme[surface] ?? "")).toBeGreaterThanOrEqual(3);
    }
  });
});

describe("the two dark declarations", () => {
  it("are one palette", () => {
    expect(darkBySystem).toEqual(dark);
  });
});
