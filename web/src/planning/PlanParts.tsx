import type { ReactNode } from "react";

import { readRecorded, readView } from "../api/client";
import type { Adoption, ContractHistory, Difference, DraftDecision, Plans, PlanEntry, WorkOrder } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { addressOf, Link } from "../app/route";
import { Badge, Banner } from "../ui/display";
import { Icon } from "../ui/Icon";
import { kindWord } from "./documentTree";
import { changeLines, refKind, refName } from "./planChanges";
import type { Change } from "./planChanges";

const changeLook: Record<Change, { tone: "success" | "active" | "neutral"; glyph: "dot" | "half" | "cross"; label: string }> = {
  added: { tone: "success", glyph: "dot", label: "Added" },
  changed: { tone: "active", glyph: "half", label: "Changed" },
  removed: { tone: "neutral", glyph: "cross", label: "Removed" },
};

const kindWords: Record<string, string> = { plan: "Plan", goal: "Goal", contract: "Contract", milestone: "Milestone" };
const kindName = (kind: string) => kindWords[kind] ?? kindWord(kind);

// DocumentDifference links a recorded document to the difference between its versions: its newest version
// against the one before it, or just its reading when it has one version. It reads the document to learn
// its versions, so the link stands once that read is back.
function DocumentDifference({ id, live }: { id: string; live: Live }) {
  const read = useView(() => readRecorded(id), `plan-document:${id}`, live);
  const versions = read.view?.document.versions ?? [];
  const newest = versions[0];
  if (!newest) {
    return null;
  }
  const before = versions[1];
  return before ? (
    <Link to={addressOf("/documents", { id, version: String(newest.version), against: String(before.version) })}>Version {before.version} to {newest.version}</Link>
  ) : (
    <Link to={addressOf("/documents", { id, version: String(newest.version) })}>Read it</Link>
  );
}

// DifferenceBlock lists how a plan version differs from the last one a member approved: each record added,
// changed, or removed with a badge for its kind, and inside a changed contract the criteria and work orders
// that changed. A document that changed links to the difference between its versions.
export function DifferenceBlock({ difference, live }: { difference: Difference; live: Live }) {
  const base = difference.base ?? 0;
  const lines = changeLines(difference);
  return (
    <div className="stack">
      {base === 0 ? <p className="muted small">Nothing was approved before, so every record is new.</p> : null}
      {base !== 0 && lines.length === 0 ? <p className="muted small">No record differs from version {base}.</p> : null}
      {(["added", "changed", "removed"] as const).map((change) => {
        const rows = lines.filter((line) => line.change === change);
        if (rows.length === 0 && base === 0) {
          return null;
        }
        const look = changeLook[change];
        return (
          <div key={change}>
            <p className="overline diff-title">{look.label}</p>
            {rows.length ? (
              <ul className="stack-8 small">
                {rows.map((line) => (
                  <li key={line.ref}>
                    <Badge tone={look.tone} glyph={look.glyph}>
                      {kindName(line.kind)}
                    </Badge>{" "}
                    <span className="mono">{line.name}</span>
                    {line.kind && !["plan", "goal", "contract", "milestone"].includes(line.kind) ? (
                      <>
                        {" "}
                        <DocumentDifference id={line.ref} live={live} />
                      </>
                    ) : null}
                    {line.inside.length ? (
                      <ul className="tags" aria-label={`Inside ${line.ref}`}>
                        {line.inside.map((text) => (
                          <li key={text}>{text}</li>
                        ))}
                      </ul>
                    ) : null}
                  </li>
                ))}
              </ul>
            ) : (
              <p className="small muted">Nothing.</p>
            )}
          </div>
        );
      })}
    </div>
  );
}

// proposerWords names who raised a plan version's approval in words: intake read it from the plan's file, and a
// lane's request is a planning session's. Any other requester is named as recorded.
function proposerWords(requestedBy: string) {
  if (requestedBy === "system:intake") {
    return "intake, from the plan's file";
  }
  return requestedBy.startsWith("lane:") ? "the planning session" : requestedBy;
}

