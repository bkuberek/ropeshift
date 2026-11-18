// What a tool printed on a worker's machine reaches the page as text: a step's detail is clipped git or gh stderr, so it
// is untrusted. A page shows it as a text node, never as HTML or markdown and never as a link, and drops the terminal
// escapes and control characters it can carry, as the command line does.

// A CSI sequence, an OSC string ended by BEL or ST, and any other two-character escape, with the C1 forms of CSI and OSC.
const sequences = /(?:\u001b\[|\u009b)[0-?]*[ -/]*[@-~]|(?:\u001b\]|\u009d)[^\u0007\u001b\u009c]*(?:\u0007|\u001b\\|\u009c)?|\u001b[ -~]/g;
// The C0 set except newline and tab, DEL, and the C1 set.
// eslint-disable-next-line no-control-regex
const controls = /[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/g;

// plainText drops escape sequences and every control character except newline and tab.
export function plainText(text: string | undefined): string {
  return (text ?? "").replace(sequences, "").replace(controls, "");
}
