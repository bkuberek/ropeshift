import type { DriverManifest, DriverResult, WorkerLine } from "../api/types";
import { Absent, Freshness, Id, Panel, StateBadge, TableRegion } from "../ui/display";
import { driverStates } from "./states";

// One row of the table: a driver of a machine, with the doctor's result for it, or the one row of a machine
// that reported no driver.
type DriverRow = { line: WorkerLine; result?: DriverResult; manifest?: DriverManifest };

// rowsOf pairs each driver a worker advertised with the doctor's result the API gives for it. A worker whose
// manifest names none gets one row with neither, which the table says is not reported.
function rowsOf(line: WorkerLine): DriverRow[] {
  if (line.drivers.length === 0) {
    return [{ line }];
  }
  return line.drivers.map((result) => ({ line, result, manifest: line.worker.manifest.drivers?.find((driver) => driver.driver === result.driver) }));
}

function stateOf(row: DriverRow): string {
  return row.result ? (row.result.usable ? "usable" : "unusable") : "not_reported";
}

// reasonOf says why a driver is not usable as `ropeshift worker doctor` does: the word first, then the doctor's
// reason, and after a full stop the fix for a CLI that failed its version check. The words come from the API.
function ReasonOf({ result }: { result?: DriverResult }) {
  if (!result?.word) {
    return null;
  }
  return (
    <>
      <strong className="driver-word">{result.word}</strong> {result.reason}
      {result.fix ? `. ${result.fix}` : ""}
    </>
  );
}

// DriversPanel is each worker's doctor result, one row for each driver of each machine (FR-DRV-6, FR-DRV-7). A driver
// is usable by the rule an offer applies, so a sign-in the doctor could not read stays usable. The doctor runs when
// the worker starts, so a result is as old as the worker's last connect, and is never marked stale.
export function DriversPanel({ workers }: { workers: WorkerLine[] }) {
  const rows = workers.flatMap(rowsOf);
  return (
    <Panel
      title="Drivers"
      flush
      foot={
        <>
          The doctor runs when a worker starts, so a result is as old as the worker's last connect. A driver is usable when its CLI is present and its sign-in is not signed
          out, and a sign-in the doctor could not read stays usable. <span className="mono">ropeshift worker doctor</span> reports the same for a machine with no daemon.
        </>
      }
    >
      <TableRegion label="Drivers">
        <table className="table stack-on-phone drivers">
          <thead>
            <tr>
              <th scope="col">Machine</th>
              <th scope="col">Driver</th>
              <th scope="col">Result</th>
              <th scope="col">Version</th>
              <th scope="col">Qualification</th>
              <th scope="col">Sign-in</th>
              <th scope="col">Reason</th>
              <th scope="col">Freshness</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => {
              const { line, result, manifest } = row;
              const present = manifest?.present ?? false;
              return (
                <tr key={`${line.worker.id}:${result?.driver ?? ""}`}>
                  <td data-label="Machine">
                    <strong>{line.worker.name || line.worker.id}</strong>
                    <span className="cell-note">
                      <Id value={line.worker.id} />
                    </span>
                  </td>
                  <td data-label="Driver" className={result ? "mono" : "hide-phone"}>
                    {result?.driver}
                  </td>
                  <td data-label="Result">
                    <StateBadge table={driverStates} state={stateOf(row)} />
                  </td>
                  <td data-label="Version" className={present ? undefined : "hide-phone"}>
                    {present ? manifest?.version || <Absent>unknown</Absent> : null}
                  </td>
                  <td data-label="Qualification" className={present ? undefined : "hide-phone"}>
                    {present ? (manifest?.qualified ? "qualified" : "unqualified") : null}
                  </td>
                  <td data-label="Sign-in" className={present ? undefined : "hide-phone"}>
                    {present && manifest ? (
                      <>
                        {manifest.sign_in.kind || <Absent>unknown</Absent>} {manifest.sign_in.status.replaceAll("_", " ")}
                      </>
                    ) : null}
                  </td>
                  <td data-label="Reason" className={result?.word ? "driver-reason" : "hide-phone"}>
                    <ReasonOf result={result} />
                  </td>
                  <td data-label="Freshness">
                    {line.worker.last_handshake_at ? (
                      <Freshness kind="last_reported" at={line.worker.last_handshake_at} source="the worker's last connect" />
                    ) : (
                      <Absent>never connected</Absent>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </TableRegion>
    </Panel>
  );
}
