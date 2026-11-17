import type { ReactNode } from "react";

import { Icon } from "../ui/Icon";

// One piece of the statement: a paragraph, or the items of a list.
type Block = { kind: "paragraph"; text: string } | { kind: "list"; items: string[] };

const marker = "- ";

// blocksOf reads the statement the way `ropeshift worker risk` prints it: a blank line ends a paragraph, and a run of
// lines that begin with "- " is a list. The text is plain, and nothing else in it has a meaning.
export function blocksOf(text: string): Block[] {
  const blocks: Block[] = [];
  for (const chunk of text.split(/\n\s*\n/)) {
    let paragraph: string[] = [];
    const flush = () => {
      if (paragraph.length > 0) {
        blocks.push({ kind: "paragraph", text: paragraph.join(" ") });
        paragraph = [];
      }
    };
    for (const raw of chunk.split("\n")) {
      const line = raw.trim();
      if (line === "") {
        continue;
      }
      if (!line.startsWith(marker)) {
        paragraph.push(line);
        continue;
      }
      flush();
      const item = line.slice(marker.length).trim();
      const last = blocks.at(-1);
      if (last?.kind === "list") {
        last.items.push(item);
      } else {
        blocks.push({ kind: "list", items: [item] });
      }
    }
    flush();
  }
  return blocks;
}

// sourceLabel names a page by its host, without a leading www.
export function sourceLabel(address: string): string {
  return new URL(address).host.replace(/^www\./, "");
}

// webAddress is the address an item names, or null for an item that is anything else, so only a web page becomes a link.
function webAddress(item: string): string | null {
  try {
    const url = new URL(item);
    return url.protocol === "https:" || url.protocol === "http:" ? url.href : null;
  } catch {
    return null;
  }
}

function Item({ item }: { item: string }): ReactNode {
  const address = webAddress(item);
  if (address === null) {
    return item;
  }
  return (
    <a href={item} target="_blank" rel="noreferrer noopener">
      {sourceLabel(address)}
      <span className="visually-hidden"> (opens in a new tab)</span>
      <Icon name="external" size="sm" />
    </a>
  );
}

// RiskStatement shows the stated risk as its paragraphs and, for the pages it asks a person to read, a list of links
// that open in a new tab.
export function RiskStatement({ text }: { text: string }) {
  return (
    <div className="prose small risk-statement">
      {blocksOf(text).map((block, index) =>
        block.kind === "paragraph" ? (
          <p key={index}>{block.text}</p>
        ) : (
          <ul key={index}>
            {block.items.map((item, at) => (
              <li key={at}>
                <Item item={item} />
              </li>
            ))}
          </ul>
        ),
      )}
    </div>
  );
}
