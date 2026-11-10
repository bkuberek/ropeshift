import { Fragment, useId } from "react";
import type { ReactNode } from "react";

// A document is text anyone with a commit wrote, so nothing in it becomes part of the page. This reads
// the shape of its lines and hands React plain strings, which React shows as text whatever they hold.
// It makes no link and loads no picture, so a document cannot send a person anywhere or tell anyone it
// was read: an address stays visible as words. A diagram stays the text it was written as.

// How a table's column aligns, as its separator row says: `:--`, `:-:`, `--:`, or none.
export type Align = "" | "left" | "center" | "right";

export type Block =
  | { kind: "heading"; level: number; text: string }
  | { kind: "paragraph"; text: string }
  | { kind: "list"; ordered: boolean; items: string[] }
  | { kind: "quote"; text: string }
  | { kind: "code"; language: string; text: string }
  | { kind: "table"; head: string[]; align: Align[]; rows: string[][] }
  | { kind: "rule" };

const fence = /^\s*(```|~~~)/;
// The language a fence names is the first word after it.
const fenceLanguage = /^\s*(?:```|~~~)\s*(\S*)/;
// A table's separator row: a run of dashes for each column, a colon at either end saying how it aligns.
const separator = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/;
const heading = /^(#{1,6})\s+(.*?)\s*#*\s*$/;
const bullet = /^\s*[-*+]\s+(.*)$/;
const numbered = /^\s*\d+[.)]\s+(.*)$/;
const quote = /^\s*>\s?(.*)$/;
const rule = /^\s*([-*_])(\s*\1){2,}\s*$/;
const table = /^\s*\|/;

function startsBlock(line: string): boolean {
  return [fence, heading, bullet, numbered, quote, rule, table].some((shape) => shape.test(line));
}

// cellsOf splits a table row on its pipes. A pipe a backslash escapes stays in its cell.
function cellsOf(line: string): string[] {
  let row = line.trim();
  if (row.startsWith("|")) {
    row = row.slice(1);
  }
  if (row.endsWith("|") && !row.endsWith("\\|")) {
    row = row.slice(0, -1);
  }
  const cells: string[] = [];
  let cell = "";
  for (let at = 0; at < row.length; at += 1) {
    const char = row[at];
    if (char === "\\" && row[at + 1] === "|") {
      cell += "|";
      at += 1;
    } else if (char === "|") {
      cells.push(cell.trim());
      cell = "";
    } else {
      cell += char;
    }
  }
  cells.push(cell.trim());
  return cells;
}

function alignOf(cell: string): Align {
  const left = cell.startsWith(":");
  const right = cell.endsWith(":");
  if (left && right) {
    return "center";
  }
  return right ? "right" : left ? "left" : "";
}

// tableOf reads a run of table lines as a table: a header, a separator with a column for each of the
// header's, and rows, each fitted to the header's columns. Lines with no separator are not a table.
function tableOf(lines: string[]): Block {
  const [header = "", divider = "", ...body] = lines;
  const head = cellsOf(header);
  const columns = cellsOf(divider);
  if (!separator.test(divider) || columns.length !== head.length) {
    return { kind: "code", language: "", text: lines.join("\n") };
  }
  const fit = (cells: string[]) => head.map((_, at) => cells[at] ?? "");
  return { kind: "table", head, align: columns.map(alignOf), rows: body.map((row) => fit(cellsOf(row))) };
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
      const language = fenceLanguage.exec(line)?.[1] ?? "";
      at += 1;
      const inside = takeWhile((candidate) => !fence.test(candidate));
      at += 1;
      blocks.push({ kind: "code", language, text: inside.join("\n") });
    } else if (shaped) {
      at += 1;
      blocks.push({ kind: "heading", level: shaped[1]?.length ?? 1, text: shaped[2] ?? "" });
    } else if (rule.test(line)) {
      at += 1;
      blocks.push({ kind: "rule" });
    } else if (table.test(line)) {
      blocks.push(tableOf(takeWhile((candidate) => table.test(candidate))));
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

// A fenced block says what it holds above it. A diagram shows as its source: drawing it would run what
// the document wrote.
function codeLabel(language: string): string {
  if (language.toLowerCase() === "mermaid") {
    return "Diagram (Mermaid source)";
  }
  return language ? `Code (${language})` : "Code";
}

// A block keeps its lines, so a long one scrolls inside it and not the page, and a keyboard reaches it.
function CodeBlock({ language, text }: { language: string; text: string }) {
  const label = useId();
  return (
    <figure className="code-block" aria-labelledby={label}>
      <figcaption className="code-label" id={label}>
        {codeLabel(language)}
      </figcaption>
      <pre tabIndex={0}>{text}</pre>
    </figure>
  );
}

const alignment = (align: Align | undefined) => (align ? { textAlign: align } : undefined);

// A table names itself by its header, and scrolls inside its region when it is wider than the page.
function Table({ head, align, rows }: { head: string[]; align: Align[]; rows: string[][] }) {
  const named = head.map((cell) => cell.replaceAll(/[`*]/g, "")).join(", ");
  return (
    <div className="table-scroll" role="region" aria-label={`Table of ${named}`} tabIndex={0}>
      <table>
        <thead>
          <tr>
            {head.map((cell, at) => (
              <th key={at} scope="col" style={alignment(align[at])}>
                {inline(cell)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, line) => (
            <tr key={line}>
              {row.map((cell, at) => (
                <td key={at} style={alignment(align[at])}>
                  {inline(cell)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
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
      return <CodeBlock language={block.language} text={block.text} />;
    case "table":
      return <Table head={block.head} align={block.align} rows={block.rows} />;
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
