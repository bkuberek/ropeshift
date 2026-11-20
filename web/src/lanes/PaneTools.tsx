import { useEffect, useRef } from "react";
import type { KeyboardEvent } from "react";

// selectContents selects an element's text and nothing else on the page, as select-all does in a field.
export function selectContents(element: HTMLElement | null): void {
  const selection = window.getSelection();
  if (!element || !selection) {
    return;
  }
  const range = document.createRange();
  range.selectNodeContents(element);
  selection.removeAllRanges();
  selection.addRange(range);
}

// copyText puts text on the clipboard, and says what happened in words a person reads.
export async function copyText(text: string, what: string): Promise<string> {
  try {
    await navigator.clipboard.writeText(text);
    const lines = text === "" ? 0 : text.split("\n").length;
    return `Copied ${what}: ${lines} ${lines === 1 ? "line" : "lines"}.`;
  } catch {
    return "The browser did not allow the copy. Select all, then copy with the keyboard.";
  }
}

// PaneTools is the toolbar above a pane's screen: copy the screen, select the pane's text, search it, and
// take the whole view with it.
export function PaneTools({
  onCopyScreen,
  onSelectAll,
  onSearch,
  searching,
  full,
  onFull,
  said,
}: {
  onCopyScreen: () => void;
  onSelectAll: () => void;
  onSearch: () => void;
  searching: boolean;
  full: boolean;
  onFull: () => void;
  said: string;
}) {
  return (
    <div className="pane-toolbar" role="toolbar" aria-label="Pane tools">
      <button className="btn btn-sm" type="button" onClick={onCopyScreen}>
        Copy screen
      </button>
      <button className="btn btn-sm" type="button" onClick={onSelectAll}>
        Select all
      </button>
      <button className="btn btn-sm" type="button" aria-pressed={searching} onClick={onSearch}>
        Search
      </button>
      <button className="btn btn-sm" type="button" aria-pressed={full} onClick={onFull}>
        Full view
      </button>
      <span className="pane-said small muted" aria-live="polite">
        {said}
      </span>
    </div>
  );
}

// FindBar searches the pane's text: Enter goes to the next match and Shift-Enter to the previous one, and
// Escape closes it, with focus back on the screen.
export function FindBar({
  words,
  matchCase,
  current,
  count,
  onWords,
  onMatchCase,
  onMove,
  onClose,
}: {
  words: string;
  matchCase: boolean;
  current: number;
  count: number;
  onWords: (words: string) => void;
  onMatchCase: (matchCase: boolean) => void;
  onMove: (by: number) => void;
  onClose: () => void;
}) {
  const field = useRef<HTMLInputElement>(null);
  useEffect(() => field.current?.focus(), []);
  const keyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Enter") {
      event.preventDefault();
      onMove(event.shiftKey ? -1 : 1);
    } else if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      onClose();
    }
  };
  return (
    <div className="pane-find" role="search" aria-label="Search the pane">
      <label className="field pane-find-field">
        <span className="visually-hidden">Find in the pane</span>
        <input ref={field} className="input input-sm" type="search" value={words} placeholder="Find in the pane" onChange={(event) => onWords(event.target.value)} onKeyDown={keyDown} />
      </label>
      <span className="pane-count small muted" aria-live="polite">
        {words === "" ? "" : count === 0 ? "No matches" : `${current + 1} of ${count}`}
      </span>
      <button className="btn btn-sm" type="button" aria-label="Previous match" disabled={count === 0} onClick={() => onMove(-1)}>
        ↑
      </button>
      <button className="btn btn-sm" type="button" aria-label="Next match" disabled={count === 0} onClick={() => onMove(1)}>
        ↓
      </button>
      <button className="btn btn-sm" type="button" aria-pressed={matchCase} onClick={() => onMatchCase(!matchCase)}>
        Match case
      </button>
      <button className="btn btn-sm btn-ghost" type="button" onClick={onClose}>
        Close
      </button>
    </div>
  );
}
