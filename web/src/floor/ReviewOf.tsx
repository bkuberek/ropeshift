import type { WorkOrder } from "../api/types";
import { addressOf, Link } from "../app/route";
import { Id } from "../ui/display";

// ReviewOf says which work a review judges, and links to it. It says nothing for work that is not a review.
export function ReviewOf({ order }: { order: WorkOrder }) {
  if (order.kind !== "review" || !order.reviews) {
    return null;
  }
  return (
    <span className="cell-note">
      Review of{" "}
      <Link to={addressOf("/work-order", { id: order.reviews })} className="id-link">
        <Id value={order.reviews} />
      </Link>
    </span>
  );
}
