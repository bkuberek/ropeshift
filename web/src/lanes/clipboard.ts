// copyText puts text on the clipboard, and says what happened in words a person reads.
export async function copyText(text: string, what: string): Promise<string> {
  try {
    await navigator.clipboard.writeText(text);
    const lines = text === "" ? 0 : text.split("\n").length;
    return `Copied ${what}: ${lines} ${lines === 1 ? "line" : "lines"}.`;
  } catch {
    return "The browser did not allow the copy. Select the text, then copy it with the keyboard.";
  }
}
