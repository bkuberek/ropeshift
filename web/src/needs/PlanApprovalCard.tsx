import { useId, useState } from "react";
import type { ReactNode } from "react";

import { decideApproval } from "../api/client";
import type { Adoption, ContractDifference, Difference, FileChange, PartDifference, PlanApprovalNeed } from "../api/types";
import { addressOf, Link } from "../app/route";
import { Fact, words } from "../parts/parts";
import { useAct } from "./useAct";

// What a plan holds, in the order a person reads a plan, and each kind's name for one and for many.
const kinds: [string, string, string][] = [
  ["requirements", "requirements document", "requirements documents"],
  ["spec", "spec", "specs"],
  ["architecture", "architecture document", "architecture documents"],
  ["experience-design", "experience design", "experience designs"],
  ["milestone", "milestone", "milestones"],
  ["contract", "contract", "contracts"],
];

function holdings(counts: Record<string, number>): string[] {
  const known = kinds.filter(([kind]) => counts[kind]).map(([kind, one, many]) => {
    const count = counts[kind] ?? 0;
    return `${count} ${count === 1 ? one : many}`;
  });
  const others = Object.keys(counts)
    .filter((kind) => !kinds.some(([name]) => name === kind))
    .map((kind) => `${counts[kind]} ${words(kind).toLowerCase()}`);
  return [...known, ...others];
}

// Refs is one line of a difference: what it says, then each record it names.
function Refs({ label, refs }: { label: string; refs?: string[] }) {
  if (!refs?.length) {
    return null;
  }
  return (
    <li className="difference-line">
      <span className="difference-label">{label}</span>
      <ul className="links">
        {refs.map((ref) => (
          <li key={ref} className="mono">
            {ref}
          </li>
        ))}
      </ul>
    </li>
  );
}

function partLines(name: string, part: PartDifference): string[] {
  const lines: string[] = [];
  for (const [verb, ids] of [
    ["added", part.added],
    ["changed", part.changed],
    ["removed", part.removed],
  ] as const) {
    if (ids?.length) {
      lines.push(`${name} ${verb}: ${ids.join(", ")}`);
    }
  }
  return lines;
}

function ContractLine({ contract }: { contract: ContractDifference }) {
  const lines = [...partLines("Criteria", contract.criteria), ...partLines("Work orders", contract.work_orders)];
  return (
    <li aria-label={contract.ref}>
      <span className="mono">{contract.ref}</span>
      <ul className="tags">
        {lines.map((line) => (
          <li key={line}>{line}</li>
        ))}
      </ul>
    </li>
  );
}

function Part({ title, children }: { title: string; children: ReactNode }) {
  const heading = useId();
  return (
    <section className="card-part" aria-labelledby={heading}>
      <h3 className="card-part-title" id={heading}>
        {title}
      </h3>
      {children}
    </section>
  );
}

// Changes says how this version differs from the one a member approved last, record by record, and for
// a contract that changed, which criteria and work orders did.
function Changes({ difference }: { difference: Difference }) {
  const base = difference.base ?? 0;
  const none = !difference.added?.length && !difference.changed?.length && !difference.removed?.length;
  return (
    <Part title={base === 0 ? "A first version" : `Since version ${base}`}>
      {base === 0 ? <p className="quiet">Nothing was approved before, so every record is new.</p> : null}
      {base !== 0 && none ? <p className="quiet">No record differs from version {base}.</p> : null}
      {none ? null : (
        <ul className="difference">
          <Refs label="Added" refs={difference.added} />
          <Refs label="Changed" refs={difference.changed} />
          <Refs label="Removed" refs={difference.removed} />
        </ul>
      )}
      {difference.contracts?.length ? (
        <ul className="difference" aria-label="Inside each contract">
          {difference.contracts.map((contract) => (
            <ContractLine key={contract.ref} contract={contract} />
          ))}
        </ul>
      ) : null}
    </Part>
  );
}

// Records is one group of an adopted plan, each record with what it says, or a word that it holds none.
function Records({ title, rows }: { title: string; rows: { key: string; label: string; text: string }[] }) {
  return (
    <Part title={title}>
      {rows.length ? (
        <ul className="links">
          {rows.map((row) => (
            <li key={row.key} aria-label={row.label}>
              <span className="mono">{row.label}</span> {row.text}
            </li>
          ))}
        </ul>
      ) : (
        <p className="quiet">Nothing.</p>
      )}
    </Part>
  );
}

function changeText(change: FileChange): string {
  return change.from ? `${change.status} ${change.from} to ${change.path}` : `${change.status} ${change.path}`;
}

