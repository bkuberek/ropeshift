import { useState } from "react";

import { passReview, sendBack } from "../api/client";
import type { ContractReviewNeed } from "../api/types";
import { addressOf, Link } from "../app/route";
import { Fact, Id } from "../parts/parts";
import { useAct } from "./useAct";

// Work no plan approval covers waits for a member to pass its contract's review. Passing it goes in one
// press, and sending the work back to planning says why, because the contract must change.
export function ContractReviewCard({ review, after }: { review: ContractReviewNeed; after: () => Promise<void> }) {
  const [asking, setAsking] = useState(false);
  const [reason, setReason] = useState("");
  const { act, busy, failure } = useAct(after);
  const id = review.work_order.id;
  const send = async () => {
    if (await act(() => sendBack(id, reason.trim()))) {
      setAsking(false);
      setReason("");
    }
  };
  return (
    <div className="card-body">
      <p className="lead">Its contract waits for a member's review, because no plan approval covers it.</p>
      <dl className="facts">
        <Fact name="Contract">{`${review.contract}, version ${review.version}`}</Fact>
        <Fact name="Work order">
          <Id value={id} />
        </Fact>
      </dl>
      {review.plan ? (
        <p>
          <Link to={addressOf("/plans", { plan: review.plan })}>Read the contract</Link>
        </p>
      ) : null}
      {asking ? (
        <form
          className="reason"
          onSubmit={(event) => {
            event.preventDefault();
            void send();
          }}
        >
          <label className="field">
            <span className="field-label">Why</span>
            <input type="text" value={reason} disabled={busy} required autoFocus onChange={(event) => setReason(event.target.value)} />
          </label>
          <div className="actions">
            <button className="button button-strong" type="submit" disabled={busy || reason.trim() === ""}>
              Send back to planning
            </button>
            <button className="button" type="button" disabled={busy} onClick={() => setAsking(false)}>
              Cancel
            </button>
          </div>
        </form>
      ) : (
        <div className="actions">
          <button className="button button-strong" type="button" disabled={busy} onClick={() => void act(() => passReview(id))}>
            Pass the review
          </button>
          <button className="button" type="button" disabled={busy} onClick={() => setAsking(true)}>
            Send back to planning
          </button>
        </div>
      )}
      {failure ? (
        <p className="refusal" role="alert">
          {failure}
        </p>
      ) : null}
    </div>
  );
}
