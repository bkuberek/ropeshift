import { readCIDurations } from "../api/measures";
import type { CIDuration, CIDurations } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { Absent, Banner, Panel, SkeletonRows, TableRegion } from "../ui/display";
import { span } from "./format";

// CI duration is read from the CI records the control plane keeps as heads finish, which change no oftener than work does,
// so the panel asks again no oftener than this however much it hears.
const gapMs = 30_000;

const band = (p50: number | undefined, p85: number | undefined) => `p50 ${span(p50 ?? 0)} · p85 ${span(p85 ?? 0)}`;

// Row says what one repository's CI takes, or that it has no history yet, with how many heads it has of the number it
// needs and the time its no-history keys expect meanwhile.
function Row({ row }: { row: CIDuration }) {
  if (!row.history) {
    return (
      <tr>
        <th scope="row">{row.name}</th>
        <td data-label="Heads" className="r">
          {row.records} of {row.min_records}
        </td>
        <td data-label="Push to completed">
          <Absent>no history</Absent> · expected {band(row.no_history_p50_ms, row.no_history_p85_ms)}
        </td>
        <td data-label="Waiting for a runner">
          <Absent>no history</Absent>
        </td>
      </tr>
    );
  }
  return (
    <tr>
      <th scope="row">{row.name}</th>
      <td data-label="Heads" className="r">
        {row.records}
      </td>
      <td data-label="Push to completed">{band(row.p50_ms, row.p85_ms)}</td>
      <td data-label="Waiting for a runner">{band(row.queue_p50_ms, row.queue_p85_ms)}</td>
    </tr>
  );
}

function Table({ view }: { view: CIDurations }) {
  return (
    <TableRegion label="CI duration by repository">
      <table className="table stack-on-phone">
        <thead>
          <tr>
            <th scope="col">Repository</th>
            <th scope="col" className="r">
              Heads
            </th>
            <th scope="col">Push to completed</th>
            <th scope="col">Waiting for a runner</th>
          </tr>
        </thead>
        <tbody>
          {view.repositories.map((row) => (
            <Row key={row.repository} row={row} />
          ))}
        </tbody>
      </table>
    </TableRegion>
  );
}

// CIDurationPanel is what each repository's CI takes: the p50 and p85 of its recent heads' time from the push to the
// last completion, and of the time a run waited for a runner. A repository with too few heads has no history, and the
// time its keys expect applies, which the work order's page and the CI limits read (FR-SPEND-21).
export function CIDurationPanel({ live }: { live: Live }) {
  const read = useView<CIDurations>(readCIDurations, "ci-duration", live, gapMs);
  const view = read.view;
  return (
    <>
      {read.failure ? <Banner tone="danger">{view ? `What this panel shows is not current. ${read.failure}` : read.failure}</Banner> : null}
      {view ? (
        <Panel title="CI duration" flush={view.repositories.length > 0} foot="From each repository's most recent heads whose required checks passed or failed. A repository with fewer heads than ci_history_min_records has no history, and the no-history keys apply.">
          {view.repositories.length === 0 ? <p className="muted">No repository is registered.</p> : <Table view={view} />}
        </Panel>
      ) : null}
      {!view && !read.failure ? (
        <div className="panel">
          <SkeletonRows rows={3} />
        </div>
      ) : null}
    </>
  );
}
