import { useId } from "react";

import { readDocument, readDocumentVersions } from "../api/client";
import type { DocumentKind, DocumentRef, DocumentRow, Documents as View, Requirement } from "../api/types";
import { useView } from "../api/useView";
import type { Live } from "../api/useView";
import { addressOf, Link } from "../app/route";
import { Fact, Page, Section } from "../parts/parts";
import { DocumentText } from "./DocumentText";
import { kindWord, PlanSections, recordedOf, RecordedRows, Traces } from "./Recorded";

// The kinds of file that have a page of their own.
const homes: DocumentKind[] = ["requirements", "spec", "design", "other"];

function DocumentRows({ documents }: { documents: DocumentRow[] }) {
  return (
    <ol className="rows" role="list">
      {documents.map((document) => (
        <li key={`${document.instance}:${document.path}`} className="row">
          <Link className="row-title" to={addressOf("/document", { path: document.path, instance: document.instance })}>
            {document.title}
          </Link>
          <p className="mono quiet">{document.path}</p>
          <ul className="tags" aria-label="Kind">
            <li>{kindWord(document.kind)}</li>
          </ul>
        </li>
      ))}
    </ol>
  );
}

// A kind this build has no page for lists with the rest, so no document falls out of every list. A file a
// plan recorded lists once, under its plan, with its version and where it stands in review. A goal, a
// plan, or a contract shows on its own page, and every other file of the repository lists as it stands.
export function DocumentList({ view, kind, empty }: { view: View; kind: DocumentKind; empty: string }) {
  const homeOf = (document: DocumentRow) => (homes.includes(document.kind) ? document.kind : "other");
  const documents = view.documents.filter((document) => homeOf(document) === kind && !document.record);
  const recorded = recordedOf(view.plans, kind);
  if (recorded.length === 0) {
    return documents.length ? <DocumentRows documents={documents} /> : <p className="empty">{empty}</p>;
  }
  return (
    <>
      <PlanSections plans={view.plans} home={kind} />
      {documents.length ? (
        <Section title="Other repository files">
          <DocumentRows documents={documents} />
        </Section>
      ) : null}
    </>
  );
}

function RequirementRow({ requirement }: { requirement: Requirement }) {
  return (
    <li className="row">
      <Link className="row-title mono" to={addressOf("/requirements", { key: requirement.key })}>
        {requirement.key}
      </Link>
      <p>{requirement.text}</p>
      <ul className="tags">
        <li>Version {requirement.version}</li>
        {requirement.source ? <li className="mono">{requirement.source}</li> : null}
      </ul>
    </li>
  );
}

// Requirements lists every requirement, then follows each plan's requirements to the criteria and work
// that serve them, then names the documents that hold them: those a plan recorded, and the other files.
export function Requirements({ view }: { view: View }) {
  const documents = view.documents.filter((document) => document.kind === "requirements" && !document.record);
  const recorded = recordedOf(view.plans, "requirements").flatMap((row) => row.documents);
  const traced = view.plans.filter((plan) => plan.requirements.length > 0);
  if (view.requirements.length === 0 && documents.length === 0 && recorded.length === 0) {
    return <p className="empty">No requirements yet.</p>;
  }
  return (
    <>
      {view.requirements.length ? (
        <ol className="rows" role="list">
          {view.requirements.map((requirement) => (
            <RequirementRow key={requirement.key} requirement={requirement} />
          ))}
        </ol>
      ) : null}
      {traced.map((plan) => (
        <Traces key={plan.plan.id} plan={plan} />
      ))}
      {documents.length || recorded.length ? (
        <Section title="Documents that hold them">
          {recorded.length ? <RecordedRows documents={recorded} /> : null}
          {documents.length ? <DocumentRows documents={documents} /> : null}
        </Section>
      ) : null}
    </>
  );
}

export function OneRequirement({ requirement }: { requirement: Requirement }) {
  return (
    <>
      <p className="back">
        <Link to="/requirements">All requirements</Link>
      </p>
      <p className="statement statement-page">{requirement.text}</p>
      <dl className="facts facts-page">
        <Fact name="Version">{requirement.version}</Fact>
        {requirement.source ? (
          <Fact name="Source" mono>
            {requirement.source}
          </Fact>
        ) : null}
        <Fact name="Digest" mono>
          {requirement.digest}
        </Fact>
      </dl>
    </>
  );
}

function Versions({ reference, current, still }: { reference: DocumentRef; current: string; still: Live }) {
  const read = useView(() => readDocumentVersions(reference), `${reference.instance}:${reference.path}`, still);
  const heading = useId();
  return (
    <section className="group" aria-labelledby={heading}>
      <h2 className="group-title" id={heading}>
        Versions
      </h2>
      {read.view ? (
        <ol className="rows" role="list">
          {read.view.versions.map(({ version, digest }) => (
            <li key={version} className="row">
              {version === current ? (
                <p className="mono" aria-current="true">
                  {version} (this one)
                </p>
              ) : (
                <Link className="mono" to={addressOf("/document", { ...reference, version })}>
                  {version}
                </Link>
              )}
              <p className="mono quiet">{digest}</p>
            </li>
          ))}
        </ol>
      ) : null}
    </section>
  );
}

export function DocumentPage({ reference, live }: { reference: DocumentRef; live: Live }) {
  const heading = useId();
  // A document comes from a repository and not from the ledger, so nothing on the stream changes it,
  // and each read here runs git.
  const still: Live = { heard: 0, checkSession: live.checkSession };
  const read = useView(() => readDocument(reference), `${reference.instance}:${reference.path}:${reference.version}`, still);
  const document = read.view;
  const failure = read.failure ? (
    <p className="refusal" role="alert">
      {read.failure}
    </p>
  ) : null;
  if (read.refusedCode === "not_found" || reference.path === "") {
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
    <Page title={document?.title ?? "Document"}>
      {failure}
      {document ? (
        <>
          <dl className="facts facts-page">
            <Fact name="Kind">{kindWord(document.kind)}</Fact>
            <Fact name="Path" mono>
              {document.path}
            </Fact>
            <Fact name="Source" mono>
              {document.source}
            </Fact>
            <Fact name="Digest" mono>
              {document.digest}
            </Fact>
          </dl>
          <p className="outside" role="note">
            This text comes from a repository, as anyone with a commit wrote it. It is shown as text: nothing in it runs, loads, or links.
          </p>
          <section aria-labelledby={heading} className="group">
            <h2 className="visually-hidden" id={heading}>
              The document
            </h2>
            <DocumentText body={document.body ?? ""} title={document.title} />
          </section>
          <Versions reference={reference} current={document.version} still={still} />
        </>
      ) : failure ? null : (
        <p className="quiet">Reading.</p>
      )}
    </Page>
  );
}
