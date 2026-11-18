import { readWorkOrderSpend } from "../api/spend";
import type { WorkOrderSpend } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { limitBasisWords, money } from "../needs/spendWords";
import { Absent, Banner, Fact, Facts, Panel, SkeletonRows } from "../ui/display";

// SpendReading is a work order's charged spend against its soft and hard limits (FR-SPEND-16): the figures, with
// where the limits came from, and a meter that reads the charge against the hard limit, so a person sees how near the
// work is to the stop. A limit of 0 is no limit, and a work order with none shows only what it is charged.
export function SpendReading({ spend }: { spend: WorkOrderSpend }) {
  const limited = spend.hard_limit > 0;
  const past = (spend.conditions ?? []).includes("spend_limit");
  const over = (spend.conditions ?? []).includes("over_estimate");
  return (
    <>
      {limited ? (
        <meter
          className="spend-meter"
          min={0}
          max={spend.hard_limit}
          low={spend.soft_limit > 0 ? spend.soft_limit : undefined}
          high={spend.soft_limit > 0 ? spend.soft_limit : undefined}
          optimum={0}
          value={Math.min(spend.charged.charged_usd, spend.hard_limit)}
          aria-label={`Charged ${money(spend.charged.charged_usd)} of a hard limit of ${money(spend.hard_limit)}`}
        />
      ) : null}
      <Facts>
        <Fact name="Charged">
          {money(spend.charged.charged_usd)}
          <span className="muted small">{` ${spend.label}`}</span>
        </Fact>
        <Fact name="Soft limit">{spend.soft_limit > 0 ? money(spend.soft_limit) : <Absent>none</Absent>}</Fact>
        <Fact name="Hard limit">
          {limited ? money(spend.hard_limit) : <Absent>none</Absent>}
          {spend.raised_to ? " (raised by a member)" : ""}
        </Fact>
        {limited ? <Fact name="Remaining">{money(spend.remaining)}</Fact> : null}
        {spend.basis && limited ? <Fact name="Limits from">{limitBasisWords[spend.basis]}</Fact> : null}
      </Facts>
      {past ? <p className="small">It is at its hard limit, so its lane waits for a raise or a person.</p> : over ? <p className="small muted">It is past its soft limit, which warns and stops nothing.</p> : null}
    </>
  );
}

// SpendPanel is the side of a work order's page, and of its lanes' pages, that shows what the work order is charged
// against its limits, as `ropeshift spend --work-order` prints it.
export function SpendPanel({ id, live }: { id: string; live: Live }) {
  const read = useView<WorkOrderSpend>(() => readWorkOrderSpend(id), `work-order:${id}:spend`, live);
  return (
    <Panel title="Spend" level={2}>
      {read.failure ? <Banner tone="danger">{read.view ? `This is not current. ${read.failure}` : read.failure}</Banner> : null}
      {read.view ? <SpendReading spend={read.view} /> : read.failure ? null : <SkeletonRows rows={2} />}
    </Panel>
  );
}
