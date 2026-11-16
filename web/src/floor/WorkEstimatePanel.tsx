import { readEstimate } from "../api/measures";
import type { WorkEstimate } from "../api/measures";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { WorkEstimateView } from "../measures/Estimate";
import { Banner, Panel, SkeletonRows } from "../ui/display";

// WorkEstimatePanel is the side of a work order's page that shows its estimate beside its baseline, as `ropeshift
// work show` prints them. The side is narrow, so each band keeps its badge and holds its explanation under a
// control; the Activity tab shows both in full.
export function WorkEstimatePanel({ id, live }: { id: string; live: Live }) {
  const read = useView<WorkEstimate>(() => readEstimate(id), `work-order:${id}:estimate`, live);
  return (
    <Panel title="Estimate" level={2}>
      {read.failure ? <Banner tone="danger">{read.view ? `This is not current. ${read.failure}` : read.failure}</Banner> : null}
      {read.view ? <WorkEstimateView read={read.view} compact /> : read.failure ? null : <SkeletonRows rows={2} />}
    </Panel>
  );
}
