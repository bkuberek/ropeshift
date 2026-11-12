import { useId } from "react";

import { readDocument, readDocumentVersions, readRecorded } from "../api/client";
import type { Documents, PlanDocuments } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { addressOf, go, Link } from "../app/route";
import { requirementStates } from "../floor/states";
import { Ago, useClock, ViewTime } from "../parts/parts";
import { IconButton } from "../ui/controls";
import { Diff } from "../ui/Diff";
import { lineDiff } from "../ui/diff";
import { Banner, Digest, StateBadge } from "../ui/display";
import { Select } from "../ui/forms";
import { useToast } from "../ui/overlays";
import { DocumentText } from "./DocumentText";
import { documentAddress, kindWord, reviewWords, versionLabel } from "./documents";
import type { TreeItem } from "./documents";
import { requirementStatus } from "./trace";

const outside = "This text comes from a repository, as anyone with a commit wrote it. It is shown as text: nothing in it runs, loads, or links.";

function CopyLink() {
  const toast = useToast();
  return (
    <IconButton
      icon="link"
      label="Copy link"
      small
      onClick={() => {
        void navigator.clipboard?.writeText(String(window.location)).then(
          () => toast("neutral", "Link copied."),
          () => toast("warning", "The link could not be copied."),
        );
      }}
    />
  );
}

// Requirements are the rows of a requirements document: each requirement with where it stands, which selects
// it for the trace beside the reader.
function Requirements({ document, plans, item, selected, version }: { document: string; plans: PlanDocuments[]; item: TreeItem; selected: string; version: string }) {
  const traces = plans.flatMap((plan) => plan.requirements).filter((trace) => trace.document === document);
  if (traces.length === 0) {
    return null;
  }
  return (
    <ol className="reqs" aria-label="Requirements">
      {traces.map((trace) => (
        <li key={trace.key} className="req" aria-current={trace.key === selected ? "true" : undefined}>
          <Link className="id" stay to={documentAddress(item, { version, key: trace.key })} aria-label={`Trace ${trace.key}`}>
            {trace.key}
          </Link>
          <span>{trace.text}</span>
          <StateBadge table={requirementStates} state={requirementStatus(trace)} />
        </li>
      ))}
    </ol>
  );
}

// Comparison shows how one version differs from the one it is compared with, by line.
function Comparison({ id, against, shown, live }: { id: string; against: string; shown: { version: number; body: string }; live: Live }) {
  const other = useView(() => readRecorded(id, against), `recorded:${id}:${against}`, live);
  if (other.failure && !other.view) {
    return <Banner tone="danger">{other.failure}</Banner>;
  }
  if (!other.view) {
    return <p className="muted">Reading version {against}.</p>;
  }
  return (
    <>
      <p className="small muted">
        Version {other.view.version.version} to version {shown.version}
      </p>
      <Diff lines={lineDiff(other.view.body, shown.body)} label={`Difference between version ${other.view.version.version} and version ${shown.version}`} />
    </>
  );
}