// AdoptionGroups shows an adopted plan as one plan and its branch as one change (ADR-0023): every claim of
// what the code already does with its evidence, which approving it stands behind, each in-flight branch it
// continues, what it adds, what it leaves out and why, and every file the branch added, changed, or moved.
function AdoptionGroups({ adoption }: { adoption: Adoption }) {
  const joined = (label: string, items?: string[]) => (items?.length ? `${label} ${items.join(", ")}` : "");
  return (
    <>
      <Records
        title="What exists"
        rows={adoption.exists.map((record) => ({
          key: record.ref,
          label: record.ref,
          text: [record.title, joined("Evidence:", record.evidence), joined("Rests on:", record.cites)].filter(Boolean).join(". "),
        }))}
      />
      <Records
        title="What continues"
        rows={adoption.continues.map((work) => ({
          key: `${work.contract}/${work.work_order}`,
          label: work.work_order,
          text: `${work.title}, continuing ${work.branch}${work.repo ? ` in ${work.repo}` : ""} (${work.contract})`,
        }))}
      />
      <Records
        title="What is new"
        rows={adoption.new.map((record) => ({
          key: record.ref,
          label: record.ref,
          text: [record.title, joined("Work orders:", record.work_orders)].filter(Boolean).join(". "),
        }))}
      />
      <Records title="What is left out" rows={adoption.left_out.map((left) => ({ key: left.ref, label: left.ref, text: left.reason }))} />
      <Part title="What the branch changes">
        {adoption.changes_unread ? (
          <p className="quiet">The changes could not be read: {adoption.changes_unread}</p>
        ) : adoption.changes.length ? (
          <ul className="links">
            {adoption.changes.map((change) => (
              <li key={`${change.status} ${change.from ?? ""} ${change.path}`} className="mono">
                {changeText(change)}
              </li>
            ))}
          </ul>
        ) : (
          <p className="quiet">Nothing.</p>
        )}
      </Part>
    </>
  );
}

// A plan is approved as one step: the digest sent is the one on this page, so a version that changed
// since this read is refused, and approving decides every draft with the member as the one who decided.
export function PlanApprovalCard({ id, need, after }: { id: string; need: PlanApprovalNeed; after: () => Promise<void> }) {
  const [reason, setReason] = useState("");
  const { act, busy, failure } = useAct(after);
  const approval = need.approval;
  const decide = (allow: boolean) => act(() => decideApproval(id, approval.subject_digest, allow, reason.trim()));
  const holds = holdings(need.counts);
  return (
    <div className="card-body">
      <p className="lead">
        Approving it decides each draft decision as yours, and passes the contract review of the work it covers, which then needs no
        review of its own.
      </p>
      <dl className="facts">
        <Fact name="Requested by" mono>
          {need.requested_by}
        </Fact>
        <Fact name="Version">{need.version}</Fact>
        <Fact name="Digest" mono>
          <span title={approval.subject_digest}>{approval.subject_digest.slice(0, 16)}</span>
        </Fact>
      </dl>
      {approval.plan_id ? (
        <p>
          <Link to={addressOf("/plans", { plan: approval.plan_id })}>Read the plan</Link>
        </p>
      ) : null}
      {holds.length ? (
        <ul className="tags" aria-label="What it holds">
          {holds.map((line) => (
            <li key={line}>{line}</li>
          ))}
        </ul>
      ) : null}
      {need.adoption ? <AdoptionGroups adoption={need.adoption} /> : null}
      <Changes difference={need.difference} />
      <Part title="Draft decisions">
        {need.drafts.length ? (
          <ol className="drafts" aria-label="Draft decisions">
            {need.drafts.map((draft) => (
              <li key={draft.id} className="draft">
                <p className="question-text">{draft.question}</p>
                <p>{draft.answer}</p>
                {draft.rationale ? <p className="quiet">{draft.rationale}</p> : null}
              </li>
            ))}
          </ol>
        ) : (
          <p className="quiet">It drafts no decision.</p>
        )}
      </Part>
      <label className="field">
        <span className="field-label">Reason, which the plan's author reads (optional)</span>
        <input type="text" value={reason} disabled={busy} onChange={(event) => setReason(event.target.value)} />
      </label>
      {failure ? (
        <p className="refusal" role="alert">
          {failure}
        </p>
      ) : null}
      <div className="actions">
        <button className="button button-strong" type="button" disabled={busy} onClick={() => void decide(true)}>
          Approve this plan
        </button>
        <button className="button" type="button" disabled={busy} onClick={() => void decide(false)}>
          Deny
        </button>
      </div>
    </div>
  );
}
