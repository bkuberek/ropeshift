import type { ReactNode } from "react";

import { RecordChip } from "../ui/display";
import { Icon } from "../ui/Icon";
import type { IconName } from "../ui/Icon";
import { useToast } from "../ui/overlays";
import { addressOfCitation, codeLocation, parseBlocks } from "./citations";
import type { Inline, RecordKind } from "./citations";

const kindWords: Record<RecordKind, { name: string; icon: IconName }> = {
  work_order: { name: "Work order", icon: "stack" },
  lane: { name: "Lane", icon: "terminal" },
  plan: { name: "Plan", icon: "plan" },
  contract: { name: "Contract", icon: "review" },
  requirement: { name: "Requirement", icon: "trace" },
  decision: { name: "Decision", icon: "decision" },
  question: { name: "Question", icon: "question" },
  document: { name: "Document", icon: "doc" },
  repository: { name: "Repository", icon: "repo" },
  approval: { name: "Approval", icon: "inbox" },
};

// CodeChip is a citation of code: a location in a repository at a commit. No view shows code, so it copies the
// location where the member can open it.
function CodeChip({ repository, path, line, commit }: { repository: string; path: string; line: number; commit: string }) {
  const toast = useToast();
  const where = codeLocation({ repository, path, line, commit });
  const copy = () => {
    void Promise.resolve(navigator.clipboard?.writeText(where)).then(
      () => toast("success", `Copied ${where}.`),
      () => toast("warning", `The browser did not allow a copy. The location is ${where}.`),
    );
  };
  return (
    <button type="button" className="chip chip-code" aria-label={`Copy ${where}`} title="Copy this location" onClick={copy}>
      <Icon name="copy" size="sm" />
      <span className="truncate">{where}</span>
    </button>
  );
}

function Inlines({ inlines }: { inlines: Inline[] }): ReactNode {
  return inlines.map((part, at) => {
    switch (part.type) {
      case "text":
        return part.text;
      case "code":
        return <code key={at}>{part.text}</code>;
      case "record": {
        const words = kindWords[part.kind];
        return <RecordChip key={at} id={part.id} icon={words.icon} to={addressOfCitation(part)} label={`${words.name} ${part.id}`} />;
      }
      case "code-ref":
        return <CodeChip key={at} repository={part.repository} path={part.path} line={part.line} commit={part.commit} />;
    }
  });
}

// MessageText shows what an agent wrote: paragraphs, inline code, fenced code, and lists, with a record cited
// as a chip that opens its view and code cited as a chip that copies its location. Nothing else is markup:
// what the text holds, HTML included, stays text.
export function MessageText({ text }: { text: string }) {
  return (
    <>
      {parseBlocks(text).map((block, at) => {
        switch (block.type) {
          case "paragraph":
            return (
              <p key={at}>
                <Inlines inlines={block.inlines} />
              </p>
            );
          case "list": {
            const List = block.ordered ? "ol" : "ul";
            return (
              <List key={at}>
                {block.items.map((item, index) => (
                  <li key={index}>
                    <Inlines inlines={item} />
                  </li>
                ))}
              </List>
            );
          }
          case "fence":
            return (
              <pre key={at} className="code-block" tabIndex={0}>
                <code>{block.text}</code>
              </pre>
            );
        }
      })}
    </>
  );
}
