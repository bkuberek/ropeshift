import { readNotices } from "../api/admin";
import type { NoticeLine, NoticeList } from "../api/types";
import type { Live } from "../api/useView";
import { noticeStates } from "../floor/states";
import { words } from "../parts/parts";
import { Digest, EmptyState, Panel, StateBadge, TableRegion } from "../ui/display";
import { Reading, timeOf, useRead } from "./parts";

// stateOf says how a notice stands on its channel: delivered, refused with the channel's reason, or asked and not
// yet answered.
function stateOf(line: NoticeLine): string {
  return line.delivered ? "delivered" : line.failure ? "refused" : "pending";
}

// NoticesTab says what each channel was told of a project's, and how many times, as `ropeshift notice list` does.
// Delivery is at least once, so a channel that took a notice after more than one attempt may have heard it twice,
// and the row says so. A notice names no title, question, or path, so none is shown.
export function NoticesTab({ live, project }: { live: Live; project: string }) {
  const read = useRead<NoticeList>(() => readNotices(), `notices:${project}`, live);
  const lines = read.view?.notices ?? [];
  return (
    <Reading failure={read.failure} ready={read.view !== null}>
      {lines.length === 0 ? (
        <div className="panel">
          <EmptyState icon="send" title="No channel has been told anything">
            A notice goes to a channel only when a notify policy turns a channel on and something waits for a person. With nothing turned on, nothing leaves this machine.
          </EmptyState>
        </div>
      ) : (
        <Panel
          flush
          foot="Delivery is at least once: a crash between the send and its record repeats a notice, and a ledger that shows more than one attempt with a delivery shows a channel that may have heard it twice."
        >
          <TableRegion label="Notices">
            <table className="table stack-on-phone">
              <thead>
                <tr>
                  <th scope="col">Notice</th>
                  <th scope="col">Channel</th>
                  <th scope="col">State</th>
                  <th scope="col" className="r">
                    Attempts
                  </th>
                  <th scope="col">Since</th>
                  <th scope="col">Last attempt</th>
                  <th scope="col">Delivery</th>
                </tr>
              </thead>
              <tbody>
                {lines.map((line) => (
                  <tr key={`${line.delivery}:${line.channel}`}>
                    <td data-label="Notice">
                      {words(line.kind)}
                      {line.reason ? <span className="cell-note mono">{line.reason}</span> : null}
                    </td>
                    <td data-label="Channel">{line.channel}</td>
                    <td data-label="State">
                      <StateBadge table={noticeStates} state={stateOf(line)} />
                      {line.failure ? <span className="cell-note">{line.failure}</span> : null}
                      {line.delivered && line.attempts > 1 ? <span className="cell-note">The channel may have heard it more than once.</span> : null}
                    </td>
                    <td data-label="Attempts" className="r num">
                      {line.attempts}
                    </td>
                    <td data-label="Since" className="num">
                      {timeOf(line.since)}
                    </td>
                    <td data-label="Last attempt" className="num">
                      {timeOf(line.last_at)}
                    </td>
                    <td data-label="Delivery" className="mono small">
                      <Digest value={line.delivery} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableRegion>
        </Panel>
      )}
    </Reading>
  );
}
