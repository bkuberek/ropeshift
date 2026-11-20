import { describe, expect, it } from "vitest";

import { golden } from "../testing";
import { buildRequest, creating, nameProblem, newRow, rowRemote, urlProblem } from "./projectRemoteRequest";
import type { Draft, Row } from "./projectRemoteRequest";
import { plainText } from "./untrustedText";

const draft = (over: Partial<Draft> = {}): Draft => ({
  question: "one",
  name: "",
  named: true,
  project: "",
  firstCommit: true,
  mode: "none",
  url: "",
  owner: "ana",
  visibility: "private",
  rows: [newRow()],
  remotes: true,
  ...over,
});

const row = (over: Partial<Row>): Row => ({ ...newRow(), ...over });

describe("the request a draft makes", () => {
  it("is refused until the first question is answered", () => {
    expect(buildRequest(draft({ question: "" }), "/srv/a").problem?.field).toBe("question");
  });

  it("names the shape the answer writes", () => {
    expect(buildRequest(draft({ question: "one" }), "/srv/a").request?.shape).toBe("monorepo");
    expect(buildRequest(draft({ question: "several", rows: [row({ name: "api" })] }), "/srv/a").request?.shape).toBe("workspace");
    expect(buildRequest(draft({ question: "elsewhere", project: " tidewater " }), "/srv/a").request).toEqual({ shape: "repository", path: "/srv/a", first_commit: true, project: "tidewater" });
  });

  it("names the project for a monorepo and a workspace, trimmed, and sends no name when it is empty", () => {
    expect(buildRequest(draft({ name: " tidewater " }), "/srv/a").request).toEqual({ shape: "monorepo", path: "/srv/a", first_commit: true, name: "tidewater" });
    expect(buildRequest(draft({ question: "several", name: "tidewater", rows: [row({ name: "api" })] }), "/srv/a").request?.name).toBe("tidewater");
    expect(buildRequest(draft({ name: "  " }), "/srv/a").request).toEqual({ shape: "monorepo", path: "/srv/a", first_commit: true });
  });

  it("sends no name for a code repository of a project planned elsewhere, which names a project of its own", () => {
    expect(buildRequest(draft({ question: "elsewhere", name: "tidewater", project: "acme" }), "/srv/a").request).toEqual({ shape: "repository", path: "/srv/a", first_commit: true, project: "acme" });
  });

  it("refuses a name that is no project name, and one a worker on an older protocol would drop", () => {
    expect(buildRequest(draft({ name: "Tide Water" }), "/srv/a").problem?.field).toBe("name");
    expect(buildRequest(draft({ name: "tidewater", named: false }), "/srv/a").problem).toEqual({
      field: "name",
      message: "This worker cannot take a project name. Clear the field, or choose a worker on plane protocol 7 or later.",
    });
    expect(buildRequest(draft({ name: "", named: false }), "/srv/a").request).toEqual({ shape: "monorepo", path: "/srv/a", first_commit: true });
  });

  it("sends no remote while the first commit is unchecked or the worker is on an older protocol, whatever was chosen", () => {
    expect(buildRequest(draft({ mode: "create", firstCommit: false }), "/srv/a").request).toEqual({ shape: "monorepo", path: "/srv/a", first_commit: false });
    expect(buildRequest(draft({ mode: "url", url: "git@a:b/c.git", remotes: false }), "/srv/a").request).toEqual({ shape: "monorepo", path: "/srv/a", first_commit: true });
  });

  it("makes every owner explicit and private the default visibility", () => {
    const built = buildRequest(draft({ question: "several", mode: "create", rows: [row({ name: "api" }), row({ name: "web", owner: "acme" }), row({ name: "old", existing: true, remote: "git@a:b/old.git" })] }), "/srv/a");
    expect(built.request).toEqual({
      shape: "workspace",
      path: "/srv/a",
      first_commit: true,
      remote: { create: { forge: "github", owner: "ana", visibility: "private" } },
      repositories: [
        { name: "api", new: true, create: { forge: "github", owner: "ana", visibility: "private" } },
        { name: "web", new: true, create: { forge: "github", owner: "acme", visibility: "private" } },
        { name: "old", remote: "git@a:b/old.git" },
      ],
    });
    expect(creating(draft({ question: "several", mode: "create", rows: [row({ name: "api" }), row({ name: "old", existing: true })] }))).toBe(2);
  });

  it("refuses a name that is empty or repeated, and a URL the daemon would refuse", () => {
    expect(buildRequest(draft({ question: "several", rows: [row({ name: " " })] }), "/srv/a").problem?.field).toBe("rows");
    expect(buildRequest(draft({ question: "several", rows: [row({ name: "api" }), row({ name: "API" })] }), "/srv/a").problem?.field).toBe("rows");
    expect(buildRequest(draft({ mode: "url", url: "" }), "/srv/a").problem?.field).toBe("remote");
    expect(buildRequest(draft({ mode: "create", owner: "" }), "/srv/a").problem?.message).toBe("Choose an owner.");
    expect(urlProblem("a b")).not.toBe("");
    expect(urlProblem("-oProxyCommand=x")).not.toBe("");
    expect(urlProblem("https://example.com/a.git")).toBe("");
  });

  it("reads Same as above as a URL of its own when each repository adds a URL", () => {
    expect(rowRemote(row({ choice: "same" }), "url")).toBe("url");
    expect(rowRemote(row({ choice: "same" }), "create")).toBe("same");
    expect(rowRemote(row({ choice: "none" }), "create")).toBe("none");
    expect(rowRemote(row({ existing: true }), "create")).toBe("none");
  });
});