// DraftDecisions lists the answers a plan version drafts: the question, the answer, the rationale, and
// whether a blocking question stood behind it. They bind nothing until a member approves the plan, and the
// ledger records who the session asked, not who answered, so each says who proposed it.
export function DraftDecisions({ drafts, proposedBy }: { drafts: DraftDecision[]; proposedBy: string }) {
  if (drafts.length === 0) {
    return <p className="muted small">The version drafts no decision.</p>;
  }
  return (
    <ol className="drafts stack" aria-label="Draft decisions">
      {drafts.map((draft) => (
        <li key={draft.id} className="draft">
          <p className="item-title">
            <span className="mono">{draft.id}</span> {draft.answer}
            {draft.blocking ? (
              <>
                {" "}
                <Badge tone="warning" glyph="question">
                  Blocking
                </Badge>
              </>
            ) : null}
          </p>
          <p className="small muted">
            Asked: “{draft.question}”{draft.rationale ? ` Rationale: ${draft.rationale}` : ""}
          </p>
          <p className="small subtle">Proposed by {proposerWords(proposedBy)}. It becomes your decision when you approve.</p>
        </li>
      ))}
    </ol>
  );
}

// ChangedContracts lists the contracts a version adds or changes, each with its criteria, the requirements they
// serve as chips, and the work orders it carries with the agent each proposes. A first version holds them
// all.
export function ChangedContracts({ plan, difference, live }: { plan: string; difference: Difference; live: Live }) {
  const read = useView(() => readView<Plans>("plans"), "plan-contracts", live);
  const base = difference.base ?? 0;
  const named = new Set([...(difference.added ?? []), ...(difference.changed ?? [])].filter((ref) => refKind(ref) === "contract").map(refName));
  const plans = (read.view?.plans ?? []).filter((row) => row.plan.id === plan);
  const found: { history: ContractHistory; orders: WorkOrder[]; added: boolean }[] = [];
  for (const plan of plans) {
    for (const history of plan.contracts) {
      if (history.key && (base === 0 ? named.size === 0 || named.has(history.key) : named.has(history.key))) {
        found.push({ history, orders: plan.work_orders.filter((order) => order.contract_id === history.id), added: (difference.added ?? []).includes(`contract:${history.key}`) });
      }
    }
  }
  if (!plan) {
    return <p className="muted small">The ledger holds this plan's contracts once it is approved.</p>;
  }
  if (found.length === 0) {
    return <p className="muted small">{read.view ? "The version adds and changes no contract." : "Reading the contracts."}</p>;
  }
  return (
    <div className="stack">
      {found.map(({ history, orders, added }) => {
        const newest = history.versions.at(-1);
        if (!newest) {
          return null;
        }
        return (
          <section key={history.id} className="stack-8 small" aria-label={newest.title}>
            <p className="row">
              <strong>{newest.title}</strong>
              <Badge tone={added ? "success" : "active"} glyph={added ? "dot" : "half"}>
                {added ? "New" : "Changed"}
              </Badge>
              <span className="subtle">version {newest.version}</span>
            </p>
            {newest.criteria?.length ? (
              <ul className="stack-8">
                {newest.criteria.map((criterion) => (
                  <li key={criterion.id}>
                    <span className="mono">{criterion.id}</span> {criterion.text}{" "}
                    {criterion.requirements?.map((key) => (
                      <Link key={key} className="chip mono" to={addressOf("/documents", { kind: "requirements", key })}>
                        {key}
                      </Link>
                    ))}
                  </li>
                ))}
              </ul>
            ) : null}
            {orders.length ? (
              <p className="muted">
                Work orders:{" "}
                {orders.map((order, at) => (
                  <span key={order.id}>
                    {at ? " · " : ""}
                    {order.title || order.id}
                    {order.size || order.agent ? ` (${[order.size, order.agent].filter(Boolean).join(", ")})` : ""}
                  </span>
                ))}
              </p>
            ) : null}
          </section>
        );
      })}
    </div>
  );
}

// ChangedDocuments lists the documents a version covers, each with its digest, so what a member approves is
// what they read, and a link to the difference between the document's versions.
export function CoveredDocuments({ records, live }: { records: PlanEntry[]; live: Live }) {
  const documents = records.filter((entry) => entry.record);
  if (documents.length === 0) {
    return <p className="muted small">The version covers no document.</p>;
  }
  return (
    <ul className="stack-8 small">
      {documents.map((entry) => (
        <li key={entry.ref} className="row">
          <Badge tone="neutral" glyph="ring">
            {kindName(refKind(entry.ref))}
          </Badge>
          <Link className="mono" to={addressOf("/documents", { id: entry.record ?? entry.ref })}>
            {refName(entry.ref)}
          </Link>
          <DocumentDifference id={entry.record ?? entry.ref} live={live} />
        </li>
      ))}
    </ul>
  );
}

