import { useState } from "react";

import { decideApproval } from "../api/client";
import type { Place } from "../api/client";
import type { Approval } from "../api/types";
import { addressOf, Link } from "../app/route";
import { Button } from "../ui/controls";
import { DecisionCard, DecisionSection } from "../ui/DecisionCard";
import { approvalStates } from "../floor/states";
import { Digest, Fact, Facts, Refusal, StateBadge } from "../ui/display";
import { Field } from "../ui/forms";
import type { Frame } from "./frame";
import { kindOf } from "./kinds";
import { authorityWords, isHeld, mergeDetailOf } from "./merge";
import { useAct } from "./useAct";

const shortOf = (commit: string) => commit.slice(0, 12);

// A merge request asks a member about the merge of one reviewed commit of a work order, which no lane and no
// command is part of. At person_approves, approving records the member's approval of that commit and asks the
// worker to merge it, and denying sends the work back to rework. For a merge that cannot go on, approving
// tries it again and denying sends the work back. The commit sent is the one on this page, so a request
// whose work moved to another commit is refused, and a person never approves a commit they did not read.
export function MergeCard({ approval, after, frame, compact = false, place }: { approval: Approval; after: () => Promise<void>; frame: Frame; compact?: boolean; place?: Place }) {
  const detail = mergeDetailOf(approval);
  const held = isHeld(approval);
  const commit = approval.subject_digest;
  const short = shortOf(commit);
  const [reason, setReason] = useState("");
  const { act, busy, failure } = useAct(after);
  const decide = (allow: boolean) => act(() => decideApproval(approval.id, commit, allow, reason.trim(), place));
  const base = detail.base || "its base branch";
  const method = detail.method ? `the ${detail.method} method` : "the repository's merge method";
  const target = detail.pull_request ? `pull request #${detail.pull_request}` : `${detail.branch || "its branch"} into ${base}`;
  const approves = held ? "Retry merge" : "Approve merge";
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
          Bound to the reviewed commit <Digest value={commit} />. If the work moves to another commit, or leaves integration, this request is void.
        </>
      }
      actions={
        <>
          <Button disabled={busy} onClick={() => void decide(false)}>
            Deny
          </Button>
          <Button variant="primary" loading={busy} disabled={busy} onClick={() => void decide(true)}>
            {approves}
          </Button>
        </>
      }
    >
      <p>{approval.summary}</p>
      <Facts label="The merge">
        {detail.repository ? <Fact name="Repository">{detail.repository}</Fact> : null}
        {detail.pull_request ? (
          <Fact name="Pull request">
            #{detail.pull_request}
            {detail.url ? (
              <>
                {" · "}
                <span className="mono">{detail.url}</span>
              </>
            ) : null}
          </Fact>
        ) : null}
        {detail.branch ? (
          <Fact name="Branch" mono>
            {detail.base ? `${detail.branch} into ${detail.base}` : detail.branch}
          </Fact>
        ) : null}
        <Fact name="Reviewed commit" mono>
          <Digest value={commit} length={12} />
        </Fact>
        {detail.method ? <Fact name="Merge method">{detail.method}</Fact> : null}
        {detail.merge_authority ? (
          <Fact name="Merge authority">
            <span className="mono">{detail.merge_authority}</span>
            {authorityWords[detail.merge_authority] ? `: ${authorityWords[detail.merge_authority]}` : ""}
          </Fact>
        ) : null}
        {held && detail.reason ? <Fact name="Why it stopped">{detail.reason}</Fact> : null}
        <Fact name="Work order">
          <Link to={addressOf("/work-order", { id: approval.work_order_id })}>{approval.work_order_id}</Link>
        </Fact>
      </Facts>
      <DecisionSection title="What each choice does">
        <Facts>
          <Fact name={approves}>
            {held
              ? `Tries the merge again: the worker is asked to merge commit ${short} into ${base} with ${method}.`
              : `Records your approval of commit ${short} and asks the worker to merge ${target} with ${method}.`}
          </Fact>
          <Fact name="Deny">Sends {approval.work_order_id} back to rework, and your reason goes into the brief its next attempt reads.</Fact>
        </Facts>
      </DecisionSection>
      <Field label="Reason, which the rework brief carries if you deny (optional)">
        <input className="input" type="text" autoComplete="off" value={reason} disabled={busy} onChange={(event) => setReason(event.target.value)} />
      </Field>
      {failure ? <Refusal>{failure}</Refusal> : null}
    </DecisionCard>
  );
}
