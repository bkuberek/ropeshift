import { useRef, useState } from "react";

import { useChange, useRead } from "../admin/parts";
import { ApiError, decideApproval, readApproval, readPlan } from "../api/client";
import type { ApprovalRecord, PlanApprovalDetail, PlanApprovalView, PlanEntry, PlanVersionLine, DraftDecision, Plan } from "../api/types";
import type { Live } from "../api/useView";
import { addressOf, Link, useArrivalFocus } from "../app/route";
import { approvalStates } from "../floor/states";
import { timeOf } from "../admin/parts";
import { Button } from "../ui/controls";
import { DecisionCard } from "../ui/DecisionCard";
import { Banner, Digest, EmptyState, Panel, Refusal, SkeletonRows, StateBadge, TargetChip } from "../ui/display";
import { Field } from "../ui/forms";
import { Dialog } from "../ui/overlays";
import { AdoptionGroups, ChangedContracts, CoveredDocuments, DifferenceBlock, DraftDecisions } from "./PlanParts";
import { versionLinks } from "./planChanges";

// Subject is what the page decides on: one version of a plan and the request to approve it. A plan the ledger
// does not hold yet, which a branch's first version is, has no plan, and its request is read by its own ID.
export type Subject = {
  plan?: Plan;
  // address is what the page's own address names: the plan's ID, or the request's ID.
  address: string;
  versions: PlanVersionLine[];
  approval?: ApprovalRecord;
  detail: PlanApprovalDetail;
  records: PlanEntry[];
  drafts: DraftDecision[];
  version: number;
};

const noDetail: PlanApprovalDetail = { title: "" };

function detailOf(approval: ApprovalRecord): PlanApprovalDetail {
  try {
    return { ...noDetail, ...(JSON.parse(approval.detail ?? "{}") as PlanApprovalDetail) };
  } catch {
    return noDetail;
  }
}

// readSubject reads a plan's version by the plan's ID, and, when the ID is no plan's, a request by its own ID.
export async function readSubject(id: string, version: string): Promise<Subject> {
  try {
    const view = await readPlan(id, version);
    const held: PlanApprovalView | undefined = view.approval;
    return {
      plan: view.plan,
      address: id,
      versions: view.versions,
      approval: held?.approval,
      detail: held?.detail ?? noDetail,
      records: held?.records ?? [],
      drafts: held?.drafts ?? [],
      version: held?.approval.plan_version ?? Number(version || view.versions[0]?.version || 0),
    };
  } catch (error) {
    if (!(error instanceof ApiError) || error.code !== "not_found") {
      throw error;
    }
  }
  const approval = await readApproval(id);
  if (approval.kind !== "plan") {
    throw new ApiError(404, "not_found", `${id} is no plan and no request to approve one.`);
  }
  return { address: id, versions: [], approval, detail: detailOf(approval), records: [], drafts: [], version: approval.plan_version ?? 1 };
}

function DeclineDialog({ subject, onClose, onDone, after }: { subject: Subject; onClose: () => void; onDone: () => void; after: () => Promise<void> }) {
  const [reason, setReason] = useState("");
  const [empty, setEmpty] = useState(false);
  const field = useRef<HTMLTextAreaElement>(null);
  const change = useChange();
  const approval = subject.approval;
  const submit = async () => {
    if (!approval) {
      return;
    }
    if (reason.trim() === "") {
      setEmpty(true);
      field.current?.focus();
      return;
    }
    const done = await change.run(async () => {
      await decideApproval(approval.id, approval.subject_digest, false, reason.trim());
      await after();
      return true;
    });
    if (done) {
      onDone();
    }
  };
  return (
    <Dialog
      open
      title={`Decline version ${subject.version}`}
      onClose={onClose}
      initialFocus={field}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Keep deciding</Button>
          <Button variant="danger" loading={change.busy} onClick={() => void submit()}>
            Decline version {subject.version}
          </Button>
        </>
      }
    >
      <form
        className="field-stack"
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <p className="small">The session that wrote the plan reads your reason, and writes a new version when it can meet it.</p>
        <Field label="Reason (required)" error={empty ? "Say why you decline, in a sentence." : undefined}>
          <textarea ref={field} className="textarea" rows={4} autoComplete="off" required placeholder="The retry budget should be per key…" value={reason} onChange={(event) => setReason(event.target.value)} />
        </Field>
        {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      </form>
    </Dialog>
  );
}

