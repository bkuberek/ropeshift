import { describe, expect, it } from "vitest";

import { paneKeyNames } from "../api/client";
import { capsOf, chordOf, isRelease } from "./chords";
import type { KeyPress } from "./chords";

function press(key: string, held: Partial<KeyPress> = {}): KeyPress {
  return { key, code: "", ctrlKey: false, altKey: false, shiftKey: false, metaKey: false, ...held };
}

describe("a key press in a captured pane", () => {
  it("names the keys a terminal sends by tmux's names", () => {
    expect(chordOf(press("Tab"))).toBe("Tab");
    expect(chordOf(press("Tab", { shiftKey: true }))).toBe("Shift-Tab");
    expect(chordOf(press("Escape"))).toBe("Escape");
    expect(chordOf(press("Enter"))).toBe("Enter");
    expect(chordOf(press("ArrowUp"))).toBe("Up");
    expect(chordOf(press("ArrowLeft", { shiftKey: true }))).toBe("Shift-Left");
    expect(chordOf(press("Backspace"))).toBe("BSpace");
    expect(chordOf(press("Delete"))).toBe("DC");
    expect(chordOf(press("Insert"))).toBe("IC");
    expect(chordOf(press(" "))).toBe("Space");
    expect(chordOf(press("F5"))).toBe("F5");
    expect(chordOf(press("PageDown"))).toBe("PageDown");
  });

  it("sends a Ctrl or Alt combination with its modifiers", () => {
    expect(chordOf(press("o", { ctrlKey: true }))).toBe("Ctrl-o");
    expect(chordOf(press("x", { ctrlKey: true, altKey: true, code: "KeyX" }))).toBe("Ctrl-Alt-x");
    expect(chordOf(press(" ", { ctrlKey: true }))).toBe("Ctrl-Space");
    expect(chordOf(press("-", { ctrlKey: true }))).toBe("Ctrl--");
  });

  it("sends a character as it is, with the Shift it already carries", () => {
    expect(chordOf(press("a"))).toBe("a");
    expect(chordOf(press("A", { shiftKey: true }))).toBe("A");
    expect(chordOf(press(";"))).toBe(";");
    expect(chordOf(press("é"))).toBe("é");
  });

  it("reads Alt with a letter by the key's place, because a Mac's Option key composes another character", () => {
    expect(chordOf(press("≈", { altKey: true, code: "KeyX" }))).toBe("Alt-x");
    expect(chordOf(press("˛", { altKey: true, shiftKey: true, code: "KeyX" }))).toBe("Alt-X");
    expect(chordOf(press("¡", { altKey: true, code: "Digit1" }))).toBe("Alt-1");
  });

  it("leaves Command and the Windows key to the browser and the page, and sends no modifier alone", () => {
    expect(chordOf(press("k", { metaKey: true }))).toBeNull();
    expect(chordOf(press("c", { metaKey: true }))).toBeNull();
    for (const alone of ["Shift", "Control", "Alt", "Meta", "CapsLock", "Dead", "Unidentified", "Process", "F13", "MediaPlayPause"]) {
      expect(chordOf(press(alone)), alone).toBeNull();
    }
  });

  it("names only keys the server takes", () => {
    const named = ["Tab", "Escape", "Enter", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Backspace", "Delete", "Insert", "Home", "End", "PageUp", "PageDown", " "];
    for (const key of [...named, "F1", "F12"]) {
      const chord = chordOf(press(key));
      expect(paneKeyNames as readonly string[], key).toContain(chord);
    }
  });

  it("gives the keys back on Ctrl-] and no other press", () => {
    expect(isRelease(press("]", { ctrlKey: true }))).toBe(true);
    expect(isRelease(press("Unidentified", { ctrlKey: true, code: "BracketRight" }))).toBe(true);
    expect(isRelease(press("]"))).toBe(false);
    expect(isRelease(press("]", { ctrlKey: true, altKey: true }))).toBe(false);
    expect(isRelease(press("Escape"))).toBe(false);
  });
});

describe("a key sequence as key caps", () => {
  it("splits a sequence into chords and each chord into its keys", () => {
    expect(capsOf("Ctrl-X Ctrl-S")).toEqual([["Ctrl", "X"], ["Ctrl", "S"]]);
    expect(capsOf("Shift-Tab")).toEqual([["Shift", "Tab"]]);
    expect(capsOf("Ctrl--")).toEqual([["Ctrl", "-"]]);
    expect(capsOf("-")).toEqual([["-"]]);
    expect(capsOf("  Enter   Tab ")).toEqual([["Enter"], ["Tab"]]);
    expect(capsOf("")).toEqual([]);
  });
});