const changeBadge: Record<string, { tone: "success" | "active" | "neutral" | "danger"; glyph: "dot" | "half" | "arrow" | "cross"; label: string }> = {
  added: { tone: "success", glyph: "dot", label: "Added" },
  changed: { tone: "active", glyph: "half", label: "Changed" },
  moved: { tone: "active", glyph: "arrow", label: "Moved" },
  deleted: { tone: "danger", glyph: "cross", label: "Deleted" },
};

function Group({ title, empty, children }: { title: string; empty: boolean; children: ReactNode }) {
  return (
    <div>
      <h3 className="overline diff-title">{title}</h3>
      {empty ? <p className="small muted">Nothing.</p> : children}
    </div>
  );
}

// AdoptionGroups shows an adopted plan as the API groups it (ADR-0023): every claim of what the code already
// does with its evidence, each in-flight branch the plan continues, what it adds, and what it leaves out with
// each reason; then the adoption branch's changed files as one change, or why they could not be read.
export function AdoptionGroups({ adoption }: { adoption: Adoption }) {
  return (
    <div className="stack">
      <Group title="What exists" empty={adoption.exists.length === 0}>
        <ul className="stack-8 small">
          {adoption.exists.map((record) => (
            <li key={record.ref}>
              <span className="mono">{record.ref}</span> {record.title}
              {record.evidence?.length ? (
                <ul className="row-tight" aria-label={`Evidence for ${record.ref}`}>
                  {record.evidence.map((cited) => (
                    <li key={cited} className="chip mono">
                      {cited}
                    </li>
                  ))}
                </ul>
              ) : null}
              {record.cites?.length ? <p className="subtle">Rests on {record.cites.join(", ")}</p> : null}
            </li>
          ))}
        </ul>
      </Group>
      <Group title="What continues" empty={adoption.continues.length === 0}>
        <ul className="stack-8 small">
          {adoption.continues.map((work) => (
            <li key={`${work.contract}/${work.work_order}`}>
              <span className="mono">{work.work_order}</span> {work.title}{" "}
              <span className="subtle">
                continues <span className="mono">{work.branch}</span>
                {work.repo ? ` in ${work.repo}` : ""} ({work.contract})
              </span>
            </li>
          ))}
        </ul>
      </Group>
      <Group title="What is new" empty={adoption.new.length === 0}>
        <ul className="stack-8 small">
          {adoption.new.map((record) => (
            <li key={record.ref}>
              <span className="mono">{record.ref}</span> {record.title}
              {record.work_orders?.length ? <span className="subtle"> · work orders {record.work_orders.join(", ")}</span> : null}
            </li>
          ))}
        </ul>
      </Group>
      <Group title="What is left out" empty={adoption.left_out.length === 0}>
        <ul className="stack-8 small">
          {adoption.left_out.map((left) => (
            <li key={left.ref}>
              <span className="mono">{left.ref}</span> {left.reason}
            </li>
          ))}
        </ul>
      </Group>
      <div>
        <h3 className="overline diff-title">The adoption branch, as one change</h3>
        {adoption.changes_unread ? (
          <Banner tone="neutral">The changes could not be read: {adoption.changes_unread}</Banner>
        ) : adoption.changes.length ? (
          <ul className="stack-8 small" aria-label="Files the branch changes">
            {adoption.changes.map((change) => {
              const look = changeBadge[change.status] ?? { tone: "neutral" as const, glyph: "dot" as const, label: change.status };
              return (
                <li key={`${change.status} ${change.from ?? ""} ${change.path}`} className="row">
                  <Badge tone={look.tone} glyph={look.glyph}>
                    {look.label}
                  </Badge>
                  <span className="mono truncate" title={change.path}>
                    {change.path}
                  </span>
                  {change.from ? (
                    <span className="subtle">
                      <Icon name="chevron-left" size="sm" /> from <span className="mono">{change.from}</span>
                    </span>
                  ) : null}
                </li>
              );
            })}
          </ul>
        ) : (
          <p className="small muted">The branch changes no file.</p>
        )}
      </div>
    </div>
  );
}

