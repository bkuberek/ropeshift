import { useId } from "react";
import type { KeyboardEvent, RefObject } from "react";

import { Link } from "../app/route";
import { onMac } from "../app/keys";
import { Button, Kbd } from "../ui/controls";
import { Refusal, TargetChip } from "../ui/display";
import type { Described } from "./target";

type Props = {
  described: Described;
  draft: string;
  onDraft: (text: string) => void;
  onSend: () => void;
  busy: boolean;
  // failure is the API's refusal, in its words. The draft stays.
  failure: string;
  field: RefObject<HTMLTextAreaElement | null>;
  // onKeystroke is a key in the box, which the assistant takes as the member starting to write.
  onKeystroke?: () => void;
};

// Composer is one message to one target: a line that names the target, a box with an edge in its color, and a
// button named for it. Mod+Enter sends, and Enter adds a line. A message is recorded with its target and grants
// nothing (FR-CHAT-9, FR-MSG-6). A target that takes no message now replaces the box with why.
export function Composer({ described, draft, onDraft, onSend, busy, failure, field, onKeystroke }: Props) {
  const mac = onMac();
  const hint = useId();
  if (described.ended) {
    const orchestrator = described.kind === "orchestrator";
    return (
      <div className="composer">
        <p className="small muted composer-ended">
          {described.ended}{" "}
          {orchestrator ? (
            <Link to="/workers" installation>
              Open Workers
            </Link>
          ) : null}
        </p>
      </div>
    );
  }
  const keys = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && (mac ? event.metaKey : event.ctrlKey)) {
      event.preventDefault();
      if (draft.trim() !== "" && !busy) {
        onSend();
      }
    }
  };
  return (
    <div className="composer">
      <div className="composer-to">
        <span>To</span>
        <TargetChip kind={described.kind} small />
        <span className="truncate">{described.to}</span>
      </div>
      <div className="composer-box">
        <label className="visually-hidden" htmlFor={`${hint}-box`}>
          {described.label}
        </label>
        <textarea
          id={`${hint}-box`}
          ref={field}
          rows={2}
          autoComplete="off"
          placeholder={described.placeholder}
          value={draft}
          aria-describedby={failure ? `${hint}-failure` : undefined}
          onChange={(event) => {
            onDraft(event.target.value);
            onKeystroke?.();
          }}
          onKeyDown={keys}
        />
        <div className="composer-bar">
          <span>
            <Kbd>{mac ? "⌘" : "Ctrl"}</Kbd> <Kbd>↵</Kbd> sends
          </span>
          <Button variant="primary" small className="send-btn" loading={busy} disabled={busy || draft.trim() === ""} onClick={onSend}>
            {described.send}
          </Button>
        </div>
      </div>
      {failure ? (
        <div id={`${hint}-failure`}>
          <Refusal>{failure}</Refusal>
        </div>
      ) : null}
    </div>
  );
}
