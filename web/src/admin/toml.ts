// What a policy body writes as a TOML basic string. TOML allows no raw control character in one, so each is escaped, and a
// carriage return or a line break a member typed can never end a line of the body.
const named: Record<string, string> = { "\b": "\\b", "\t": "\\t", "\n": "\\n", "\f": "\\f", "\r": "\\r", '"': '\\"', "\\": "\\\\" };

export function tomlString(text: string): string {
  const escaped = text.replace(/[\u0000-\u001f"\\\u007f]/g, (char) => named[char] ?? `\\u${char.charCodeAt(0).toString(16).padStart(4, "0")}`);
  return `"${escaped}"`;
}
