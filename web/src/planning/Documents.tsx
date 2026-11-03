import { useId } from "react";

import { readDocument, readDocumentVersions } from "../api/client";
import type { DocumentKind, DocumentRef, DocumentRow, Documents as View, Requirement } from "../api/types";
import { useView } from "../api/useView";
import type { Live } from "../api/useView";
import { addressOf, Link } from "../app/route";
import { Fact, Page, Section, words } from "../parts/parts";
import { DocumentText } from "./DocumentText";

const kindWords: Record<DocumentKind, string> = { requirements: "Requirements", spec: "Spec", design: "Design", other: "Document" };

function DocumentRows({ documents }: { documents: DocumentRow[] }) {
  return (
    <ol className="rows" role="list">
      {documents.map((document) => (
        <li key={`${document.instance}:${document.path}`} className="row">
          <Link className="row-title" to={addressOf("/document", { path: document.path, instance: document.instance })}>
            {document.title}
          </Link>
          <p className="mono quiet">{document.path}</p>
        </li>
      ))}
    </ol>
  );
}

export function DocumentList({ view, kind, empty }: { view: View; kind: DocumentKind; empty: string }) {
  const documents = view.documents.filter((document) => document.kind === kind);
  return documents.length ? <DocumentRows documents={documents} /> : <p className="empty">{empty}</p>;
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

export function Requirements({ view }: { view: View }) {
  const documents = view.documents.filter((document) => document.kind === "requirements");
  if (view.requirements.length === 0 && documents.length === 0) {
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
      {documents.length ? (
        <Section title="Documents that hold them">
          <DocumentRows documents={documents} />
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
  if (read.failure || reference.path === "") {
    return (
      <Page title="No such document">
        {read.failure ? (
          <p className="refusal" role="alert">
            {read.failure}
          </p>
        ) : null}
        <p className="back">
          <Link to="/documents">Other documents</Link>
        </p>
      </Page>
    );
  }
  return (
    <Page title={document?.title ?? "Document"}>
      {document ? (
        <>
          <dl className="facts facts-page">
            <Fact name="Kind">{kindWords[document.kind] ?? words(document.kind)}</Fact>
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
      ) : (
        <p className="quiet">Reading.</p>
      )}
    </Page>
  );
}
