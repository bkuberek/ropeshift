import type { DocumentKind, DocumentRow, Documents, RecordedDocument, RecordedVersion } from "../api/types";
import { addressOf } from "../app/route";
import { words } from "../parts/parts";

// The pages a person looks for a planning document on, by its kind. A kind this build has no page for lists
// with the other documents, so none falls out of every list.
export type DocumentHome = DocumentKind;

export const homes: DocumentHome[] = ["requirements", "spec", "design", "other"];

export const homeWords: Record<DocumentHome, string> = { requirements: "Requirements", spec: "Specs", design: "Designs", other: "Other" };

export function homeOfKind(kind: string): DocumentHome {
  switch (kind) {
    case "requirements":
    case "spec":
    case "design":
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

// versionLabel is a version's line in the selector: its number, and the plan version it stands in.
export function versionLabel(version: RecordedVersion): string {
  switch (version.review) {
    case "approved":
      return `v${version.version} · approved in plan v${version.plan_version ?? 0}`;
    case "pending":
      return `v${version.version} · in pending plan v${version.plan_version ?? 0}`;
    default:
      return `v${version.version}`;
  }
}

// One document of the tree: a planning document the ledger recorded, or a file of the repository.
export type TreeItem = {
  key: string;
  title: string;
  path: string;
  kind: DocumentHome;
  recorded: boolean;
  // params are what the address holds to open it.
  params: Record<string, string>;
};

const recordedItem = (document: RecordedDocument): TreeItem => ({
  key: document.id,
  title: document.title || document.id,
  path: document.path ?? "",
  kind: homeOfKind(document.kind),
  recorded: true,
  params: { id: document.id },
});

const fileItem = (document: DocumentRow): TreeItem => ({
  key: `${document.instance}:${document.path}`,
  title: document.title,
  path: document.path,
  kind: homeOfKind(document.kind),
  recorded: false,
  params: { path: document.path, instance: document.instance },
});

// treeOf lists the documents of a project by kind: each file a plan recorded once, from the ledger, with
// every other file of the repository as it stands. A file the ledger holds as a goal, a plan, or a contract
// shows on its own page, so it is left out.
export function treeOf(view: Documents): Record<DocumentHome, TreeItem[]> {
  const tree: Record<DocumentHome, TreeItem[]> = { requirements: [], spec: [], design: [], other: [] };
  for (const plan of view.plans) {
    for (const document of plan.documents) {
      tree[recordedItem(document).kind].push(recordedItem(document));
    }
  }
  for (const document of view.documents.filter((row) => !row.record)) {
    tree[fileItem(document).kind].push(fileItem(document));
  }
  return tree;
}

// openedDocument is the document the address opens: the one it names, else the requirements document that
// holds the requirement it selects, else the first of the kind it names, else the first requirements
// document, else the first document there is.
export function openedDocument(view: Documents, params: URLSearchParams): TreeItem | undefined {
  const tree = treeOf(view);
  const all = Object.values(tree).flat();
  const id = params.get("id");
  const path = params.get("path");
  // A document the address names but the lists do not hold is still asked for, so the page says what the
  // server says of it: a link from outside may name any.
  if (id) {
    return all.find((item) => item.recorded && item.key === id) ?? { key: id, title: id, path: "", kind: "other", recorded: true, params: { id } };
  }
  if (path) {
    const instance = params.get("instance") ?? "";
    return (
      all.find((item) => !item.recorded && item.path === path && (!instance || item.params.instance === instance)) ?? {
        key: `${instance}:${path}`,
        title: path,
        path,
        kind: "other",
        recorded: false,
        params: instance ? { path, instance } : { path },
      }
    );
  }
  const key = params.get("key");
  if (key) {
    const holder = view.plans.flatMap((plan) => plan.requirements).find((trace) => trace.key === key)?.document;
    const found = all.find((item) => item.recorded && item.key === holder);
    if (found) {
      return found;
    }
  }
  const kind = homes.find((home) => home === params.get("kind")) ?? "requirements";
  return tree[kind][0] ?? (params.get("kind") ? undefined : (tree.requirements[0] ?? all[0]));
}

// documentAddress is where a document of the tree opens, with what the address already selects kept.
export function documentAddress(item: TreeItem, extra: Record<string, string | undefined> = {}): string {
  return addressOf("/documents", { ...item.params, ...extra });
}
