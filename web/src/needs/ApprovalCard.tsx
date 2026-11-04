import { useState } from "react";

import { decideApproval } from "../api/client";
import type { Approval } from "../api/types";
import { useAct } from "./useAct";

// The digest sent is the one on this page, so a call that changed since this read is refused, and a
// person never approves something they did not read.
export function ApprovalCard({ approval, after }: { approval: Approval; after: () => Promise<void> }) {
  const [reason, setReason] = useState("");
  const { act, busy, failure } = useAct(after);
  const decide = (allow: boolean) => act(() => decideApproval(approval.id, approval.subject_digest, allow, reason.trim()));
  return (
    <div className="card-body">
      <p className="lead">{approval.summary}</p>
      {approval.command || approval.input ? <pre className="call">{approval.command || approval.input}</pre> : null}
      <dl className="facts">
        {approval.path ? (
          <div>
            <dt>Lands at</dt>
            <dd className="mono">{approval.path}</dd>
          </div>
        ) : null}
        <div>
          <dt>Lane</dt>
          <dd className="mono">{approval.lane_id}</dd>
        </div>
        <div>
          <dt>Work order</dt>
          <dd className="mono">{approval.work_order_id}</dd>
        </div>
        <div>
          <dt>Digest</dt>
          <dd className="mono" title={approval.subject_digest}>
            {approval.subject_digest.slice(0, 16)}
          </dd>
        </div>
      </dl>
      <label className="field">
        <span className="field-label">Reason, which the lane reads (optional)</span>
        <input type="text" value={reason} disabled={busy} onChange={(event) => setReason(event.target.value)} />
      </label>
      {failure ? (
        <p className="refusal" role="alert">
          {failure}
        </p>
      ) : null}
      <div className="actions">
        <button className="button button-strong" type="button" disabled={busy} onClick={() => void decide(true)}>
          Allow this one call
        </button>
        <button className="button" type="button" disabled={busy} onClick={() => void decide(false)}>
          Deny
        </button>
      </div>
    </div>
  );
}
