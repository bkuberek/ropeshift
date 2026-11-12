import { useId, useState } from "react";

import { passReview, sendBack } from "../api/client";
import type { ContractReviewNeed } from "../api/types";
import { addressOf, Link } from "../app/route";
import { Button } from "../ui/controls";
import { DecisionCard } from "../ui/DecisionCard";
import { Fact, Facts, Id, Refusal, WorkBadge } from "../ui/display";
import { Field } from "../ui/forms";
import type { Frame } from "./frame";
import { useAct } from "./useAct";

// Work no plan approval covers waits for a member to pass its contract's review. Passing it goes in one
// press, and sending the work back to planning says why, because the contract must change.
export function ContractReviewCard({ review, after, frame }: { review: ContractReviewNeed; after: () => Promise<void>; frame: Frame }) {
  const [asking, setAsking] = useState(false);
  const [reason, setReason] = useState("");
  const { act, busy, failure } = useAct(after);
  const form = useId();
  const id = review.work_order.id;
  const send = async () => {
    if (await act(() => sendBack(id, reason.trim()))) {
      setAsking(false);
      setReason("");
    }
  };
  return (
    <DecisionCard
      kind={{ icon: "review", name: "Contract review" }}
      project={frame.project}
      title={frame.title}
      provenance={frame.provenance}
      state={<WorkBadge state={review.work_order.state} />}
      binding={`Bound to ${review.contract}, version ${review.version}.`}
      actions={
        asking ? (
          <>
            <Button disabled={busy} onClick={() => setAsking(false)}>
              Cancel
            </Button>
            <Button variant="primary" type="submit" form={form} disabled={busy || reason.trim() === ""}>
              Send back to planning
            </Button>
          </>
        ) : (
          <>
            <Button disabled={busy} onClick={() => setAsking(true)}>
              Send back to planning
            </Button>
            <Button variant="primary" loading={busy} disabled={busy} onClick={() => void act(() => passReview(id))}>
              Pass the review
            </Button>
          </>
        )
      }
    >
      <p>Its contract waits for a member's review, because no plan approval covers it.</p>
      <Facts>
        <Fact name="Contract">{`${review.contract}, version ${review.version}`}</Fact>
        <Fact name="Work order">
          <Id value={id} />
        </Fact>
      </Facts>
      {review.plan ? (
        <p>
          <Link to={addressOf("/plans", { plan: review.plan })}>Read the contract</Link>
        </p>
      ) : null}
      {asking ? (
        <form
          id={form}
          className="reason"
          onSubmit={(event) => {
            event.preventDefault();
            void send();
          }}
        >
          <Field label="Why (required)">
            <textarea className="textarea" rows={2} autoComplete="off" value={reason} disabled={busy} required autoFocus onChange={(event) => setReason(event.target.value)} />
          </Field>
        </form>
      ) : null}
      {failure ? <Refusal>{failure}</Refusal> : null}
    </DecisionCard>
  );
}
