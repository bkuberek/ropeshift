import { ApiError, maxPaneChords, sendPaneKeys, typeIntoPane } from "../api/client";

type Input = { kind: "keys"; chords: string[] } | { kind: "text"; text: string; enter: boolean };

// PaneSender sends what a member types into a lane's pane, one input after another and in order, so no key
// overtakes the text before it. Chords pressed while an input is on its way wait together and go as one
// sequence, up to the most a sequence holds, so a person typing in a captured pane makes few requests. A
// refused input says why, and what waited behind it is dropped, because it was typed for a screen that
// did not take the input before it.
export class PaneSender {
  private readonly waiting: Input[] = [];
  private sending = false;

  constructor(
    private readonly lane: string,
    private readonly failed: (message: string) => void,
  ) {}

  // keys presses chords, each a chord in the pane's key grammar, such as Tab or Ctrl-o.
  keys(...chords: string[]): void {
    for (const chord of chords) {
      const last = this.waiting.at(-1);
      if (last?.kind === "keys" && last.chords.length < maxPaneChords) {
        last.chords.push(chord);
      } else {
        this.waiting.push({ kind: "keys", chords: [chord] });
      }
    }
    void this.send();
  }

  // text types a line, and presses Enter after it when asked.
  text(text: string, enter: boolean): void {
    this.waiting.push({ kind: "text", text, enter });
    void this.send();
  }

  private async send(): Promise<void> {
    if (this.sending) {
      return;
    }
    this.sending = true;
    try {
      for (let input = this.waiting.shift(); input; input = this.waiting.shift()) {
        try {
          await (input.kind === "keys" ? sendPaneKeys(this.lane, input.chords.join(" ")) : typeIntoPane(this.lane, input.text, input.enter));
        } catch (error) {
          this.waiting.length = 0;
          this.failed(error instanceof ApiError ? error.message : String(error));
        }
      }
    } finally {
      this.sending = false;
    }
  }
}
