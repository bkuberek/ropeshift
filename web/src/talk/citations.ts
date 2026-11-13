import { addressOf } from "../app/route";

// What the assistant's text may hold, and nothing more: paragraphs, inline code, fenced code blocks, lists,
// and citations of records and of code. Everything else, HTML included, stays the text it was written as,
// because what an agent wrote is data and never markup (FR-CHAT-2).

export type RecordKind = "work_order" | "lane" | "plan" | "contract" | "requirement" | "decision" | "question" | "document" | "repository" | "approval";

export const recordKinds: RecordKind[] = ["work_order", "lane", "plan", "contract", "requirement", "decision", "question", "document", "repository", "approval"];

export type Inline =
  | { type: "text"; text: string }
  | { type: "code"; text: string }
  | { type: "record"; kind: RecordKind; id: string }
  | { type: "code-ref"; repository: string; path: string; line: number; commit: string };

export type Block =
  | { type: "paragraph"; inlines: Inline[] }
  | { type: "list"; ordered: boolean; items: Inline[][] }
  | { type: "fence"; language: string; text: string };

// An ID a citation names: characters an ID holds, so a citation never carries markup or a space.
const idPattern = "[A-Za-z0-9_.:@-]{1,128}";
const kindPattern = recordKinds.join("|");

const recordCitation = new RegExp(`^\\[\\[(${kindPattern}):(${idPattern})\\]\\]`);
const codeCitation = /^\[\[code:([A-Za-z0-9_.-]{1,64}):([^\s\]#]{1,256})#L(\d{1,7})@([0-9a-f]{4,64})\]\]/;

// parseInlines reads a line's inline code and citations. A citation inside inline code stays code.
export function parseInlines(text: string): Inline[] {
  const out: Inline[] = [];
  let plain = "";
  const flush = () => {
    if (plain) {
      out.push({ type: "text", text: plain });
      plain = "";
    }
  };
  let at = 0;
  while (at < text.length) {
    const rest = text.slice(at);
    if (rest.startsWith("`")) {
      const close = rest.indexOf("`", 1);
      if (close > 1) {
        flush();
        out.push({ type: "code", text: rest.slice(1, close) });
        at += close + 1;
        continue;
      }
    }
    if (rest.startsWith("[[")) {
      const code = codeCitation.exec(rest);
      if (code) {
        flush();
        out.push({ type: "code-ref", repository: code[1] ?? "", path: code[2] ?? "", line: Number(code[3]), commit: code[4] ?? "" });
        at += code[0].length;
        continue;
      }
      const record = recordCitation.exec(rest);
      if (record) {
        flush();
        out.push({ type: "record", kind: record[1] as RecordKind, id: record[2] ?? "" });
        at += record[0].length;
        continue;
      }
    }
    plain += text[at];
    at += 1;
  }
  flush();
  return out;
}

const bullet = /^\s{0,3}[-*]\s+(.*)$/;
const numbered = /^\s{0,3}\d{1,3}[.)]\s+(.*)$/;
const fence = /^\s{0,3}```\s*([A-Za-z0-9_+.-]*)\s*$/;

// parseBlocks reads a message into paragraphs, lists, and fenced code. A fence the message left open runs to
// its end.
export function parseBlocks(text: string): Block[] {
  const blocks: Block[] = [];
  const lines = text.replaceAll("\r\n", "\n").split("\n");
  let paragraph: string[] = [];
  let list: { ordered: boolean; items: string[] } | null = null;
  const endParagraph = () => {
    if (paragraph.length) {
      blocks.push({ type: "paragraph", inlines: parseInlines(paragraph.join("\n")) });
      paragraph = [];
    }
  };
  const endList = () => {
    if (list) {
      blocks.push({ type: "list", ordered: list.ordered, items: list.items.map(parseInlines) });
      list = null;
    }
  };
  for (let at = 0; at < lines.length; at += 1) {
    const line = lines[at] ?? "";
    const opened = fence.exec(line);
    if (opened) {
      endParagraph();
      endList();
      const body: string[] = [];
      at += 1;
      while (at < lines.length && !fence.test(lines[at] ?? "") ) {
        body.push(lines[at] ?? "");
        at += 1;
      }
      blocks.push({ type: "fence", language: opened[1] ?? "", text: body.join("\n") });
      continue;
    }
    if (line.trim() === "") {
      endParagraph();
      endList();
      continue;
    }
    const item = bullet.exec(line) ?? numbered.exec(line);
    if (item) {
      endParagraph();
      const ordered = numbered.test(line) && !bullet.test(line);
      if (list && list.ordered !== ordered) {
        endList();
      }
      list = list ?? { ordered, items: [] };
      list.items.push(item[1] ?? "");
      continue;
    }
    endList();
    paragraph.push(line);
  }
  endParagraph();
  endList();
  return blocks;
}

// The view each kind of record opens, by its address. A contract has no view of its own, so it opens the
// plans, where each plan lists its contracts. The ID is encoded into the query by `addressOf`, never joined.
export function addressOfCitation({ kind, id }: { kind: RecordKind; id: string }): string {
  switch (kind) {
    case "work_order":
      return addressOf("/work-order", { id });
    case "lane":
      return addressOf("/lane", { id });
    case "plan":
      return addressOf("/plans", { plan: id });
    case "contract":
      return "/plans";
    case "requirement":
      return addressOf("/documents", { kind: "requirements", key: id });
    case "decision":
      return addressOf("/decisions", { id });
    case "question":
      return addressOf("/questions", { id });
    case "document":
      return addressOf("/documents", { id });
    case "repository":
      return addressOf("/repositories", { repo: id });
    case "approval":
      return addressOf("/needs-you", { item: `approval:${id}` });
  }
}

// codeLocation is the location a code citation copies.
export function codeLocation(ref: { repository: string; path: string; line: number; commit: string }): string {
  return `${ref.repository}/${ref.path}:${ref.line} @${ref.commit.slice(0, 7)}`;
}
