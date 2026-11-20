import { describe, expect, it } from "vitest";

import type { NeedItem, QuestionItem } from "../api/types";
import { fullHeadingOf, headingOf, questionTitle, titleLimit } from "./kinds";

const question = (text: string, id = "q-1"): QuestionItem => ({ id, text });

const batchOf = (...texts: string[]): NeedItem =>
  ({
    kind: "questions",
    id: "batch-1",
    since: 0,
    batch: { batch: { id: "batch-1", status: "open", opened_at: 0, closes_at: 0 }, items: texts.map((text, index) => question(text, `q-${index}`)) },
  }) as NeedItem;

describe("the title of a question", () => {
  it("is the question's own text when it is short", () => {
    expect(questionTitle("Should the export keep the old column names?")).toBe("Should the export keep the old column names?");
  });

  it("falls back to A question when the text is empty or only space", () => {
    expect(questionTitle("")).toBe("A question");
    expect(questionTitle(" \n\t ")).toBe("A question");
  });

  it("reads on one line", () => {
    expect(questionTitle("Which   store\nholds the\tindex?")).toBe("Which store holds the index?");
  });

  it("is cut at a word boundary with an ellipsis when it is long", () => {
    const text = "Should the importer keep the original column names when the source file has duplicates, or rename each duplicate with a numeric suffix?";
    const title = questionTitle(text);
    expect(title.length).toBeLessThanOrEqual(titleLimit + 1);
    expect(title.endsWith("…")).toBe(true);
    const kept = title.slice(0, -1);
    expect(text.startsWith(kept)).toBe(true);
    expect(text[kept.length]).toBe(" ");
    expect(kept.endsWith(" ")).toBe(false);
  });

  it("leaves no dangling punctuation before the ellipsis", () => {
    const text = `${"x".repeat(85)}, then more words follow here`;
    expect(questionTitle(text)).toBe(`${"x".repeat(85)}…`);
  });

  it("is cut inside a word when there is no boundary to cut at", () => {
    const title = questionTitle("x".repeat(500));
    expect(title).toBe(`${"x".repeat(titleLimit)}…`);
  });

  it("is the heading of a batch of one question, and the whole text stays available", () => {
    const text = `${"Which of the two migration orders should run first? ".repeat(4)}Say why.`;
    const item = batchOf(text);
    expect(headingOf(item)).toBe(questionTitle(text));
    expect(headingOf(item)).not.toBe(text);
    expect(fullHeadingOf(item)).toBe(text.replace(/\s+/g, " ").trim());
  });

  it("is still a count for a batch of several questions", () => {
    const item = batchOf("First?", "Second?");
    expect(headingOf(item)).toBe("2 questions");
    expect(fullHeadingOf(item)).toBe("2 questions");
  });
});

// A lane titles its question in a few words, which the list shows in place of the text, and the full text stays one
// hover away.
describe("a question its asker titled", () => {
  const titled: NeedItem = {
    kind: "questions",
    id: "batch-1",
    since: 0,
    batch: {
      batch: { id: "batch-1", status: "open", opened_at: 0, closes_at: 0 },
      items: [{ id: "q-1", text: "How often does a call to the forge retry before it gives up?", title: "Forge retries" }],
    },
  } as NeedItem;

  it("is headed by its title, with its text whole beside it", () => {
    expect(headingOf(titled)).toBe("Forge retries");
    expect(fullHeadingOf(titled)).toBe("How often does a call to the forge retry before it gives up?");
  });
});
