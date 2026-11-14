import type { View } from "./routes";
import { goKeys, titleOf } from "./routes";

// KeyInput is what the keyboard model reads of one key press: the key, its modifiers, whether focus is
// in a text field or the terminal pane, and whether the page runs on a Mac, where Mod is Command.
export type KeyInput = { key: string; metaKey: boolean; ctrlKey: boolean; altKey: boolean; shiftKey?: boolean; inField: boolean; mac: boolean };

export type KeyAction =
  | { type: "palette" }
  | { type: "sheet" }
  | { type: "go"; view: View }
  | { type: "projects" }
  | { type: "dock" }
  | { type: "target" }
  | { type: "next" }
  | { type: "previous" }
  | { type: "primary" };

// KeyState remembers a G that waits for the key of the view to go to.
export type KeyState = { goSince: number | null };

export const idle: KeyState = { goSince: null };

// A G waits this long for its second key.
const goWaitMs = 1500;

const single: Record<string, KeyAction> = {
  "/": { type: "palette" },
  "?": { type: "sheet" },
  p: { type: "projects" },
  ".": { type: "dock" },
  t: { type: "target" },
  j: { type: "next" },
  k: { type: "previous" },
  a: { type: "primary" },
};

type Options = { singleKeys: boolean; now: number };

type Read = { action: KeyAction | null; state: KeyState; handled: boolean };

// readKey reads one key press. Mod+K opens the palette everywhere, a text field included. Every other
// shortcut is one key, which a person can turn off and which a text field or the terminal pane keeps for
// itself. No key decides anything: A moves focus to a decision's primary button and presses nothing.
export function readKey(input: KeyInput, state: KeyState, { singleKeys, now }: Options): Read {
  const key = input.key.length === 1 ? input.key.toLowerCase() : input.key;
  const mod = input.mac ? input.metaKey : input.ctrlKey;
  if (mod && key === "k" && !input.altKey) {
    return { action: { type: "palette" }, state: idle, handled: true };
  }
  if (input.metaKey || input.ctrlKey || input.altKey || input.inField || !singleKeys) {
    return { action: null, state: idle, handled: false };
  }
  if (state.goSince !== null && now - state.goSince <= goWaitMs) {
    const view = goKeys[key];
    return view ? { action: { type: "go", view }, state: idle, handled: true } : { action: null, state: idle, handled: false };
  }
  if (key === "g") {
    return { action: null, state: { goSince: now }, handled: true };
  }
  const action = single[key];
  return action ? { action, state: idle, handled: true } : { action: null, state: idle, handled: false };
}

// inField says focus is where keys are text: a field, an editable region, or the terminal pane.
export function inField(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) {
    return false;
  }
  if (target.isContentEditable || target.closest("[data-keys='own']")) {
    return true;
  }
  if (target instanceof HTMLInputElement) {
    return !["button", "checkbox", "radio", "submit", "reset", "range", "color", "file"].includes(target.type);
  }
  return target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement;
}

export function onMac(): boolean {
  return /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
}

export type ShortcutRow = { keys: string[]; does: string };

export type ShortcutGroup = { name: string; rows: ShortcutRow[] };

// shortcutList is the shortcut sheet: every key the keyboard model reads, and the keys a list, a menu,
// and the palette read themselves.
export function shortcutList(mac: boolean): ShortcutGroup[] {
  const mod = mac ? "⌘" : "Ctrl";
  return [
    {
      name: "Anywhere",
      rows: [
        { keys: [mod, "K"], does: "Open the command palette" },
        { keys: ["/"], does: "Open the command palette" },
        { keys: ["?"], does: "Show these shortcuts" },
        { keys: ["P"], does: "Switch project" },
        { keys: ["."], does: "Open or close the conversation panel, with the cursor in its message box" },
        { keys: ["T"], does: "Switch the conversation target" },
        { keys: ["Esc"], does: "Close the palette, dialog, menu, or drawer" },
      ],
    },
    {
      name: "Go to",
      rows: Object.entries(goKeys).map(([key, view]) => ({ keys: ["G", key.toUpperCase()], does: titleOf(view) })),
    },
    {
      name: "Lists and decisions",
      rows: [
        { keys: ["J"], does: "Next item" },
        { keys: ["K"], does: "Previous item" },
        { keys: ["Enter"], does: "Open the item" },
        { keys: ["A"], does: "Move focus to the decision's primary button, which you then press" },
        { keys: ["↑", "↓"], does: "Move in menus, tabs, and the palette" },
      ],
    },
  ];
}
