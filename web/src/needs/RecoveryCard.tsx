import { decideApproval } from "../api/client";
import type { Approval } from "../api/types";
import { Button } from "../ui/controls";
import { DecisionCard } from "../ui/DecisionCard";
import { approvalStates } from "../floor/states";
import { Digest, Fact, Facts, Id, Refusal, StateBadge, TargetChip } from "../ui/display";
import type { Frame } from "./frame";
import { detailOf } from "./recovery";
import { useAct } from "./useAct";

// A recovery request is an act an orchestrator's policy did not allow. Approving it runs that act as this
// member's decision, on the subject this page read: a work order or lane that moved since makes it void.
export function RecoveryCard({ approval, after, frame }: { approval: Approval; after: () => Promise<void>; frame: Frame }) {
  const detail = detailOf(approval);
  const { act, busy, failure } = useAct(after);
  const decide = (allow: boolean) => act(() => decideApproval(approval.id, approval.subject_digest, allow, ""));
  return (
    <DecisionCard
      kind={{ icon: "restart", name: "Recovery request" }}
      project={frame.project}
      title={frame.title}
      provenance={
        <>
          <TargetChip kind="orchestrator" small /> {frame.provenance}
        </>
      }
      state={<StateBadge table={approvalStates} state={approval.state} />}
      binding={
        <>
          Bound to <Digest value={approval.subject_digest} />. If the work order or lane moves, this request is void.
        </>
      }
      actions={
        <>
          <Button disabled={busy} onClick={() => void decide(false)}>
            Decline
          </Button>
          <Button variant="primary" loading={busy} disabled={busy} onClick={() => void decide(true)}>
            Approve
          </Button>
        </>
      }
    >
      <Facts>
        <Fact name="Why the orchestrator asks">{detail.reason ?? ""}</Fact>
        <Fact name="The policy limit it went past">{detail.limit ?? ""}</Fact>
        {detail.check ? <Fact name="The check that failed">{detail.check}</Fact> : null}
        {detail.policy_version ? <Fact name="Policy">version {detail.policy_version}</Fact> : null}
        <Fact name="Work order">
          <Id value={approval.work_order_id} />
        </Fact>
        {approval.lane_id ? (
          <Fact name="Lane">
            <Id value={approval.lane_id} />
          </Fact>
        ) : null}
        <Fact name="Asked by" mono>
          {approval.requested_by}
        </Fact>
      </Facts>
      {failure ? <Refusal>{failure}</Refusal> : null}
    </DecisionCard>
  );
}
