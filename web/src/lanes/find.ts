import type { Run } from "./screen";

// Marked is a piece of a screen's run, with the match it is part of, or null outside every match.
export type Marked = Run & { match: number | null };

// findIn finds every place the words occur in a pane's text, as offsets, without overlaps. Case counts only
// when asked.
export function findIn(text: string, words: string, matchCase: boolean): number[] {
  if (!words) {
    return [];
  }
  const hay = matchCase ? text : text.toLowerCase();
  const needle = matchCase ? words : words.toLowerCase();
  const found: number[] = [];
  for (let at = hay.indexOf(needle); at !== -1; at = hay.indexOf(needle, at + needle.length)) {
    found.push(at);
  }
  return found;
}

// markRuns splits a screen's runs where each match starts and ends, so a match keeps the colours of the
// text it spans, and names the match each piece is part of.
export function markRuns(runs: Run[], starts: number[], length: number): Marked[] {
  const marked: Marked[] = [];
  let offset = 0;
  let next = 0;
  for (const run of runs) {
    let at = 0;
    while (at < run.text.length) {
      const here = offset + at;
      while (next < starts.length && (starts[next] ?? 0) + length <= here) {
        next++;
      }
      const start = starts[next];
      const inside = start !== undefined && start <= here;
      const until = inside ? start + length - offset : start === undefined ? run.text.length : start - offset;
      const end = Math.min(run.text.length, until);
      marked.push({ text: run.text.slice(at, end), className: run.className, match: inside ? next : null });
      at = end;
    }
    offset += run.text.length;
  }
  return marked;
}
