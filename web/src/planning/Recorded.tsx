import { useId } from "react";

import { readRecorded } from "../api/client";
import type { PlanDocuments, RecordedDocument, RecordedVersion, RequirementTrace, TracedCriterion, TracedWork } from "../api/types";
import { useView } from "../api/useView";
import type { Live } from "../api/useView";
import { addressOf, Link } from "../app/route";
import { workLabel } from "../floor/states";
import { Ago, Fact, Page, Section, useClock, ViewTime, words } from "../parts/parts";
import { DocumentText } from "./DocumentText";

// The pages a person looks for a planning document on, by its kind. A kind this build has no page for
// lists with the other documents, so none falls out of every list.
export type DocumentHome = "requirements" | "spec" | "design" | "other";

export function homeOfKind(kind: string): DocumentHome {
  switch (kind) {
    case "requirements":
    case "spec":
      return kind;
    case "architecture":
    case "experience-design":
      return "design";
    default:
      return "other";
  }
}

const kindWords: Record<string, string> = {
  requirements: "Requirements",
  spec: "Spec",
  design: "Design",
  architecture: "Architecture",
  "experience-design": "Experience design",
  milestone: "Milestone",
  other: "Document",
};

// kindWord names a document's kind, of a file or of a record, the way a person says it.
export const kindWord = (kind: string) => kindWords[kind] ?? words(kind);

// reviewWords says where a version stands: under a plan version a member approved, waiting in one whose
// approval is open, or in neither.
export function reviewWords(version: RecordedVersion): string {
  switch (version.review) {
    case "approved":
      return `Approved with plan version ${version.plan_version ?? 0}`;
    case "pending":
      return `Waits for approval in plan version ${version.plan_version ?? 0}`;
    default:
      return "In no plan approval";
  }
}

export const recordedAddress = (id: string, version?: number) => addressOf("/document", { id, version: version ? String(version) : undefined });

function RecordedRow({ document, showKind }: { document: RecordedDocument; showKind: boolean }) {
  const newest = document.versions[0];
  return (
    <li className="row">
      <Link className="row-title" to={recordedAddress(document.id)}>
        {document.title || document.id}
      </Link>
      {document.path ? <p className="mono quiet">{document.path}</p> : null}
      <ul className="tags">
        {showKind ? <li>{kindWord(document.kind)}</li> : null}
        {newest ? <li>Version {newest.version}</li> : null}
        {newest ? <li className={`review review-${newest.review}`}>{reviewWords(newest)}</li> : null}
      </ul>
    </li>
  );
}

export function RecordedRows({ documents, showKind = false }: { documents: RecordedDocument[]; showKind?: boolean }) {
  return (
    <ol className="rows" role="list">
      {documents.map((document) => (
        <RecordedRow key={document.id} document={document} showKind={showKind} />
      ))}
    </ol>
  );
}

// recordedOf lists the documents of one page's kinds that each plan recorded, leaving out a plan with none.
export function recordedOf(plans: PlanDocuments[], home: DocumentHome): { plan: PlanDocuments; documents: RecordedDocument[] }[] {
  return plans
    .map((plan) => ({ plan, documents: plan.documents.filter((document) => homeOfKind(document.kind) === home) }))
    .filter((row) => row.documents.length > 0);
}

// PlanSections stands each plan's documents of one page under the plan's title.
export function PlanSections({ plans, home }: { plans: PlanDocuments[]; home: DocumentHome }) {
  return (
    <>
      {recordedOf(plans, home).map(({ plan, documents }) => (
        <Section key={plan.plan.id} title={plan.plan.title}>
          <RecordedRows documents={documents} showKind={home === "design" || home === "other"} />
        </Section>
      ))}
    </>
  );
}

// A requirement's criteria, gathered under the contract that holds them, in the order they came. Every
// criterion of one contract carries the same work, so the work is named once, under the contract.
type TracedContract = { contract: string; title: string; version: number; criteria: TracedCriterion[]; work: TracedWork[] };

function byContract(criteria: TracedCriterion[]): TracedContract[] {
  const out: TracedContract[] = [];
  for (const criterion of criteria) {
    const held = out.find((row) => row.contract === criterion.contract);
    if (held) {
      held.criteria.push(criterion);
      continue;
    }
    out.push({
      contract: criterion.contract,
      title: criterion.contract_title,
      version: criterion.contract_version,
      criteria: [criterion],
      work: criterion.work_orders,
    });
  }
  return out;
}

// A review agent's verdict lands on a criterion, so each criterion keeps the place for one.
function Criterion({ criterion }: { criterion: TracedCriterion }) {
  return (
    <li className="trace-criterion">
      <p>
        <span className="mono">{criterion.criterion}</span> {criterion.text}
      </p>
      <p className="quiet trace-verdict">No review verdict yet</p>
    </li>
  );
}

