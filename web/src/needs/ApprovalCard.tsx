import { useState } from "react";

import { decideApproval } from "../api/client";
import type { Place } from "../api/client";
import type { Approval } from "../api/types";
import { Button } from "../ui/controls";
import { DecisionCard, DecisionSection } from "../ui/DecisionCard";
import { approvalStates } from "../floor/states";
import { Digest, Fact, Facts, Id, Refusal, StateBadge } from "../ui/display";
import { Field } from "../ui/forms";
import type { Frame } from "./frame";
import { kindOf } from "./kinds";
import { useAct } from "./useAct";

// The digest sent is the one on this page, so a call that changed since this read is refused, and a
// person never approves something they did not read.
export function ApprovalCard({ approval, after, frame, compact = false, place }: { approval: Approval; after: () => Promise<void>; frame: Frame; compact?: boolean; place?: Place }) {
  const [reason, setReason] = useState("");
  const { act, busy, failure } = useAct(after);
  const decide = (allow: boolean) => act(() => decideApproval(approval.id, approval.subject_digest, allow, reason.trim(), place));
  return (
    <DecisionCard
      compact={compact}
      kind={kindOf({ kind: "approval", approval, id: approval.id, since: 0 })}
      project={frame.project}
      title={frame.title}
      provenance={frame.provenance}
      state={<StateBadge table={approvalStates} state={approval.state} />}
      binding={
        <>
          Bound to <Digest value={approval.subject_digest} />. If the call changes, this approval is void.
        </>
      }
      actions={
        <>
          <Button disabled={busy} onClick={() => void decide(false)}>
            Deny
          </Button>
          <Button variant="primary" loading={busy} disabled={busy} onClick={() => void decide(true)}>
            Allow this one call
          </Button>
        </>
      }
    >
      <p>{approval.summary}</p>
      {approval.command || approval.input ? (
        <DecisionSection title="Exact operation">
          <pre className="op call">{approval.command || approval.input}</pre>
        </DecisionSection>
      ) : null}
      <Facts>
        {approval.path ? (
          <Fact name="Lands at" mono>
            {approval.path}
          </Fact>
        ) : null}
        <Fact name="Lane">
          <Id value={approval.lane_id} />
        </Fact>
        <Fact name="Work order">
          <Id value={approval.work_order_id} />
        </Fact>
        <Fact name="Digest">
          <Digest value={approval.subject_digest} length={16} />
        </Fact>
      </Facts>
      <Field label="Reason, which the lane reads (optional)">
        <input className="input" type="text" autoComplete="off" value={reason} disabled={busy} onChange={(event) => setReason(event.target.value)} />
      </Field>
      {failure ? <Refusal>{failure}</Refusal> : null}
    </DecisionCard>
  );
}