// Decision is the sticky card that decides one version: what approving does, what it binds to, Decline with a
// reason, and Approve. A version a newer one replaced is void, and one that was decided is read-only.
function Decision({ subject, after, reload }: { subject: Subject; after: () => Promise<void>; reload: () => void }) {
  const [declining, setDeclining] = useState(false);
  const change = useChange();
  const approval = subject.approval;
  if (!approval) {
    return null;
  }
  const links = versionLinks(subject.versions, subject.version);
  const state = approval.state;
  const approve = () =>
    void change.run(async () => {
      await decideApproval(approval.id, approval.subject_digest, true, "");
      await after();
      reload();
    });
  const decisions = subject.detail.decisions ?? subject.drafts.length;
  const contracts = subject.detail.counts?.contract ?? 0;
  const open = state === "pending";
  const resolution = open ? undefined : (
    <div className="resolution" role="status" aria-label="Resolution">
      <StateBadge table={approvalStates} state={state === "superseded" ? "void" : state} />
      <span className="small">
        {state === "approved" || state === "denied" ? (
          <>
            {state === "approved" ? "Approved" : "Declined"} by <span className="mono">{approval.decided_by ?? "someone"}</span>
            {approval.decided_at ? <> on {timeOf(approval.decided_at)}</> : null}
            {approval.reason ? <>: {approval.reason}</> : null}
          </>
        ) : state === "superseded" ? (
          <>
            A newer version replaced this one before anyone decided it.
            {links?.newer ? (
              <>
                {" "}
                <Link to={addressOf("/plan", { id: subject.address, version: String(links.newer) })}>Open version {links.newer}</Link>
              </>
            ) : null}
          </>
        ) : (
          "This request can no longer be decided."
        )}
      </span>
    </div>
  );
  return (
    <>
      <DecisionCard
        kind={{ icon: "plan", name: "Your decision" }}
        title={`Approve version ${subject.version}`}
        state={<StateBadge table={approvalStates} state={state} />}
        binding={
          <>
            Bound to digest <Digest value={approval.subject_digest} />. If the plan changes, this approval is void.
          </>
        }
        resolution={resolution}
        actions={
          <>
            <Button disabled={change.busy} onClick={() => setDeclining(true)}>
              Decline…
            </Button>
            <Button variant="primary" loading={change.busy} onClick={approve}>
              Approve version {subject.version}
            </Button>
          </>
        }
      >
        <div className="small stack-8">
          <p>
            Approving decides {decisions} draft {decisions === 1 ? "decision" : "decisions"} as yours
            {contracts ? ` and passes contract review for the work of ${contracts} ${contracts === 1 ? "contract" : "contracts"}` : ""}. Contract review still runs and sends any new question up the
            ladder.
          </p>
          <p className="muted">A one-way decision, a conflict between requirements, or a change of scope still reaches you later.</p>
          {change.failure ? <Refusal>{change.failure}</Refusal> : null}
        </div>
      </DecisionCard>
      {declining ? <DeclineDialog subject={subject} after={after} onClose={() => setDeclining(false)} onDone={() => { setDeclining(false); reload(); }} /> : null}
    </>
  );
}

function Versions({ subject }: { subject: Subject }) {
  if (subject.versions.length === 0) {
    return null;
  }
  return (
    <Panel title="Versions">
      <ol className="list-rows" aria-label="Versions of the plan">
        {subject.versions.map((line) => (
          <li key={line.version} className="list-row row">
            {line.version === subject.version ? (
              <span aria-current="true">Version {line.version} (this one)</span>
            ) : (
              <Link to={addressOf("/plan", { id: subject.address, version: String(line.version) })}>Version {line.version}</Link>
            )}
            {line.state ? <StateBadge table={approvalStates} state={line.state === "superseded" ? "void" : line.state} /> : <span className="unavailable">no request</span>}
          </li>
        ))}
      </ol>
    </Panel>
  );
}

// PlanApprovalBody is the page for one version of a plan: the difference from the approved version, the
// answers it drafts, the contracts it adds or changes, the documents it covers, and for an adopted plan the
// groups the API returns, beside the card that decides it.
export function PlanApprovalBody({ subject, live, after, reload }: { subject: Subject; live: Live; after: () => Promise<void>; reload: () => void }) {
  const approval = subject.approval;
  const links = versionLinks(subject.versions, subject.version);
  if (!approval) {
    return (
      <>
        <div className="panel">
          <EmptyState icon="plan" title={`No request asks about version ${subject.version}`}>
            Nobody was asked to approve this version, so there is nothing to decide here.
          </EmptyState>
        </div>
        <Versions subject={subject} />
      </>
    );
  }
  const superseded = approval.state === "superseded" && links?.newer;
  return (
    <>
      {superseded ? (
        <Banner tone="warning" action={<Link className="btn btn-sm" to={addressOf("/plan", { id: subject.address, version: String(links?.newer) })}>Open version {links?.newer}</Link>}>
          <strong>Version {links?.newer} replaced this version.</strong> Its decision card is void.
        </Banner>
      ) : null}
      <div className="grid grid-main-side">
        <div className="stack">
          <Panel title={subject.detail.difference?.base ? `Difference from version ${subject.detail.difference.base}` : "Difference from the approved version"}>
            <DifferenceBlock difference={subject.detail.difference ?? {}} live={live} />
          </Panel>
          {subject.detail.adoption ? (
            <Panel title="What the adoption does">
              <AdoptionGroups adoption={subject.detail.adoption} />
            </Panel>
          ) : null}
          <Panel title="Draft decisions">
            <DraftDecisions drafts={subject.drafts} proposedBy={approval.requested_by} />
          </Panel>
          <Panel title="New and changed contracts">
            <ChangedContracts plan={subject.plan?.id ?? ""} difference={subject.detail.difference ?? {}} live={live} />
          </Panel>
          <Panel title="Documents">
            <CoveredDocuments records={subject.records} live={live} />
          </Panel>
          <Versions subject={subject} />
        </div>
        <aside className="stack sticky decision-aside" aria-label="Your decision">
          <Decision subject={subject} after={after} reload={reload} />
          {approval.work_order_id ? (
            <Link className="btn" to={addressOf("/work-order", { id: approval.work_order_id })}>
              Open the planning session's work order
            </Link>
          ) : null}
        </aside>
      </div>
    </>
  );
}

