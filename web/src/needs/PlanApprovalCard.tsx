import { useContext, useEffect, useState } from "react";
import type { ReactNode } from "react";

import { decideApproval, inProjectOf, readPlan } from "../api/client";
import type { Adoption, Consolidation, ContractDifference, Difference, FileChange, ManifestRegistration, NeedItem, PartDifference, PlanApprovalNeed, PlanBranch } from "../api/types";
import { addressOf, ItemProject, Link } from "../app/route";
import { importedText, keptText, noRemovalText, personMergesText, removalText, truncatedText } from "../planning/consolidation";
import { createsLine } from "../planning/creation";
import { registrationOf, registrationTitle, RegistrationRows, ReplacedDrafts } from "../planning/PlanParts";
import { RevisionSince } from "../planning/RevisionSince";
import { words } from "../parts/parts";
import { Button } from "../ui/controls";
import { DecisionCard, DecisionSection } from "../ui/DecisionCard";
import { approvalStates } from "../floor/states";
import { Digest, Fact, Facts, Refusal, StateBadge } from "../ui/display";
import { Field } from "../ui/forms";
import type { Decided } from "./decided";
import { decidedOf } from "./decided";
import type { Frame } from "./frame";
import { useAct } from "./useAct";

// What a plan holds, in the order a person reads a plan, and each kind's name for one and for many.
const kinds: [string, string, string][] = [
  ["requirements", "requirements document", "requirements documents"],
  ["spec", "spec", "specs"],
  ["architecture", "architecture document", "architecture documents"],
  ["experience-design", "experience design", "experience designs"],
  ["milestone", "milestone", "milestones"],
  ["decision", "decision record", "decision records"],
  ["contract", "contract", "contracts"],
  ["repositories", "repository manifest", "repository manifests"],
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
  return <DecisionSection title={title}>{children}</DecisionSection>;
}

// Changes says how this version differs from the one a member approved last, record by record, and for
// a contract that changed, which criteria and work orders did.
function Changes({ difference }: { difference: Difference }) {
  const base = difference.base ?? 0;
  const none = !difference.added?.length && !difference.changed?.length && !difference.removed?.length;
  return (
    <Part title={base === 0 ? "A first version" : `Since version ${base}`}>
      {base === 0 ? <p className="muted">Nothing was approved before, so every record is new.</p> : null}
      {base !== 0 && none ? <p className="muted">No record differs from version {base}.</p> : null}
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
        <p className="muted">Nothing.</p>
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
          <p className="muted">The changes could not be read: {adoption.changes_unread}</p>
        ) : adoption.changes.length ? (
          <ul className="links">
            {adoption.changes.map((change) => (
              <li key={`${change.status} ${change.from ?? ""} ${change.path}`} className="mono">
                {changeText(change)}
              </li>
            ))}
          </ul>
        ) : (
          <p className="muted">Nothing.</p>
        )}
      </Part>
      {adoption.consolidation ? <ConsolidationPart consolidation={adoption.consolidation} /> : null}
    </>
  );
}

