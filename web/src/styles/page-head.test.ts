// @vitest-environment node
import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

import { themes } from "./palette";

const tokens = themes(readFileSync("src/styles/tokens.css", "utf8"));
const page = readFileSync("index.html", "utf8");

const meta = (name: string, media = "") =>
  [...page.matchAll(/<meta ([^>]*)\/?>/g)]
    .map((match) => match[1] ?? "")
    .find((attributes) => attributes.includes(`name="${name}"`) && (media === "" || attributes.includes(`media="${media}"`)));

const content = (attributes = "") => /content="([^"]*)"/.exec(attributes)?.[1] ?? "";

// The browser paints its own bar before the page's script runs, from the page's head, so the head names
// each theme's surface as the tokens define it, and lets the page reach a phone's rounded edges.
describe("the page's head", () => {
  it("names each theme's surface from the tokens as the browser's bar color", () => {
    expect(content(meta("theme-color", "(prefers-color-scheme: light)"))).toBe(tokens.light.surface);
    expect(content(meta("theme-color", "(prefers-color-scheme: dark)"))).toBe(tokens.dark.surface);
  });

  it("covers the whole screen, so the bars pad themselves by the safe area", () => {
    expect(content(meta("viewport"))).toContain("viewport-fit=cover");
  });
});