// Author says where a version came from: the planning session whose lane's branch held it, or the file intake
// read it from, at its commit. The ledger records no author of a commit.
function Author({ approval, source = "" }: { approval: ApprovalRecord; source?: string }) {
  const onWork = approval.work_order_id ? (
    <>
      {" "}
      on <Link to={addressOf("/work-order", { id: approval.work_order_id })}>{approval.work_order_id}</Link>
    </>
  ) : null;
  if (approval.requested_by.startsWith("lane:")) {
    return (
      <>
        Written by{" "}
        <TargetChip kind="planning" small>
          Planning session
        </TargetChip>
        {onWork}
      </>
    );
  }
  if (approval.requested_by !== "system:intake") {
    return (
      <span>
        Requested by {approval.requested_by}
        {onWork}
      </span>
    );
  }
  // A source is a file's path and the commit intake read it at.
  const at = source.lastIndexOf("@");
  const path = at > 0 ? source.slice(0, at) : source;
  const commit = at > 0 ? source.slice(at + 1) : "";
  return (
    <span>
      Read by intake
      {path ? (
        <>
          {" "}
          from <code>{path}</code>
        </>
      ) : null}
      {commit ? (
        <>
          {" "}
          at commit <Digest value={commit} length={8} />
        </>
      ) : null}
      {onWork}
    </span>
  );
}

// PlanApprovalPage reads one version of a plan by its plan's ID, or a branch's first version by its request's.
export function PlanApprovalPage({ live, id, version, after }: { live: Live; id: string; version: string; after: () => Promise<void> }) {
  const heading = useRef<HTMLHeadingElement>(null);
  useArrivalFocus(heading);
  const read = useRead(() => (id ? readSubject(id, version) : Promise.reject(new ApiError(404, "not_found", "No plan is named."))), `plan:${id}:${version}`, live);
  const subject = read.view;
  const missing = read.refusedCode === "not_found";
  const title = subject ? (subject.plan?.title ?? subject.detail.title ?? "Plan") : "Plan";
  const approval = subject?.approval;
  return (
    <div className="screen">
      <nav className="crumbs" aria-label="Breadcrumb">
        <Link to="/roadmap">Roadmap</Link>
        <span aria-hidden="true">›</span>
        <Link to="/plans">Plans</Link>
        <span aria-hidden="true">›</span>
        <span aria-current="page">{subject ? `Version ${subject.version}` : "Version"}</span>
      </nav>
      <div className="page-head">
        <div className="page-head-text">
          <h1 ref={heading} tabIndex={-1}>
            {missing ? "No such plan" : approval?.state === "pending" ? `Approve plan: ${title}, version ${subject?.version}` : subject ? `${title}, version ${subject.version}` : "Plan"}
          </h1>
          {approval && subject ? (
            <p className="row-tight page-note">
              <Author approval={approval} source={subject.detail.source} />
              {approval.raised_at ? <> · submitted {timeOf(approval.raised_at)}</> : null} · digest <Digest value={approval.subject_digest} />
            </p>
          ) : null}
        </div>
        {approval ? <StateBadge table={approvalStates} state={approval.state} /> : null}
      </div>
      {read.failure && !subject ? <Banner tone="danger">{read.failure}</Banner> : null}
      {read.failure && subject ? <Banner tone="danger">{`What this page shows is not current. ${read.failure}`}</Banner> : null}
      {subject ? (
        <PlanApprovalBody subject={subject} live={live} after={after} reload={read.again} />
      ) : read.failure ? null : (
        <div className="panel">
          <SkeletonRows rows={5} />
        </div>
      )}
    </div>
  );
}
