// The difference between two texts, by line, which every place that reviews a change shows.

export type DiffLine = { kind: "same" | "add" | "del"; text: string };

const linesOf = (text: string) => (text === "" ? [] : text.replace(/\n$/, "").split("\n"));

// lineDiff is the difference between two texts by line, the longest common run kept in place.
export function lineDiff(before: string, after: string): DiffLine[] {
  const a = linesOf(before);
  const b = linesOf(after);
  const longest: number[][] = Array.from({ length: a.length + 1 }, () => new Array<number>(b.length + 1).fill(0));
  for (let i = a.length - 1; i >= 0; i -= 1) {
    for (let j = b.length - 1; j >= 0; j -= 1) {
      longest[i]![j] = a[i] === b[j] ? longest[i + 1]![j + 1]! + 1 : Math.max(longest[i + 1]![j]!, longest[i]![j + 1]!);
    }
  }
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      out.push({ kind: "same", text: a[i]! });
      i += 1;
      j += 1;
    } else if (longest[i + 1]![j]! >= longest[i]![j + 1]!) {
      out.push({ kind: "del", text: a[i]! });
      i += 1;
    } else {
      out.push({ kind: "add", text: b[j]! });
      j += 1;
    }
  }
  for (; i < a.length; i += 1) {
    out.push({ kind: "del", text: a[i]! });
  }
  for (; j < b.length; j += 1) {
    out.push({ kind: "add", text: b[j]! });
  }
  return out;
}
