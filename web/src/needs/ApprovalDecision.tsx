import type { Place } from "../api/client";
import type { Approval } from "../api/types";
import { ApprovalCard } from "./ApprovalCard";
import type { Decided } from "./decided";
import type { Frame } from "./frame";
import { MergeCard } from "./MergeCard";
import { RecoveryCard } from "./RecoveryCard";

type Props = { approval: Approval; after: () => Promise<void>; frame: Frame; compact?: boolean; place?: Place; onDecided?: (decided: Decided) => void };

// ApprovalDecision is the card of a request a member decides: a recovery act an orchestrator asked for, the
// merge of a reviewed commit, or a call a lane asked to run. Each asks something else of a member, so each
// has its own card, wherever a request shows.
export function ApprovalDecision(props: Props) {
  switch (props.approval.kind) {
    case "recovery":
      return <RecoveryCard {...props} />;
    case "merge":
      return <MergeCard {...props} />;
    default:
      return <ApprovalCard {...props} />;
  }
}
