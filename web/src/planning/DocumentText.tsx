import { Fragment } from "react";
import type { ReactNode } from "react";

// A document is text anyone with a commit wrote, so nothing in it becomes part of the page. This reads
// the shape of its lines and hands React plain strings, which React shows as text whatever they hold.
// It makes no link and loads no picture, so a document cannot send a person anywhere or tell anyone it
// was read: an address stays visible as words.

export type Block =
  | { kind: "heading"; level: number; text: string }
  | { kind: "paragraph"; text: string }
  | { kind: "list"; ordered: boolean; items: string[] }
  | { kind: "quote"; text: string }
  | { kind: "code"; text: string }
  | { kind: "rule" };

const fence = /^\s*(```|~~~)/;
const heading = /^(#{1,6})\s+(.*?)\s*#*\s*$/;
const bullet = /^\s*[-*+]\s+(.*)$/;
const numbered = /^\s*\d+[.)]\s+(.*)$/;
const quote = /^\s*>\s?(.*)$/;
const rule = /^\s*([-*_])(\s*\1){2,}\s*$/;
const table = /^\s*\|/;

function startsBlock(line: string): boolean {
  return [fence, heading, bullet, numbered, quote, rule, table].some((shape) => shape.test(line));
}

export function blocksOf(body: string): Block[] {
  const lines = body.replaceAll("\r\n", "\n").split("\n");
  const blocks: Block[] = [];
  let at = 0;
  // takeWhile gathers the run of lines a test accepts.
  const takeWhile = (accept: (line: string) => boolean): string[] => {
    const taken: string[] = [];
    for (let line = lines[at]; line !== undefined && accept(line); line = lines[at]) {
      taken.push(line);
      at += 1;
    }
    return taken;
  };
  while (at < lines.length) {
    const line = lines[at] ?? "";
    const shaped = heading.exec(line);
    if (line.trim() === "") {
      at += 1;
    } else if (fence.test(line)) {
      at += 1;
      const inside = takeWhile((candidate) => !fence.test(candidate));
      at += 1;
      blocks.push({ kind: "code", text: inside.join("\n") });
    } else if (shaped) {
      at += 1;
      blocks.push({ kind: "heading", level: shaped[1]?.length ?? 1, text: shaped[2] ?? "" });
    } else if (rule.test(line)) {
      at += 1;
      blocks.push({ kind: "rule" });
    } else if (table.test(line)) {
      blocks.push({ kind: "code", text: takeWhile((candidate) => table.test(candidate)).join("\n") });
    } else if (quote.test(line)) {
      const said = takeWhile((candidate) => quote.test(candidate));
      blocks.push({ kind: "quote", text: said.map((row) => quote.exec(row)?.[1] ?? "").join(" ").trim() });
    } else if (bullet.test(line) || numbered.test(line)) {
      const shape = bullet.test(line) ? bullet : numbered;
      const items = takeWhile((candidate) => shape.test(candidate)).map((row) => shape.exec(row)?.[1] ?? "");
      blocks.push({ kind: "list", ordered: shape === numbered, items });
    } else {
      const first = lines[at] ?? "";
      at += 1;
      const rest = takeWhile((candidate) => candidate.trim() !== "" && !startsBlock(candidate));
      blocks.push({ kind: "paragraph", text: [first, ...rest].map((row) => row.trim()).join(" ") });
    }
  }
  return blocks;
}

const inlineShape = /`([^`]+)`|\*\*([^*]+)\*\*|!?\[([^\]]*)\]\(([^)]*)\)/g;

// inline shows code and strong words, and a link as its words with its address after them.
function inline(text: string): ReactNode[] {
  const shown: ReactNode[] = [];
  let at = 0;
  for (const match of text.matchAll(inlineShape)) {
    shown.push(text.slice(at, match.index));
    const [whole, code, strong, words, address] = match;
    if (code !== undefined) {
      shown.push(<code key={match.index}>{code}</code>);
    } else if (strong !== undefined) {
      shown.push(<strong key={match.index}>{strong}</strong>);
    } else {
      shown.push(
        <Fragment key={match.index}>
          {words} <span className="address">({address})</span>
        </Fragment>,
      );
    }
    at = match.index + whole.length;
  }
  shown.push(text.slice(at));
  return shown;
}

// A document's headings stand under the page's own title, however it numbers them.
function Heading({ level, children }: { level: number; children: ReactNode }) {
  switch (level) {
    case 1:
      return <h2>{children}</h2>;
    case 2:
      return <h3>{children}</h3>;
    default:
      return <h4>{children}</h4>;
  }
}

function Shown({ block }: { block: Block }) {
  switch (block.kind) {
    case "heading":
      return <Heading level={block.level}>{inline(block.text)}</Heading>;
    case "paragraph":
      return <p>{inline(block.text)}</p>;
    case "quote":
      return <blockquote>{inline(block.text)}</blockquote>;
    case "code":
      return <pre tabIndex={0}>{block.text}</pre>;
    case "rule":
      return <hr />;
    case "list": {
      const items = block.items.map((item, index) => <li key={index}>{inline(item)}</li>);
      return block.ordered ? <ol>{items}</ol> : <ul>{items}</ul>;
    }
  }
}

export function DocumentText({ body, title }: { body: string; title: string }) {
  const blocks = blocksOf(body);
  const [first, ...rest] = blocks;
  const shown = first?.kind === "heading" && first.text === title ? rest : blocks;
  return (
    <div className="prose">
      {shown.map((block, index) => (
        <Shown key={index} block={block} />
      ))}
    </div>
  );
}
