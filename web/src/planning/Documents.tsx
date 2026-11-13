import type { Documents } from "../api/types";
import type { Live } from "../api/useView";
import { LinkTabs } from "../ui/controls";
import { EmptyState } from "../ui/display";
import { Icon } from "../ui/Icon";
import { Link } from "../app/route";
import { documentAddress, homes, homeWords, openedDocument, treeOf } from "./documentTree";
import type { TreeItem } from "./documentTree";
import { FileReader, RecordedReader } from "./Reader";
import { StartSession } from "./StartSession";
import { Matrix, TracePanel } from "./Trace";
import { documentsAddress } from "./requirementTrace";

// DocumentsTabs are the view's two tabs. Each has an address of its own, so switching between them changes the
// address, and a requirement a person selected goes with it.
export function DocumentsTabs({ tab, keyed }: { tab: "reader" | "traceability"; keyed: string }) {
  return (
    <LinkTabs
      label="Documents view"
      tabs={[
        { to: documentsAddress("reader", keyed), label: "Reader", current: tab === "reader" },
        { to: documentsAddress("traceability", keyed), label: "Traceability", current: tab === "traceability" },
      ]}
    />
  );
}

// Tree lists the documents by kind, the one that is open marked. A file a plan recorded lists once, from the
// ledger, with the other files as they stand.
function Tree({ view, open, keyed }: { view: Documents; open: TreeItem | undefined; keyed: string }) {
  const tree = treeOf(view);
  return (
    <nav className="tree" aria-label="Documents">
      {homes.map((home) => (
        <div key={home}>
          <p className="overline tree-title">{homeWords[home]}</p>
          {tree[home].length === 0 ? <p className="tree-none small subtle">None yet</p> : null}
          {tree[home].map((item) => (
            <Link key={item.key} to={documentAddress(item, { key: item.key === open?.key ? keyed : "" })} aria-current={item.key === open?.key ? "page" : undefined} title={item.path}>
              <Icon name="doc" size="sm" />
              <span className="truncate">{item.title}</span>
            </Link>
          ))}
        </div>
      ))}
    </nav>
  );
}

// DocumentsBody is the reader and the matrix: three columns, a document tree by kind, the reader with its
// version selector and review state, and the trace panel for the requirement the address selects.
export function DocumentsBody({ view, live, params, tab, project }: { view: Documents; live: Live; params: URLSearchParams; tab: "reader" | "traceability"; project: string }) {
  const keyed = params.get("key") ?? "";
  if (tab === "traceability") {
    return <Matrix view={view} keyed={keyed} />;
  }
  const open = openedDocument(view, params);
  const none = view.documents.length === 0 && view.plans.every((plan) => plan.documents.length === 0);
  if (none) {
    return (
      <div className="panel">
        <EmptyState icon="doc" title="No documents yet" action={<StartSession project={project} variant="primary" />}>
          Documents appear when a plan or an adoption adds them.
        </EmptyState>
      </div>
    );
  }
  return (
    <div className="grid grid-docs">
      <Tree view={view} open={open} keyed={keyed} />
      {open ? (
        open.recorded ? (
          <RecordedReader key={`${open.key}:${params.get("version") ?? ""}`} item={open} view={view} live={live} params={params} />
        ) : (
          <FileReader key={`${open.key}:${params.get("version") ?? ""}`} item={open} live={live} params={params} />
        )
      ) : (
        <div className="panel">
          <EmptyState icon="doc" title={params.get("id") || params.get("path") ? "No such document" : "No document of that kind"}>
            {params.get("id") || params.get("path") ? "No document with that address is held." : "Choose another kind in the list."}
          </EmptyState>
        </div>
      )}
      <TracePanel view={view} keyed={keyed} />
    </div>
  );
}
