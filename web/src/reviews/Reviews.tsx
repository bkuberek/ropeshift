import { readView } from "../api/client";
import type { NeedsYou, Operations } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { addressOf } from "../app/route";
import { useWorkIndex } from "../floor/workIndex";
import { Page, StampLine, useClock, ViewTime } from "../parts/parts";
import { LinkTabs } from "../ui/controls";
import { Banner, SkeletonRows } from "../ui/display";
import { Agreement } from "./Agreement";
import { PullRequests } from "./PullRequests";
import { pullRequestsOf, queueOf } from "./inReview";
import { Queue } from "./Queue";

export type ReviewsTab = "queue" | "pull-requests" | "agreement";

export const reviewsTabs: { id: ReviewsTab; label: string }[] = [
  { id: "queue", label: "Queue" },
  { id: "pull-requests", label: "Pull requests" },
  { id: "agreement", label: "Agreement" },
];

// tabOf is the tab an address names, and the queue for any other word.
export const tabOf = (word: string): ReviewsTab => reviewsTabs.find((tab) => tab.id === word)?.id ?? "queue";

// ReviewsPage is the work that waits for a member after its lane finished: the queue of work waiting for
// acceptance or a merge, the pull requests it lands through, and how often the member agreed with the review
// (FR-CAL-2). What the queue offers each row is what the read model lists, never a rule of this page.
export function ReviewsPage({ live, tab, after }: { live: Live; tab: string; after: () => Promise<void> }) {
  const chosen = tabOf(tab);
  const { index, failure, busy } = useWorkIndex(live);
  // The requests that wait on a member in this project say which acts each row has.
  const needs = useView<NeedsYou>(() => readView<NeedsYou>("needs-you"), "reviews:needs", live);
  // The measures hold the agreement, and are read only while that tab is open.
  const measures = useView<Operations | null>(() => (chosen === "agreement" ? readView<Operations>("operations") : Promise.resolve(null)), `reviews:operations:${chosen}`, live);
  const now = useClock();
  const rows = index?.rows ?? [];
  const queue = queueOf(rows, needs.view?.items ?? []);
  const requests = pullRequestsOf(rows);
  const problem = failure || needs.failure || measures.failure;
  return (
    <Page title="Reviews" note="Work that waits for your acceptance or a merge, the pull requests it lands through, and how often you agree with the review.">
      <LinkTabs
        label="Reviews"
        tabs={reviewsTabs.map((row) => ({
          to: addressOf("/reviews", { tab: row.id === "queue" ? "" : row.id }),
          label: row.label,
          count: !index ? undefined : row.id === "queue" ? queue.length : row.id === "pull-requests" ? requests.length : undefined,
          current: row.id === chosen,
        }))}
      />
      <div className="tab-body">
        {problem ? <Banner tone="danger">{index ? `This view is not current. ${problem}` : problem}</Banner> : null}
        {index && needs.view ? (
          <ViewTime.Provider value={{ asOf: index.stamp.as_of, elapsed: Math.max(0, now - index.readAt) }}>
            <div className="view-body stack" aria-busy={busy}>
              {chosen === "queue" ? <Queue rows={queue} after={after} /> : null}
              {chosen === "pull-requests" ? <PullRequests rows={requests} /> : null}
              {chosen === "agreement" && measures.view ? <Agreement report={measures.view.measures} repositories={index.repositories} /> : null}
              {chosen === "agreement" && !measures.view && !measures.failure ? <SkeletonRows rows={4} /> : null}
            </div>
            <StampLine stamp={index.stamp} />
          </ViewTime.Provider>
        ) : problem ? null : (
          <div className="panel">
            <SkeletonRows rows={5} />
          </div>
        )}
      </div>
    </Page>
  );
}
