import { useId, useState } from "react";

import { actOnWork } from "../api/client";
import { Link } from "../app/route";
import { workAddress } from "../floor/WorkOrders";
import { MergeCard } from "../needs/MergeCard";
import { isHeld, mergeHeading } from "../needs/merge";
import { Lasted } from "../parts/parts";
import { useAct } from "../needs/useAct";
import { Button } from "../ui/controls";
import { Absent, EmptyState, Id, Refusal, TableRegion, WorkBadge } from "../ui/display";
import { Field } from "../ui/forms";
import { Dialog, useToast } from "../ui/overlays";
import type { QueueRow } from "./inReview";
import { checksOf, notesOf, verdictsOf, waitsOn } from "./inReview";

// ReworkDialog asks why work goes back for another attempt, and sends it as `work rework` does.
function ReworkDialog({ row, onClose, after }: { row: QueueRow; onClose: () => void; after: () => Promise<void> }) {
  const [reason, setReason] = useState("");
  const { act, busy, failure } = useAct(after);
  const toast = useToast();
  const form = useId();
  const send = async () => {
    if (await act(() => actOnWork(row.id, "rework", reason.trim()))) {
      toast("success", `Sent ${row.title} back for rework.`);
      onClose();
    }
  };
  return (
    <Dialog
      open
      title={`Send ${row.id} back for rework`}
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form={form} loading={busy} disabled={busy || reason.trim() === ""}>
            Send back for rework
          </Button>
        </>
      }
    >
      <form
        id={form}
        className="field-stack"
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void send();
        }}
      >
        <p className="small muted">Sends {row.title} back for another attempt. Say what must change.</p>
        <Field label="Why (required)">
          <textarea className="textarea" rows={3} autoComplete="off" value={reason} disabled={busy} onChange={(event) => setReason(event.target.value)} />
        </Field>
        {failure ? <Refusal>{failure}</Refusal> : null}
      </form>
    </Dialog>
  );
}

// Actions are what a member can do to one row: the acts the read model lists for it, and no others.
function Actions({ row, after, onRework, onMerge }: { row: QueueRow; after: () => Promise<void>; onRework: () => void; onMerge: () => void }) {
  const { act, busy, failure } = useAct(after);
  const toast = useToast();
  if (!row.accept && !row.rework && !row.merge) {
    return <span className="small muted">Nothing for you to do</span>;
  }
  const accept = async () => {
    if (await act(() => actOnWork(row.id, "accept", ""))) {
      toast("success", `Accepted ${row.title}.`);
    }
  };
  return (
    <div className="stack-8">
      <span className="row-tight">
        {row.accept ? (
          <Button small variant="primary" loading={busy} disabled={busy} aria-label={`Accept ${row.id}`} onClick={() => void accept()}>
            Accept
          </Button>
        ) : null}
        {row.rework ? (
          <Button small disabled={busy} aria-label={`Rework ${row.id}`} onClick={onRework}>
            Rework
          </Button>
        ) : null}
        {row.merge ? (
          <Button small variant="primary" disabled={busy} aria-label={`${isHeld(row.merge) ? "Retry merge" : "Approve merge"} of ${row.id}`} onClick={onMerge}>
            {isHeld(row.merge) ? "Retry merge" : "Approve merge"}
          </Button>
        ) : null}
      </span>
      {failure ? (
        <div className="row-refusal">
          <Refusal>{failure}</Refusal>
        </div>
      ) : null}
    </div>
  );
}

// Queue lists the work that waits for acceptance or a merge, the longest wait first, each with the acts the read
// model offers for it. Accepting is one press, as on the work's own card; rework asks why; a merge request opens
// the merge card, which binds the approval to the commit the review read.
export function Queue({ rows, after }: { rows: QueueRow[]; after: () => Promise<void> }) {
  const [reworking, setReworking] = useState("");
  const [merging, setMerging] = useState("");
  const rework = rows.find((row) => row.id === reworking);
  const merge = rows.find((row) => row.id === merging);
  if (rows.length === 0) {
    return (
      <div className="panel">
        <EmptyState icon="review" title="Nothing is in review.">
          Work appears here once its lane finishes, while it waits for your acceptance or its merge.
        </EmptyState>
      </div>
    );
  }
  return (
    <>
      <div className="panel">
        <TableRegion label="Work in review">
          <table className="table stack-on-phone">
            <thead>
              <tr>
                <th scope="col">Work order</th>
                <th scope="col">State</th>
                <th scope="col">Review</th>
                <th scope="col">Waits on</th>
                <th scope="col" className="r">
                  In state
                </th>
                <th scope="col" className="r">
                  Act
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr key={row.id} data-work={row.id}>
                  <td data-label="Work order" className="title-cell">
                    <Link to={workAddress(row.id)} data-item>
                      {row.title}
                    </Link>
                    <span className="cell-note">
                      <Id value={row.id} />
                    </span>
                  </td>
                  <td data-label="State">
                    <WorkBadge state={row.state} />
                    <span className="cell-note">
                      Merge authority {row.standing.authority ? <span className="mono">{row.standing.authority}</span> : <Absent>not set</Absent>}
                    </span>
                  </td>
                  <td data-label="Review">
                    {verdictsOf(row.standing)}
                    {notesOf(row.standing) ? <span className="cell-note">{notesOf(row.standing)}</span> : null}
                    <span className="cell-note">{checksOf(row.standing)}</span>
                  </td>
                  <td data-label="Waits on" className="title-cell">
                    {waitsOn(row)}
                  </td>
                  <td data-label="In state" className="r">
                    {row.since ? <Lasted at={row.since} /> : <Absent>not read</Absent>}
                  </td>
                  <td data-label="Act" className="r actions-cell">
                    <Actions row={row} after={after} onRework={() => setReworking(row.id)} onMerge={() => setMerging(row.id)} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableRegion>
      </div>
      {rework ? <ReworkDialog row={rework} onClose={() => setReworking("")} after={after} /> : null}
      {merge?.merge ? (
        <Dialog open wide title={`Merge request for ${merge.id}`} onClose={() => setMerging("")}>
          <MergeCard approval={merge.merge} after={after} frame={{ title: mergeHeading(merge.merge) }} />
        </Dialog>
      ) : null}
    </>
  );
}