// RecordedReader reads a version of a planning document as the ledger stored it, which stays what a contract
// cited after the file moves on. It says where the version stands in review and, for a requirements document,
// lists its requirements. Two versions of one document show as one difference.
export function RecordedReader({ item, view, live, params }: { item: TreeItem; view: Documents; live: Live; params: URLSearchParams }) {
  const version = params.get("version") ?? "";
  const against = params.get("against") ?? "";
  const selected = params.get("key") ?? "";
  const heading = useId();
  const read = useView(() => readRecorded(item.key, version), `recorded:${item.key}:${version}`, live);
  const now = useClock();
  const shown = read.view;
  if (read.refusedCode === "not_found" || read.refusedCode === "invalid") {
    return (
      <article className="panel" aria-labelledby={heading}>
        <div className="panel-head">
          <h2 id={heading}>No such document</h2>
        </div>
        <div className="panel-body">
          <Banner tone="danger">{read.failure}</Banner>
        </div>
      </article>
    );
  }
  const others = shown?.document.versions.filter((row) => row.version !== shown.version.version) ?? [];
  const go2 = (changes: Record<string, string>) => go(documentAddress(item, { version, against, key: selected, ...changes }), { stay: true });
  return (
    <article className="panel" aria-labelledby={heading}>
      <div className="panel-head">
        <h2 id={heading}>{shown?.document.title || item.title}</h2>
        {shown ? (
          <>
            <Select small aria-label="Version" value={String(shown.version.version)} onChange={(event) => go2({ version: event.target.value, against: "" })}>
              {shown.document.versions.map((row) => (
                <option key={row.version} value={row.version}>
                  {versionLabel(row)}
                </option>
              ))}
            </Select>
            <CopyLink />
          </>
        ) : null}
      </div>
      <div className="panel-body">
        {read.failure && !shown ? <Banner tone="danger">{read.failure}</Banner> : null}
        {shown ? (
          <ViewTime.Provider value={{ asOf: shown.as_of, elapsed: Math.max(0, now - read.readAt) }}>
            <p className="small muted doc-meta">
              <code>{shown.document.path || item.key}</code> · digest <Digest value={shown.version.digest} />
              {shown.version.source ? (
                <>
                  {" "}
                  · <span className="mono" title={shown.version.source}>{shown.version.source}</span>
                </>
              ) : null}
            </p>
            <dl className="kv doc-facts">
              <div className="kv-row">
                <dt>Kind</dt>
                <dd>{kindWord(shown.document.kind)}</dd>
              </div>
              <div className="kv-row">
                <dt>Review</dt>
                <dd>{reviewWords(shown.version)}</dd>
              </div>
              <div className="kv-row">
                <dt>Recorded</dt>
                <dd>
                  <Ago at={shown.version.recorded_at} />
                </dd>
              </div>
              {shown.plan ? (
                <div className="kv-row">
                  <dt>Plan</dt>
                  <dd>
                    <Link to={addressOf("/plans", { plan: shown.plan.id })}>{shown.plan.title}</Link>
                    {shown.version.plan_version ? (
                      <>
                        {" · "}
                        <Link to={addressOf("/plan", { id: shown.plan.id, version: String(shown.version.plan_version) })}>Open in plan</Link>
                      </>
                    ) : null}
                  </dd>
                </div>
              ) : null}
            </dl>
            <p className="outside" role="note">
              {outside}
            </p>
            {others.length ? (
              <label className="row-tight compare">
                <span className="small muted">Compare with</span>
                <Select small aria-label="Compare with" value={against} onChange={(event) => go2({ against: event.target.value })}>
                  <option value="">No comparison</option>
                  {others.map((row) => (
                    <option key={row.version} value={row.version}>
                      {versionLabel(row)}
                    </option>
                  ))}
                </Select>
              </label>
            ) : null}
            {shown.document.kind === "requirements" ? <Requirements document={shown.document.id} plans={view.plans} item={item} selected={selected} version={version} /> : null}
            {against ? (
              <Comparison id={item.key} against={against} shown={{ version: shown.version.version, body: shown.body }} live={live} />
            ) : (
              <DocumentText body={shown.body} title={shown.document.title} />
            )}
          </ViewTime.Provider>
        ) : read.failure ? null : (
          <p className="muted">Reading.</p>
        )}
      </div>
    </article>
  );
}

// FileReader reads a file of the repository as it stands, at the version the address names. A file git cannot
// read shows the reason and keeps the list of its versions.
export function FileReader({ item, live, params }: { item: TreeItem; live: Live; params: URLSearchParams }) {
  const version = params.get("version") ?? "";
  const heading = useId();
  // A document comes from a repository and not from the ledger, so nothing on the stream changes it, and
  // each read here runs git.
  const still: Live = { heard: 0, checkSession: live.checkSession };
  const reference = { path: item.params.path ?? item.path, instance: item.params.instance ?? "", version };
  const read = useView(() => readDocument(reference), `${reference.instance}:${reference.path}:${version}`, still);
  const versions = useView(() => readDocumentVersions({ path: reference.path, instance: reference.instance }), `${reference.instance}:${reference.path}:versions`, still);
  const document = read.view;
  const list = versions.view?.versions ?? [];
  return (
    <article className="panel" aria-labelledby={heading}>
      <div className="panel-head">
        <h2 id={heading}>{document?.title || item.title}</h2>
        {list.length ? (
          <Select small aria-label="Version" value={document?.version ?? version} onChange={(event) => go(documentAddress(item, { version: event.target.value }), { stay: true })}>
            {list.map((row) => (
              <option key={row.version} value={row.version}>
                {`${row.version} · ${row.digest.replace(/^sha256:/, "").slice(0, 7)}`}
              </option>
            ))}
          </Select>
        ) : null}
        <CopyLink />
      </div>
      <div className="panel-body">
        {read.failure ? <Banner tone="danger">{read.failure}</Banner> : null}
        {document ? (
          <>
            <p className="small muted doc-meta">
              <code>{document.path}</code> · digest <Digest value={document.digest} />
            </p>
            <dl className="kv doc-facts">
              <div className="kv-row">
                <dt>Kind</dt>
                <dd>{kindWord(document.kind)}</dd>
              </div>
              <div className="kv-row">
                <dt>Version</dt>
                <dd>{document.version}</dd>
              </div>
              <div className="kv-row">
                <dt>Source</dt>
                <dd>
                  <span className="id" title={document.source}>
                    {document.source}
                  </span>
                </dd>
              </div>
            </dl>
            <p className="outside" role="note">
              {outside}
            </p>
            <DocumentText body={document.body ?? ""} title={document.title} />
          </>
        ) : read.failure ? null : (
          <p className="muted">Reading.</p>
        )}
      </div>
    </article>
  );
}
