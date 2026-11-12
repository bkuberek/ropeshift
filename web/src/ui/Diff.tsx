import type { DiffLine } from "./diff";

// Diff shows a difference by line: what goes in green with a plus, what goes out in red with a minus.
export function Diff({ lines, label }: { lines: DiffLine[]; label: string }) {
  if (lines.length === 0) {
    return <p className="small muted">No difference.</p>;
  }
  return (
    <div className="diff">
      <table className="diff-table" aria-label={label}>
        <tbody>
          {lines.map((line, at) => (
            <tr key={at} className={line.kind === "same" ? undefined : line.kind}>
              <td className="sign">
                {line.kind === "add" ? "+" : line.kind === "del" ? "−" : ""}
                <span className="visually-hidden">{line.kind === "add" ? "added" : line.kind === "del" ? "removed" : ""}</span>
              </td>
              <td>{line.text}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
