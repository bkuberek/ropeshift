import { useState } from "react";

import type { Documents, PlanDocuments, RequirementTrace, TracedCriterion } from "../api/types";
import { addressOf, Link } from "../app/route";
import { requirementStates, verdictStates } from "../floor/states";
import { FilterChip } from "../ui/controls";
import { Digest, EmptyState, Panel, StateBadge, TableRegion, WorkBadge } from "../ui/display";
import { Select } from "../ui/forms";
import { documentsAddress, filterRows, requirementStatus, rowsOf, statusFilters, workOf } from "./requirementTrace";
import type { Row } from "./requirementTrace";

// A requirement's criteria, gathered under the contract that holds them, in the order they came.
type TracedContract = { contract: string; title: string; version: number; criteria: TracedCriterion[] };

function byContract(criteria: TracedCriterion[]): TracedContract[] {
  const out: TracedContract[] = [];
  for (const criterion of criteria) {
    const held = out.find((row) => row.contract === criterion.contract && row.version === criterion.contract_version);
    if (held) {
      held.criteria.push(criterion);
    } else {
      out.push({ contract: criterion.contract, title: criterion.contract_title, version: criterion.contract_version, criteria: [criterion] });
    }
  }
  return out;
}

// TraceBody follows one requirement to what serves it: its criteria, with their contract and version, then
// the work orders that carry them with where each stands, then the verdicts that speak for that work with
// the commit each read. A requirement existing code meets shows its evidence where the verdicts would be.
export function TraceBody({ trace, plan }: { trace: RequirementTrace; plan?: PlanDocuments["plan"] }) {
  const status = requirementStatus(trace);
  const work = workOf(trace);
  const judged = trace.criteria.flatMap((criterion) =>
    criterion.work_orders.filter((order) => order.verdict).map((order) => ({ criterion: criterion.criterion, order, verdict: order.verdict! })),
  );
  return (
    <div className="trace small">
      <div className="trace-node">
        <p className="overline">Requirement</p>
        <p>{trace.text}</p>
        <p className="row-tight">
          <StateBadge table={requirementStates} state={status} />
          {plan ? <Link to={addressOf("/plans", { plan: plan.id })}>{plan.title}</Link> : null}
        </p>
      </div>
      <div className="trace-node">
        <p className="overline">Criteria</p>
        {trace.criteria.length ? (
          byContract(trace.criteria).map((row) => (
            <div key={`${row.contract}@${row.version}`} className="stack-8">
              {row.criteria.map((criterion) => (
                <p key={criterion.criterion}>
                  <span className="chip mono">{criterion.criterion}</span> {criterion.text}
                </p>
              ))}
              <p className="subtle">
                Contract <span className="mono">{row.contract}</span> v{row.version} {row.title}
              </p>
            </div>
          ))
        ) : trace.status === "met" ? (
          <p className="muted">None. The code already meets it.</p>
        ) : (
          <p className="trace-gap">No criteria trace to this requirement.</p>
        )}
      </div>
      <div className="trace-node">
        <p className="overline">Work orders</p>
        {work.length ? (
          <ul className="stack-8">
            {work.map((order) => (
              <li key={order.id} className="row">
                <Link to={addressOf("/work-order", { id: order.id })}>{order.title || order.id}</Link>
                <WorkBadge state={order.state} />
              </li>
            ))}
          </ul>
        ) : (
          <p className="muted">No work carries it yet.</p>
        )}
      </div>
      <div className="trace-node">
        {trace.status === "met" ? (
          <>
            <p className="overline">Evidence</p>
            {trace.evidence?.length ? (
              <ul className="stack-8" aria-label={`Evidence that the code meets ${trace.key}`}>
                {trace.evidence.map((cited) => (
                  <li key={cited} className="mono">
                    {cited}
                  </li>
                ))}
              </ul>
            ) : (
              <p className="muted">The plan cites none.</p>
            )}
          </>
        ) : (
          <>
            <p className="overline">Verdicts</p>
            {judged.length ? (
              <ul className="stack-8">
                {judged.map(({ criterion, order, verdict }) => (
                  <li key={`${criterion}:${order.id}`} className="row">
                    <StateBadge table={verdictStates} state={verdict.verdict} />
                    <span>
                      <span className="mono">{criterion}</span> on <Digest value={verdict.commit} length={7} />
                    </span>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="subtle">none yet</p>
            )}
          </>
        )}
      </div>
    </div>
  );
}

// TracePanel is the panel beside the reader for the requirement the address selects.
export function TracePanel({ view, keyed }: { view: Documents; keyed: string }) {
  const row = rowsOf(view.plans).find((candidate) => candidate.trace.key === keyed);
  const known = view.requirements.find((requirement) => requirement.key === keyed);
  return (
    <Panel title={keyed ? `Trace · ${keyed}` : "Trace"} className="trace-panel">
      {row ? (
        <TraceBody trace={row.trace} plan={row.plan} />
      ) : known ? (
        <TraceBody trace={{ key: known.key, text: known.text, document: "", criteria: [], status: known.status, evidence: known.evidence }} />
      ) : (
        <p className="muted">{keyed ? `No requirement ${keyed} is recorded.` : "Select a requirement to follow it to its criteria, work, and verdicts."}</p>
      )}
    </Panel>
  );
}

// Evidence is what stands in the matrix where a verdict would: the latest verdict of the requirement's work,
// or the evidence for existing code, or the word that there is none yet.
function Latest({ row }: { row: Row }) {
  if (row.trace.status === "met") {
    return row.trace.evidence?.length ? (
      <ul className="row-tight">
        {row.trace.evidence.map((cited) => (
          <li key={cited} className="chip mono">
            {cited}
          </li>
        ))}
      </ul>
    ) : (
      <span className="unavailable">no evidence cited</span>
    );
  }
  const latest = row.verdicts.at(-1);
  return latest ? <StateBadge table={verdictStates} state={latest.verdict} /> : <span className="unavailable">none yet</span>;
}

// Matrix is the traceability tab: a requirement on each row with where it stands, the criteria that serve it,
// its work orders, and its latest verdict or evidence, filtered by status and by plan.
export function Matrix({ view, keyed }: { view: Documents; keyed: string }) {
  const [status, setStatus] = useState("");
  const [plan, setPlan] = useState("");
  const rows = rowsOf(view.plans);
  const shown = filterRows(rows, { status, plan });
  const count = (state: string) => rows.filter((row) => row.status === state && (!plan || row.plan.id === plan)).length;
  if (rows.length === 0) {
    return (
      <div className="panel">
        <EmptyState icon="trace" title="No requirement is traced yet">
          Requirements appear here once a plan records a requirements document.
        </EmptyState>
      </div>
    );
  }
  return (
    <div className={keyed ? "grid grid-main-side" : undefined}>
      <div className="panel">
        <div className="panel-head">
          <h2>Requirements</h2>
          <span className="subtle">
            {shown.length} of {rows.length}
          </span>
        </div>
        <div className="panel-body filters">
          <div className="filter-row" role="group" aria-label="Status">
            <FilterChip pressed={status === ""} count={plan ? rows.filter((row) => row.plan.id === plan).length : rows.length} onClick={() => setStatus("")}>
              All
            </FilterChip>
            {statusFilters.map((state) => (
              <FilterChip key={state} pressed={status === state} count={count(state)} onClick={() => setStatus(status === state ? "" : state)}>
                {requirementStates[state]?.label ?? state}
              </FilterChip>
            ))}
          </div>
          {view.plans.length > 1 ? (
            <label className="row-tight">
              <span className="small muted">Plan</span>
              <Select small aria-label="Plan" value={plan} onChange={(event) => setPlan(event.target.value)}>
                <option value="">Every plan</option>
                {view.plans.map((candidate) => (
                  <option key={candidate.plan.id} value={candidate.plan.id}>
                    {candidate.plan.title}
                  </option>
                ))}
              </Select>
            </label>
          ) : null}
        </div>
        <TableRegion label="Traceability matrix">
          <table className="table stack-on-phone matrix">
            <thead>
              <tr>
                <th scope="col">Requirement</th>
                <th scope="col">Status</th>
                <th scope="col">Criteria</th>
                <th scope="col">Work orders</th>
                <th scope="col">Latest verdict or evidence</th>
              </tr>
            </thead>
            <tbody>
              {shown.map((row) => (
                <tr key={`${row.plan.id}:${row.trace.key}`} aria-selected={row.trace.key === keyed || undefined} className={row.trace.key === keyed ? "selected" : undefined}>
                  <td data-label="Requirement">
                    <Link to={documentsAddress("traceability", row.trace.key)} stay className="id" aria-current={row.trace.key === keyed ? "true" : undefined}>
                      {row.trace.key}
                    </Link>
                    <span className="small muted"> {row.trace.text}</span>
                  </td>
                  <td data-label="Status">
                    <StateBadge table={requirementStates} state={row.status} />
                  </td>
                  <td data-label="Criteria">{row.criteria.length ? row.criteria.join(", ") : <span className="unavailable">none</span>}</td>
                  <td data-label="Work orders">{row.work.length ? row.work.map((order) => order.title || order.id).join(", ") : <span className="unavailable">none</span>}</td>
                  <td data-label="Verdict">
                    <Latest row={row} />
                  </td>
                </tr>
              ))}
              {shown.length === 0 ? (
                <tr>
                  <td colSpan={5} className="muted">
                    No requirement matches these filters.
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </TableRegion>
      </div>
      {keyed ? <TracePanel view={view} keyed={keyed} /> : null}
    </div>
  );
}