function Contract({ row, requirement, plan }: { row: TracedContract; requirement: string; plan: string }) {
  const title = row.title || row.contract;
  return (
    <li className="trace-contract">
      <p className="trace-contract-title">
        <Link to={addressOf("/plans", { plan })}>
          {title}, version {row.version}
        </Link>
      </p>
      <ul className="trace-criteria" aria-label={`Criteria of ${title} that serve ${requirement}`}>
        {row.criteria.map((criterion) => (
          <Criterion key={criterion.criterion} criterion={criterion} />
        ))}
      </ul>
      {row.work.length ? (
        <ul className="tags" aria-label={`Work orders of ${title}`}>
          {row.work.map((order) => (
            <li key={order.id}>
              {order.title || order.id}: <span className="tag-strong">{workLabel(order.state)}</span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="quiet">No work carries it yet.</p>
      )}
    </li>
  );
}

function Trace({ trace, plan }: { trace: RequirementTrace; plan: string }) {
  return (
    <li className="trace">
      <p className="trace-key mono">{trace.key}</p>
      <p>{trace.text}</p>
      {trace.criteria.length ? (
        <ul className="trace-contracts" aria-label={`Contracts that serve ${trace.key}`}>
          {byContract(trace.criteria).map((row) => (
            <Contract key={row.contract} row={row} requirement={trace.key} plan={plan} />
          ))}
        </ul>
      ) : (
        <p className="quiet">No criterion serves it yet.</p>
      )}
    </li>
  );
}

// Traces follows each requirement of a plan to the contracts and criteria that serve it and the work that
// carries them (FR-DOC-7). A review agent's verdicts on each criterion come with reviews, and until then
// each criterion says it has none.
export function Traces({ plan }: { plan: PlanDocuments }) {
  return (
    <Section title={plan.plan.title}>
      <p className="lead">Each requirement, the criteria that serve it, and the work that carries them.</p>
      <ol className="traces" aria-label={`Requirements of ${plan.plan.title}, traced to work`}>
        {plan.requirements.map((trace) => (
          <Trace key={`${trace.document}:${trace.key}`} trace={trace} plan={plan.plan.id} />
        ))}
      </ol>
    </Section>
  );
}

function Versions({ document, current }: { document: RecordedDocument; current: number }) {
  const heading = useId();
  return (
    <section className="group" aria-labelledby={heading}>
      <h2 className="group-title" id={heading}>
        Versions
      </h2>
      <ol className="rows" role="list">
        {document.versions.map((version) => (
          <li key={version.version} className="row">
            {version.version === current ? (
              <p aria-current="true">Version {version.version} (this one)</p>
            ) : (
              <Link to={recordedAddress(document.id, version.version)}>Version {version.version}</Link>
            )}
            <ul className="tags">
              <li className={`review review-${version.review}`}>{reviewWords(version)}</li>
              <li>
                Recorded <Ago at={version.recorded_at} />
              </li>
            </ul>
            {version.source ? <p className="mono quiet">{version.source}</p> : null}
            <p className="mono quiet">{version.digest}</p>
          </li>
        ))}
      </ol>
    </section>
  );
}

// RecordedPage reads a version of a planning document as the ledger stored it, which stays what a
// contract cited after its file moves on. The ledger holds it, so the stream says when it changed.
export function RecordedPage({ id, version, live }: { id: string; version: string; live: Live }) {
  const heading = useId();
  const read = useView(() => readRecorded(id, version), `recorded:${id}:${version}`, live);
  const now = useClock();
  const shown = read.view;
  const failure = read.failure ? (
    <p className="refusal" role="alert">
      {read.failure}
    </p>
  ) : null;
  if (read.refusedCode === "not_found" || read.refusedCode === "invalid") {
    return (
      <Page title="No such document">
        {failure}
        <p className="back">
          <Link to="/documents">Other documents</Link>
        </p>
      </Page>
    );
  }
  return (
    <Page title={shown?.document.title || (shown ? id : "Document")}>
      {failure}
      {shown ? (
        <ViewTime.Provider value={{ asOf: shown.as_of, elapsed: Math.max(0, now - read.readAt) }}>
          <dl className="facts facts-page">
            <Fact name="Kind">{kindWord(shown.document.kind)}</Fact>
            {shown.plan ? (
              <Fact name="Plan">
                <Link to={addressOf("/plans", { plan: shown.plan.id })}>{shown.plan.title}</Link>
              </Fact>
            ) : null}
            <Fact name="Version">{shown.version.version}</Fact>
            <Fact name="Review">{reviewWords(shown.version)}</Fact>
            {shown.version.source ? (
              <Fact name="Source" mono>
                {shown.version.source}
              </Fact>
            ) : null}
            <Fact name="Digest" mono>
              {shown.version.digest}
            </Fact>
          </dl>
          <p className="outside" role="note">
            This text comes from a repository, as anyone with a commit wrote it, and the ledger keeps it as intake read it. It is shown as
            text: nothing in it runs, loads, or links.
          </p>
          <section aria-labelledby={heading} className="group">
            <h2 className="visually-hidden" id={heading}>
              The document
            </h2>
            <DocumentText body={shown.body} title={shown.document.title} />
          </section>
          <Versions document={shown.document} current={shown.version.version} />
        </ViewTime.Provider>
      ) : failure ? null : (
        <p className="quiet">Reading.</p>
      )}
    </Page>
  );
}
