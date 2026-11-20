import type { PaneKeyName } from "../api/client";

// KeyPress is what the pane reads of one key press.
export type KeyPress = { key: string; code?: string; ctrlKey: boolean; altKey: boolean; shiftKey: boolean; metaKey: boolean };

// The browser's names for the keys a chord may end with, and tmux's name for each, which the server takes.
const named: Record<string, PaneKeyName> = {
  ArrowUp: "Up",
  ArrowDown: "Down",
  ArrowLeft: "Left",
  ArrowRight: "Right",
  Backspace: "BSpace",
  Delete: "DC",
  End: "End",
  Enter: "Enter",
  Escape: "Escape",
  Home: "Home",
  Insert: "IC",
  PageUp: "PageUp",
  PageDown: "PageDown",
  " ": "Space",
  Tab: "Tab",
  F1: "F1",
  F2: "F2",
  F3: "F3",
  F4: "F4",
  F5: "F5",
  F6: "F6",
  F7: "F7",
  F8: "F8",
  F9: "F9",
  F10: "F10",
  F11: "F11",
  F12: "F12",
};

// releaseKey is the release key, which gives the keyboard back to the page while the pane holds it, as the
// page writes it.
export const releaseKey = "Ctrl-]";

// isRelease says a press is the release key.
export function isRelease(press: KeyPress): boolean {
  return press.ctrlKey && !press.altKey && !press.metaKey && (press.key === "]" || press.code === "BracketRight");
}

// characterOf reads the one character a press ends with. With Alt, a Mac's Option key composes another
// character, so a letter or a digit is read by the key's place instead: KeyX is x, or X with Shift.
function characterOf(press: KeyPress): string | null {
  if (press.altKey && press.code) {
    const letter = /^Key([A-Z])$/.exec(press.code)?.[1];
    if (letter) {
      return press.shiftKey ? letter : letter.toLowerCase();
    }
    const digit = /^Digit([0-9])$/.exec(press.code)?.[1];
    if (digit) {
      return digit;
    }
  }
  return [...press.key].length === 1 ? press.key : null;
}

// chordOf names a key press in the pane's key grammar, or null for a press the session does not take: a
// modifier alone, a press with Command or the Windows key, which stays with the browser and the page, or a
// key the grammar has no name for. A character carries its own Shift, so Shift is named only on a named key.
export function chordOf(press: KeyPress): string | null {
  if (press.metaKey) {
    return null;
  }
  const modifiers = [press.ctrlKey ? "Ctrl" : "", press.altKey ? "Alt" : ""].filter(Boolean);
  const name = named[press.key];
  if (name) {
    return [...modifiers, ...(press.shiftKey ? ["Shift"] : []), name].join("-");
  }
  const character = characterOf(press);
  return character === null ? null : [...modifiers, character].join("-");
}

// capsOf splits a key sequence into its chords and each chord into its keys, to show them as key caps. A
// hyphen after a modifier is the key itself, so Ctrl-- is Ctrl and the minus key.
export function capsOf(sequence: string): string[][] {
  return sequence
    .split(/\s+/)
    .filter(Boolean)
    .map((chord) => {
      const caps: string[] = [];
      let rest = chord;
      for (let modifier = /^(Ctrl|Alt|Shift)-(?=.)/i.exec(rest); modifier; modifier = /^(Ctrl|Alt|Shift)-(?=.)/i.exec(rest)) {
        caps.push(modifier[1] ?? "");
        rest = rest.slice(modifier[0].length);
      }
      return [...caps, rest];
    });
}
