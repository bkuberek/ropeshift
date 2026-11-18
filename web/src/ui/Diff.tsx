import type { DiffLine } from "./lineDiff";

type Row = DiffLine | { kind: "fold"; count: number };

// folded holds back each run of unchanged lines that is longer than the context on both of its sides, so a change in a long
// text shows the lines around it and says how many it left out. A run of one line is shown, because a row that says one
// line was left out is no shorter.
function folded(lines: DiffLine[], context: number): Row[] {
  const out: Row[] = [];
  let at = 0;
  while (at < lines.length) {
    if (lines[at]?.kind !== "same") {
      out.push(lines[at] as DiffLine);
      at += 1;
      continue;
    }
    let end = at;
    while (lines[end]?.kind === "same") {
      end += 1;
    }
    const before = at === 0 ? 0 : context;
    const after = end === lines.length ? 0 : context;
    const hidden = end - at - before - after;
    if (hidden < 2) {
      out.push(...lines.slice(at, end));
    } else {
      out.push(...lines.slice(at, at + before), { kind: "fold", count: hidden }, ...lines.slice(end - after, end));
    }
    at = end;
  }
  return out;
}

// Diff shows a difference by line: what goes in green with a plus, what goes out in red with a minus. With a context, an
// unchanged run longer than the context on both sides is folded into one row that says how many lines it holds.
export function Diff({ lines, label, context }: { lines: DiffLine[]; label: string; context?: number }) {
  if (lines.length === 0) {
    return <p className="small muted">No difference.</p>;
  }
  const rows = context === undefined ? lines : folded(lines, context);
  return (
    <div className="diff">
      <table className="diff-table" aria-label={label}>
        <tbody>
          {rows.map((line, at) =>
            line.kind === "fold" ? (
              <tr key={at} className="fold">
                <td className="sign" />
                <td className="muted">{line.count} unchanged lines</td>
              </tr>
            ) : (
              <tr key={at} className={line.kind === "same" ? undefined : line.kind}>
                <td className="sign">
                  {line.kind === "add" ? "+" : line.kind === "del" ? "−" : ""}
                  <span className="visually-hidden">{line.kind === "add" ? "added" : line.kind === "del" ? "removed" : ""}</span>
                </td>
                <td>{line.text}</td>
              </tr>
            ),
          )}
        </tbody>
      </table>
    </div>
  );
}