// ConsolidationPart says what approving a consolidating adoption does beyond the plan: the merge that imported the
// design repository's history, every path both repositories held with the version kept, the trees left out, the old
// design repository approving removes, and at person_merges how the person merges the branch.
function ConsolidationPart({ consolidation }: { consolidation: Consolidation }) {
  return (
    <Part title="What the consolidation imports">
      <p>{importedText(consolidation)}</p>
      {consolidation.unread ? null : consolidation.collisions.length ? (
        <ul className="links" aria-label="Paths both repositories held">
          {consolidation.collisions.map((collision) => (
            <li key={collision.path}>
              <span className="mono">{collision.path}</span>: {keptText(collision)}
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted">No path was held by both repositories.</p>
      )}
      {!consolidation.unread && consolidation.collisions_truncated ? <p className="muted">{truncatedText}</p> : null}
      {consolidation.left_out.length ? (
        <ul className="links" aria-label="Code trees left out">
          {consolidation.left_out.map((tree) => (
            <li key={tree} className="mono">
              {tree}
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted">No code tree is left out.</p>
      )}
      {consolidation.removes ? <p>{removalText(consolidation.removes)}</p> : null}
      {consolidation.removal_unread ? <p>{noRemovalText(consolidation.removal_unread)}</p> : null}
      {consolidation.person_merges ? <p>{personMergesText}</p> : null}
    </Part>
  );
}

// Preview is what approving a plan version would register of the repositories its manifest lists, as the card
// has read it: not asked for a version that lists no manifest, being read, read, or not readable.
type Preview = { state: "none" } | { state: "reading" } | { state: "read"; registration?: ManifestRegistration } | { state: "failed"; failure: string };

// usePreview reads, for a plan version whose manifest lists repositories, the preview the full plan page reads
// of it, from the clones on the daemon's worker's machine and with nothing recorded, so the card shows each
// entry a member consents to (ADR-0022). It reads again when the version or its digest changes.
function usePreview(plan: string, version: number, digest: string, lists: boolean): Preview {
  const project = useContext(ItemProject);
  const [preview, setPreview] = useState<Preview>({ state: lists ? "reading" : "none" });
  useEffect(() => {
    if (!lists) {
      setPreview({ state: "none" });
      return;
    }
    let current = true;
    setPreview({ state: "reading" });
    const read = project ? inProjectOf(project, () => readPlan(plan, String(version))) : readPlan(plan, String(version));
    read.then(
      (view) => current && setPreview({ state: "read", registration: view.approval?.registration }),
      (error: unknown) => current && setPreview({ state: "failed", failure: error instanceof Error ? error.message : String(error) }),
    );
    return () => {
      current = false;
    };
  }, [lists, plan, version, digest, project]);
  return preview;
}

// A plan is approved as one step: the digest sent is the one on this page, so a version that changed
// since this read is refused, and approving decides every draft with the member as the one who decided. A
// version that lists a manifest also registers its repositories, so it is approved only once the card shows
// each entry, its path, its remote, and what approving does with it.
// RegisteredRepositories is what an approval registered of the repositories its manifest lists, as the answer to
// the decision said it.
export function RegisteredRepositories({ registration, approval }: { registration: ManifestRegistration; approval?: string }) {
  return (
    <Part title={registrationTitle(registration)}>
      <RegistrationRows registration={registration} approval={approval} />
    </Part>
  );
}

// Sharing says who else waits to approve the version a request is about: main, whose request names no planning work
// order, and other planning branches.
export type Sharing = { main: boolean; branches: boolean };

// sharingVersion reads who waits beside a plan request for the same version of the same plan, in its project. A request
// that names a work order is a planning branch's, and one that names none is the plan as main holds it.
export function sharingVersion(item: NeedItem, items: NeedItem[]): Sharing {
  const sharing = { main: false, branches: false };
  if (item.kind !== "plan_approval" || !item.plan_approval.approval.plan_id) {
    return sharing;
  }
  const own = item.plan_approval.approval;
  for (const other of items) {
    if (other === item || other.kind !== "plan_approval" || other.project?.name !== item.project?.name) {
      continue;
    }
    const approval = other.plan_approval.approval;
    if (approval.plan_id === own.plan_id && approval.plan_version === own.plan_version) {
      if (approval.work_order_id) {
        sharing.branches = true;
      } else {
        sharing.main = true;
      }
    }
  }
  return sharing;
}

// sharedWith says for whom another request asks about the same version: for main when main's request waits, and
// otherwise for another planning branch, which is what the server names a shared version for when it is not main's.
function sharedWith({ main, branches }: Sharing): string {
  if (main) {
    return branches ? "for main and for another branch" : "for main";
  }
  return "for another branch";
}

// SharedBranch names the branch a request is about when another request asks about the same version for main or for
// another branch, so the two cards read apart: approving each approves the merge of its own branch.
function SharedBranch({ branch, sharing }: { branch: PlanBranch; sharing: Sharing }) {
  return (
    <div role="group" aria-label="Branch">
      <p>
        Another request asks about this version {sharedWith(sharing)}. This one is about branch <span className="mono">{branch.branch || branch.work_order}</span> of work
        order <span className="mono">{branch.work_order}</span>
        {branch.commit ? (
          <>
            {" "}
            at <Digest value={branch.commit} length={7} />
          </>
        ) : null}
        , and approving it approves the merge of that branch only.
      </p>
    </div>
  );
}

// FinishingBranch points a request held back as a placeholder to the planning branch whose commit finishes it, whose
// acceptance waits in Needs you beside it: accepting that work order asks about this version again, finished, and
// approving that request also approves the branch's merge.
function FinishingBranch({ branch }: { branch: PlanBranch }) {
  return (
    <div role="group" aria-label="Finished by">
      <p>
        Branch <span className="mono">{branch.branch || branch.work_order}</span> of work order <span className="mono">{branch.work_order}</span>
        {branch.commit ? (
          <>
            {" "}
            finishes this plan at <Digest value={branch.commit} length={7} />
          </>
        ) : (
          " finishes this plan"
        )}
        . Accept that work order, and ropeshift asks about this version again, finished and naming its branch, so approving it also approves
        the branch&apos;s merge.
      </p>
      <p>
        <Link to={addressOf("/needs-you", { item: `work:${branch.work_order}` })} aria-label={`Open the acceptance of ${branch.work_order}`}>
          Open its acceptance
        </Link>
      </p>
    </div>
  );
}

type CardProps = {
  id: string;
  need: PlanApprovalNeed;
  after: () => Promise<void>;
  frame: Frame;
  // registered is what approving registered, once this card's approval did. onRegistered hands it up, because the
  // card goes when the item no longer waits, and a member still reads what became of each entry.
  registered?: ManifestRegistration;
  onRegistered?: (registration: ManifestRegistration) => void;
  // onDecided hands up what the person decided, as the ledger recorded it, for the same reason.
  onDecided?: (decided: Decided) => void;
  // sharing is who else waits to approve the version, which the card names beside the branch it is about.
  sharing?: Sharing;
};

export function PlanApprovalCard({ id, need, after, frame, registered, onRegistered, onDecided, sharing = { main: false, branches: false } }: CardProps) {
  const [reason, setReason] = useState("");
  const { act, busy, failure } = useAct(after);
  const approval = need.approval;
  const unfinished = need.unfinished ?? [];
  const preview = usePreview(approval.plan_id ?? "", need.version, approval.subject_digest, (need.counts.repositories ?? 0) > 0 && Boolean(approval.plan_id));
  const unseen = preview.state === "reading" || preview.state === "failed";
  const decide = (allow: boolean) =>
    act(async () => {
      const sent = { allow, reason: reason.trim() };
      const decided = await decideApproval(id, approval.subject_digest, allow, sent.reason);
      onDecided?.(decidedOf(decided, sent));
      const registration = allow ? registrationOf(decided) : undefined;
      if (registration) {
        onRegistered?.(registration);
      }
    });
  const holds = holdings(need.counts);
  const creates = registered || preview.state !== "read" ? "" : createsLine(preview.registration?.repositories);
  return (
    <DecisionCard
      kind={{ icon: "plan", name: "Plan approval" }}
      project={frame.project}
      title={frame.title}
      provenance={frame.provenance}
      state={<StateBadge table={approvalStates} state={approval.state || "pending"} />}
      binding={
        <>
          Bound to plan digest <Digest value={approval.subject_digest} />. A new version needs its own approval.
        </>
      }
      actions={
        <>
          <Button disabled={busy} onClick={() => void decide(false)}>
            Deny
          </Button>
          {unfinished.length === 0 ? (
            <Button variant="primary" loading={busy} disabled={busy || unseen} onClick={() => void decide(true)}>
              Approve this plan
            </Button>
          ) : null}
        </>
      }
    >
      {unfinished.length > 0 ? (
        <>
          <p>Ropeshift did not offer this plan version for approval, because it is not finished. Still missing:</p>
          <ul>
            {unfinished.map((line) => (
              <li key={line}>{line}</li>
            ))}
          </ul>
          <p className="muted">A commit that finishes the plan replaces this request.</p>
          {need.finished_by ? <FinishingBranch branch={need.finished_by} /> : null}
        </>
      ) : (
      <p>
        Approving it decides each draft decision as yours, and passes the contract review of the work it covers, which then needs no
        review of its own.
        {creates ? ` Approving it also ${creates}.` : ""}
      </p>
      )}
      {need.branch ? <SharedBranch branch={need.branch} sharing={sharing} /> : null}
      <Facts>
        <Fact name="Requested by" mono>
          {need.requested_by}
        </Fact>
        <Fact name="Version">{need.version}</Fact>
        <Fact name="Digest">
          <Digest value={approval.subject_digest} length={16} />
        </Fact>
        {holds.length ? (
          <Fact name="Holds">
            <ul className="inline-list" aria-label="What it holds">
              {holds.map((line) => (
                <li key={line}>{line}</li>
              ))}
            </ul>
          </Fact>
        ) : null}
      </Facts>
      <p className="row-tight">
        <Link to={addressOf("/plan", { id: approval.plan_id || approval.id, version: String(need.version) })}>Open the full plan approval</Link>
        {approval.plan_id ? <Link to={addressOf("/plans", { plan: approval.plan_id })}>Read the plan</Link> : null}
      </p>
      {need.adoption ? <AdoptionGroups adoption={need.adoption} /> : null}
      {registered ? <RegisteredRepositories registration={registered} approval={approval.id} /> : null}
      {!registered && preview.state === "reading" ? <p className="muted">Reading what approving registers of the repositories its manifest lists.</p> : null}
      {!registered && preview.state === "failed" ? (
        <Refusal>{`What approving registers could not be read (${preview.failure}). Open the full plan approval to read it.`}</Refusal>
      ) : null}
      {!registered && preview.state === "read" && preview.registration ? <RegisteredRepositories registration={preview.registration} approval={approval.id} /> : null}
      <Changes difference={need.difference} />
      {need.revision?.revision ? (
        <Part title={`Decisions since version ${need.revision.revision.approved_version}`}>
          <RevisionSince revision={need.revision} />
        </Part>
      ) : null}
      <Part title="Draft decisions">
        {need.drafts.length ? (
          <ol className="drafts" aria-label="Draft decisions">
            {need.drafts.map((draft) => (
              <li key={draft.id} className="draft">
                <p className="question-text">{draft.question}</p>
                <p>{draft.answer}</p>
                {draft.rationale ? <p className="muted">{draft.rationale}</p> : null}
              </li>
            ))}
          </ol>
        ) : (
          <p className="muted">It drafts no decision.</p>
        )}
        <ReplacedDrafts replaced={need.replaced ?? []} />
      </Part>
      <Field label="Reason, which the plan's author reads (optional)">
        <input className="input" type="text" autoComplete="off" value={reason} disabled={busy} onChange={(event) => setReason(event.target.value)} />
      </Field>
      {failure ? <Refusal>{failure}</Refusal> : null}
    </DecisionCard>
  );
}
