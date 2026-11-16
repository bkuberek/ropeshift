import { readLaneTelemetry } from "../api/admin";
import type { LaneTelemetry, LaneTelemetryList } from "../api/types";
import { useView } from "../api/useView";
import type { Live } from "../api/useView";
import { timeOf } from "../admin/parts";
import { Absent, Banner, EmptyState, Id, Panel, SkeletonRows, TableRegion } from "../ui/display";

const count = (value: number) => value.toLocaleString("en-US");

// said is a lane's tokens as `ropeshift telemetry lanes` prints them: each type in order of its name, then its count.
function said(tokens: Record<string, number>): string {
  return Object.keys(tokens)
    .sort()
    .map((kind) => `${kind} ${count(tokens[kind] ?? 0)}`)
    .join(", ");
}

// dollars says money in dollars and cents. A cost too small to show in cents says so, and does not read as nothing.
function dollars(value: number): string {
  return value > 0 && value < 0.005 ? "under $0.01" : `$${value.toFixed(2)}`;
}

// CostOf says what a lane's agent reported it cost. It is a lower bound, because a turn a kill stopped reports
// nothing, so a figure reads "at least". A lane that reported no cost says so, and is never drawn as a cost of zero.
function CostOf({ lane }: { lane: LaneTelemetry }) {
  return lane.cost_usd > 0 ? <>at least {dollars(lane.cost_usd)}</> : <Absent>none reported</Absent>;
}

// TokensOf lists a lane's tokens by type and, under them, what each roster agent that spent some used. An agent's
// tokens are part of the lane's, and are not added to them.
function TokensOf({ lane }: { lane: LaneTelemetry }) {
  const agents = Object.entries(lane.agents ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const tokens = Object.keys(lane.tokens).length > 0 ? said(lane.tokens) : null;
  return (
    <>
      {tokens ?? <Absent>none reported</Absent>}
      {agents.map(([agent, spent]) => (
        <span className="cell-note" key={agent}>
          {agent}: {said(spent)}
        </span>
      ))}
    </>
  );
}

// LaneCosts is the cost of each lane on the Usage page, from the route `ropeshift telemetry lanes` reads: each
// lane's context at its last call, its tokens by type, and the cost its agent reported. A control plane with no
// telemetry store says so, and a store that holds nothing lists no lane. Neither reads as a cost of zero.
export function LaneCosts({ live }: { live: Live }) {
  const read = useView<LaneTelemetryList>(() => readLaneTelemetry(), "usage:lanes", live);
  const view = read.view;
  const lanes = view?.lanes ?? [];
  return (
    <>
      {read.failure ? <Banner tone="danger">{view ? `The lane costs are not current. ${read.failure}` : read.failure}</Banner> : null}
      {!view && !read.failure ? (
        <div className="panel">
          <SkeletonRows rows={3} />
        </div>
      ) : null}
      {view && !view.available ? (
        <Panel title="Estimated cost by lane">
          <EmptyState icon="gauge" title="This control plane reads no telemetry store">
            Lane costs come from the store a worker keeps beside the control plane. Without one no lane's tokens or cost can be read, and none is drawn as zero.
          </EmptyState>
        </Panel>
      ) : null}
      {view?.available && lanes.length === 0 ? (
        <Panel title="Estimated cost by lane">
          <EmptyState icon="gauge" title="No lane has reported its tokens or cost">
            A lane's tokens and cost show here once its agent reports them, and a lane that reported nothing is never drawn as a cost of zero.
          </EmptyState>
        </Panel>
      ) : null}
      {view?.available && lanes.length > 0 ? (
        <Panel
          title="Estimated cost by lane"
          flush
          foot="Costs are estimated at list price from what each lane's agent reported, and are not a bill. A turn a kill stopped reports nothing, so a cost is a lower bound. An agent's tokens are part of its lane's, and are not added to them."
        >
          <TableRegion label="Estimated cost by lane">
            <table className="table stack-on-phone">
              <caption className="visually-hidden">Estimated cost by lane</caption>
              <thead>
                <tr>
                  <th scope="col">Lane</th>
                  <th scope="col" className="r">
                    Context
                  </th>
                  <th scope="col">Tokens</th>
                  <th scope="col" className="r">
                    Est. cost
                  </th>
                  <th scope="col">Last call</th>
                </tr>
              </thead>
              <tbody>
                {lanes.map((lane) => (
                  <tr key={lane.lane}>
                    <td data-label="Lane">
                      <Id value={lane.lane} />
                    </td>
                    <td data-label="Context" className="r num">
                      {count(lane.context_tokens)} tokens
                    </td>
                    <td data-label="Tokens">
                      <TokensOf lane={lane} />
                    </td>
                    <td data-label="Est. cost" className="r num">
                      <CostOf lane={lane} />
                    </td>
                    <td data-label="Last call" className="num">
                      {timeOf(lane.last_at)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableRegion>
        </Panel>
      ) : null}
    </>
  );
}
