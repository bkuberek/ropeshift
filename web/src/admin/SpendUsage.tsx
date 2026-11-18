import type { ReactNode } from "react";

import { readSpend } from "../api/admin";
import type { ProjectWindowSpend, SpendView, WorkOrderSpend } from "../api/admin";
import type { Live } from "../api/useView";
import type { StateLook } from "../floor/states";
import { Badge, EmptyState, Id, Panel, TableRegion } from "../ui/display";
import { Reading, useRead } from "./parts";

// What a Spend section shows above its form (FR-SPEND-18): what the project's lanes were charged today and this week, and
// what each open work order was charged, each against the limits in force. A limit of 0 is none, and says so.

const dollars = (value: number) => `$${value.toFixed(2)}`;
const limitWords = (value: number) => (value > 0 ? dollars(value) : "none");

// limitState says where charged spend stands against a soft and a hard limit. A scope with no limit is not under one.
export function limitState(spent: number, soft: number, hard: number): StateLook {
  if (hard > 0 && spent >= hard) {
    return { label: "Past the hard limit", tone: "danger", glyph: "cross" };
  }
  if (soft > 0 && spent >= soft) {
    return { label: "Past the soft limit", tone: "warning", glyph: "triangle" };
  }
  if (soft > 0 || hard > 0) {
    return { label: "Under its limits", tone: "success", glyph: "check" };
  }
  return { label: "No limit set", tone: "neutral", glyph: "ring" };
}

type Row = { key: string; scope: ReactNode; note?: string; spent: number; soft: number; hard: number; remaining: number; raised: number };

const windowRow = (window: ProjectWindowSpend): Row => ({
  key: window.window,
  scope: window.window === "day" ? "Today" : "This week",
  spent: window.spent_usd,
  soft: window.soft_limit,
  hard: window.hard_limit,
  remaining: window.remaining,
  raised: window.raised_to ?? 0,
});

const orderRow = (order: WorkOrderSpend): Row => ({
  key: order.work_order,
  scope: <Id value={order.work_order} />,
  note: `${order.kind}, ${order.label}`,
  spent: order.charged.charged_usd,
  soft: order.soft_limit,
  hard: order.hard_limit,
  remaining: order.remaining,
  raised: order.raised_to ?? 0,
});

function SpendTable({ label, first, rows }: { label: string; first: string; rows: Row[] }) {
  return (
    <TableRegion label={label}>
      <table className="table stack-on-phone">
        <caption className="visually-hidden">{label}</caption>
        <thead>
          <tr>
            <th scope="col">{first}</th>
            <th scope="col" className="r">
              Charged
            </th>
            <th scope="col" className="r">
              Soft limit
            </th>
            <th scope="col" className="r">
              Hard limit
            </th>
            <th scope="col" className="r">
              Remaining
            </th>
            <th scope="col">Standing</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => {
            const look = limitState(row.spent, row.soft, row.hard);
            return (
              <tr key={row.key}>
                <td data-label={first}>
                  {row.scope}
                  {row.note ? <span className="cell-note">{row.note}</span> : null}
                </td>
                <td data-label="Charged" className="r num">
                  {dollars(row.spent)}
                </td>
                <td data-label="Soft limit" className="r num">
                  {limitWords(row.soft)}
                </td>
                <td data-label="Hard limit" className="r num">
                  {limitWords(row.hard)}
                  {row.raised > 0 ? <span className="cell-note">raised by a member to {dollars(row.raised)}</span> : null}
                </td>
                <td data-label="Remaining" className="r num">
                  {row.hard > 0 ? dollars(row.remaining) : "none"}
                </td>
                <td data-label="Standing">
                  <Badge tone={look.tone} glyph={look.glyph}>
                    {look.label}
                  </Badge>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </TableRegion>
  );
}

// SpendUsage reads the project's spend and lays it out, and reads it again when the section saves a version (again), because
// the limits of every work order follow a new version at once.
export function SpendUsage({ live, project, again }: { live: Live; project: string; again: number }) {
  const read = useRead<SpendView>(() => readSpend(), `spend:${project}:${again}`, live);
  const view = read.view;
  const orders = view?.work_orders ?? [];
  return (
    <Reading failure={read.failure} ready={view !== null} rows={3}>
      {view ? (
        <>
          <Panel title="Charged spend" flush foot="An estimate at list price, and not a bill. A work order's line says how many turns were estimated by average.">
            <SpendTable label="Charged spend of the project" first="Window" rows={[windowRow(view.project_spend.day), windowRow(view.project_spend.week)]} />
          </Panel>
          <Panel title="Open plans and work orders" flush>
            {orders.length === 0 ? (
              <EmptyState icon="gauge" title="No open work order has a spend limit">
                A work order's limits show here while it is open.
              </EmptyState>
            ) : (
              <SpendTable label="Charged spend of each open work order" first="Work order" rows={orders.map(orderRow)} />
            )}
          </Panel>
        </>
      ) : null}
    </Reading>
  );
}
