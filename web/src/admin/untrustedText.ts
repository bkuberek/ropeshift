// What a tool printed on a worker's machine reaches the page as text: a step's detail is clipped git or gh stderr, so it
// is untrusted. A page shows it as a text node, never as HTML or markdown and never as a link, and drops the terminal
// escapes and control characters it can carry, as the command line does.

// A CSI sequence, an OSC string ended by BEL or ST, and any other two-character escape, with the C1 forms of CSI and OSC.
const sequences = /(?:\u001b\[|\u009b)[0-?]*[ -/]*[@-~]|(?:\u001b\]|\u009d)[^\u0007\u001b\u009c]*(?:\u0007|\u001b\\|\u009c)?|\u001b[ -~]/g;
// The control characters (the C0 set, DEL, and the C1 set) except newline and tab, and the format characters: the
// bidirectional overrides, isolates, and marks, and the zero-width characters. This is the set ledger.HiddenRune names,
// and web/testdata/hidden-runes.json holds it to the same one.
const hidden = /(?![\n\t])[\p{Cc}\p{Cf}]/gu;

// plainText drops escape sequences, every control character except newline and tab, and every character that hides or
// reorders text.
export function plainText(text: string | undefined): string {
  return (text ?? "").replace(sequences, "").replace(hidden, "");
}