describe("plainText", () => {
  it("drops escape sequences and control characters and keeps newline and tab", () => {
    expect(plainText("\u001b[31mred\u001b[0m\u001b]0;title\u0007 ok\r\nline\ttab\u0000\u0008\u007f\u009b2Jend")).toBe("red ok\nline\ttabend");
    expect(plainText(undefined)).toBe("");
  });

  it("leaves markup and links as the text they are", () => {
    expect(plainText("<b>x</b> [a](https://e.example/)")).toBe("<b>x</b> [a](https://e.example/)");
  });

  it("reads a blank braille character as a space", () => {
    expect(plainText("a\u2800b")).toBe("a b");
  });

  it("drops bidirectional overrides, isolates, marks, and zero-width characters", () => {
    expect(plainText("a\u202eb\u202cc\u2066d\u2069e\u200ef\u200fg\u061ch\u200bi\u200cj\u200dk\u2060l\ufeffm")).toBe("abcdefghijklm");
    expect(plainText("Réessayer — 日本語 😀 \u00a0x")).toBe("Réessayer — 日本語 😀 \u00a0x");
  });

  // internal/ledger writes the characters HiddenRune names, which the command line drops, and every one of them is
  // dropped here, newline and tab apart, and no other.
  it("drops the characters the command line drops and no others", () => {
    const { ranges } = golden<{ ranges: [number, number][] }>("hidden-runes");
    const hidden = (code: number) => ranges.some(([first, last]) => code >= first && code <= last);
    const mismatches: string[] = [];
    for (let code = 0; code <= 0x10ffff; code++) {
      if (code >= 0xd800 && code <= 0xdfff) {
        continue;
      }
      const text = String.fromCodePoint(code);
      const kept = code === 0x0a || code === 0x09 || !hidden(code);
      if ((plainText(text) === text) !== kept) {
        mismatches.push(`U+${code.toString(16).toUpperCase().padStart(4, "0")}`);
      }
    }
    expect(mismatches).toEqual([]);
  }, 60_000);
});

describe("the name a project may have", () => {
  it("is lowercase letters and digits in words joined by single hyphens, 63 characters at most", () => {
    for (const good of ["a", "tidewater", "harbor-api-2", "a".repeat(63)]) {
      expect(nameProblem(good)).toBe("");
    }
    for (const bad of ["Harbor", "har bor", "-harbor", "harbor-", "a--b", "a_b", "a".repeat(64)]) {
      expect(nameProblem(bad)).not.toBe("");
    }
  });
});
