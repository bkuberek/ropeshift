import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { openPage } from "./opening";
import { serveViews } from "./testing";

const hostile = `<img src=x onerror=alert(1)><script>alert(2)</script>`;

// What no page may ever hold, whatever a record or a document says.
const built = "img, script, iframe, object, embed, svg, style, link, meta, base, form[action]";

const pages = [
  "/", "/roadmap", "/plans", "/requirements", "/specs", "/designs", "/documents", "/decisions", "/questions",
  "/in-progress", "/on-hold", "/stuck", "/completed", "/shop-floor", "/operations",
];

// Every link in the application is one it built itself, to one of its own addresses.
function links(): string[] {
  return [...document.querySelectorAll("a")].map((link) => link.getAttribute("href") ?? "");
}

// ATTACK: every view runs on what the API serves, which carries markup wherever an agent, a lane, or a
// repository writes text, so this holds each view to what an attacker could put there.
describe.each(pages)("the view at %s", (path) => {
  it("builds nothing from a record's text, and links only to itself", async () => {
    serveViews();
    openPage(path);
    await waitFor(() => expect(screen.getByRole("main").querySelector("[aria-busy]") ?? screen.queryByRole("heading", { name: "Needs you" })).not.toBeNull());
    expect(screen.getByRole("main").querySelectorAll(built)).toHaveLength(0);
    for (const href of links()) {
      expect(href, href).toMatch(/^(\/projects\/[a-z0-9-]+\/[a-z-]*(\?[^#]*)?|#main)$/);
    }
  });
});

describe("markup in what a person reads", () => {
  it("stays text in a contract", async () => {
    serveViews();
    openPage("/plans");
    await userEvent.click(await screen.findByRole("link", { name: "Write the docs" }));
    expect((await screen.findAllByText(/means\.$/))[0]?.textContent).toContain(hostile);
    expect(document.querySelector("main img, main script")).toBeNull();
  });

  it("stays text in a decision's rationale", async () => {
    serveViews();
    openPage("/decisions");
    expect((await screen.findByText(/^A count cannot know/)).textContent).toContain(hostile);
    expect(document.querySelector("main img, main script")).toBeNull();
  });

  it("stays text in a requirement", async () => {
    serveViews();
    openPage("/requirements");
    expect((await screen.findByText(/^A person can read why/)).textContent).toContain(hostile);
    expect(document.querySelector("main img, main script")).toBeNull();
  });

  it("stays text in a goal's measures", async () => {
    serveViews();
    openPage("/roadmap");
    expect((await screen.findByText(/^A person reads/)).textContent).toContain(hostile);
    expect(document.querySelector("main img, main script")).toBeNull();
  });

  it("stays text in a question", async () => {
    serveViews();
    openPage("/questions");
    expect((await screen.findByText(/^May the/)).textContent).toContain(hostile);
    expect(document.querySelector("main img, main script")).toBeNull();
  });

  it("stays text in what a lane said when it failed", async () => {
    serveViews();
    openPage("/completed");
    expect((await screen.findByText(/^It gave up on/)).textContent).toContain(hostile);
    expect(document.querySelector("main img, main script")).toBeNull();
  });

  it("stays text in what a source could not read", async () => {
    serveViews();
    openPage("/operations");
    expect((await screen.findByText("plans/bad.md")).parentElement?.textContent).toContain(hostile);
    expect(document.querySelector("main img, main script")).toBeNull();
  });

  it("stays text in a document, with no link and no picture, on the real read", async () => {
    serveViews();
    openPage("/document?path=docs%2Fspecs%2Fretry.md");
    await screen.findByRole("heading", { level: 3, name: "Links and pictures" });
    const text = document.querySelector(".prose");
    expect(text?.textContent).toContain(hostile);
    expect(text?.textContent).toContain("<script>alert(4)</script>");
    expect(text?.textContent).toContain("javascript:alert(5)");
    expect(text?.querySelectorAll("a, img, script, iframe, svg")).toHaveLength(0);
  });
});

// ATTACK: a link a stranger sends fills the address, so what it holds is one more thing that stays text.
describe("markup in an address", () => {
  const encoded = encodeURIComponent(hostile);

  it.each([
    ["a plan", `/plans?plan=${encoded}`, "No such plan"],
    ["a decision", `/decisions?id=${encoded}`, "No such decision"],
    ["a requirement", `/requirements?key=${encoded}`, "No such requirement"],
  ])("stays out of the page as %s", async (_name, path, heading) => {
    serveViews();
    openPage(path);
    expect(await screen.findByRole("heading", { level: 1, name: heading })).toBeTruthy();
    expect(screen.getByRole("main").querySelectorAll("img, script")).toHaveLength(0);
  });

  it("reaches the server only as a query value", async () => {
    const sent = serveViews();
    openPage(`/questions?category=${encoded}&x=${encoded}`);
    await waitFor(() => expect(sent.some((call) => call.path.startsWith("/v1/views/questions"))).toBe(true));
    const read = sent.find((call) => call.path.startsWith("/v1/views/questions"));
    expect(read?.path).toBe(`/v1/views/questions?category=${new URLSearchParams({ category: hostile }).toString().slice("category=".length)}`);
  });

  it("names no document path anywhere but the query", async () => {
    const sent = serveViews();
    openPage(`/document?path=${encodeURIComponent("../../etc/passwd")}`);
    await waitFor(() => expect(sent.some((call) => call.path.startsWith("/v1/documents/read"))).toBe(true));
    const read = sent.find((call) => call.path.startsWith("/v1/documents/read"));
    expect(read?.path).toBe("/v1/documents/read?path=..%2F..%2Fetc%2Fpasswd");
  });
});

describe("the source", () => {
  const source = (dir: string): string[] =>
    readdirSync(dir).flatMap((name: string) => {
      const path = join(dir, name);
      return statSync(path).isDirectory() ? source(path) : /\.tsx?$/.test(name) && !/\.test\.|testing|opening/.test(name) ? [path] : [];
    });

  it("never loads a frame, runs a string, or sets an address from data", () => {
    for (const path of source("src")) {
      expect(readFileSync(path, "utf8"), path).not.toMatch(/srcDoc|new Function\(|<iframe|<object|<embed|window\.open|location\.(href|assign|replace)/);
    }
  });

  it("makes no link but its own", () => {
    for (const path of source("src")) {
      const text = readFileSync(path, "utf8");
      for (const [, href] of text.matchAll(/<a\s[^>]*href=(\S+)/g)) {
        expect(`${path}: ${href}`).toMatch(/: ("#main">?|\{to\})$/);
      }
    }
  });
});
