import type { ReactNode } from "react";

import { readView } from "../api/client";
import type { Decisions as DecisionsView, DocumentKind, Documents, NeedsYou, Operations as OperationsView, Plans, Project, Questions, Roadmap as RoadmapView, Stamp } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { FloorPage } from "../floor/Floor";
import { UsagePage, WorkersPage } from "../floor/Installation";
import { Operations } from "../floor/Operations";
import { WorkOrderPage, WorkOrdersPage } from "../floor/WorkOrders";
import { LanePage } from "../lanes/LanePane";
import { Page, StampLine, useClock, ViewTime } from "../parts/parts";
import { Decisions } from "../planning/Decisions";
import { DocumentList, DocumentPage, OneRequirement, Requirements } from "../planning/Documents";
import { OpenQuestions } from "../planning/OpenQuestions";
import { PlanList, PlanPage } from "../planning/Plans";
import { RecordedPage } from "../planning/Recorded";
import { Roadmap } from "../planning/Roadmap";
import { LinkTabs } from "../ui/controls";
import { Banner, EmptyState, SkeletonRows } from "../ui/display";
import { addressOf, Link } from "./route";
import type { View } from "./routes";

// A document list runs git for each file, so a page that follows the stream reads it this seldom.
const documentsGapMs = 30_000;

type Spec<T> = {
  name: string;
  live: Live;
  category?: string;
  gapMs?: number;
  title: string | ((view: T | null) => string);
  note?: (view: T) => string;
  crumbs?: ReactNode;
  // above stands between the title and the view's body, such as its tabs.
  above?: ReactNode;
  body: (view: T) => ReactNode;
};

// ViewPage follows one read model. The title stands from the first moment, so a person who moved here
// has somewhere for focus to land, and skeleton rows stand where the body will be. A failed read shows
// in place of the body, and a view that has data keeps it under a banner that says it is not current.
function ViewPage<T extends Stamp>({ name, live, category = "", gapMs, title, note, crumbs, above, body }: Spec<T>) {
  const read = useView<T>(() => readView<T>(name, category), `${name}?${category}`, live, gapMs);
  const now = useClock();
  const view = read.view;
  return (
    <Page title={typeof title === "function" ? title(view) : title} note={view && note ? note(view) : undefined} crumbs={crumbs}>
      {above}
      <div className={above ? "tab-body" : undefined}>
        {read.failure ? <Banner tone="danger">{view ? `What this view shows is not current. ${read.failure}` : read.failure}</Banner> : null}
        {view ? (
          <ViewTime.Provider value={{ asOf: view.as_of, elapsed: Math.max(0, now - read.readAt) }}>
            <div className="view-body stack" aria-busy={read.busy}>
              {body(view)}
            </div>
            <StampLine stamp={view} />
          </ViewTime.Provider>
        ) : null}
        {!view && !read.failure ? (
          <div className="panel">
            <SkeletonRows rows={5} />
          </div>
        ) : null}
      </div>
    </Page>
  );
}

const count = (amount: number, noun: string) => `${amount} ${noun}${amount === 1 ? "" : "s"}`;

const documentKinds: { kind: DocumentKind; label: string; empty: string }[] = [
  { kind: "requirements", label: "Requirements", empty: "No requirements yet." },
  { kind: "spec", label: "Specs", empty: "No specs yet." },
  { kind: "design", label: "Designs", empty: "No designs yet." },
  { kind: "other", label: "Other", empty: "No other documents yet." },
];

function documentsPage(live: Live, params: URLSearchParams) {
  const param = (name: string) => params.get(name) ?? "";
  if (param("id")) {
    return <RecordedPage id={param("id")} version={param("version")} live={live} />;
  }
  if (param("path")) {
    return <DocumentPage reference={{ path: param("path"), instance: param("instance"), version: param("version") }} live={live} />;
  }
  const kind = documentKinds.find((row) => row.kind === param("kind"))?.kind ?? "requirements";
  const key = param("key");
  const tabs = (
    <LinkTabs
      label="Kinds"
      tabs={documentKinds.map((row) => ({ to: addressOf("/documents", { kind: row.kind }), label: row.label, current: row.kind === kind && !key }))}
    />
  );
  if (kind === "requirements" && key) {
    const find = (view: Documents | null) => view?.requirements.find((requirement) => requirement.key === key);
    return (
      <ViewPage<Documents>
        name="documents"
        live={live}
        gapMs={documentsGapMs}
        title={(view) => (view === null || find(view) ? key : "No such requirement")}
        crumbs={
          <>
            <Link to="/documents?kind=requirements">Requirements</Link>
            <span aria-hidden="true">›</span>
            <span>{key}</span>
          </>
        }
        body={(view) => {
          const requirement = find(view);
          return requirement ? (
            <OneRequirement requirement={requirement} />
          ) : (
            <EmptyState icon="trace" title="No requirement with that key" action={<Link to="/documents?kind=requirements">All requirements</Link>} />
          );
        }}
      />
    );
  }
  const spec = documentKinds.find((row) => row.kind === kind);
  return (
    <ViewPage<Documents>
      name="documents"
      live={live}
      gapMs={documentsGapMs}
      title="Documents"
      above={tabs}
      body={(view) => (kind === "requirements" ? <Requirements view={view} /> : <DocumentList view={view} kind={kind} empty={spec?.empty ?? "None yet."} />)}
    />
  );
}

function plansPage(live: Live, id: string) {
  const find = (view: Plans | null) => view?.plans.find((row) => row.plan.id === id);
  if (!id) {
    return <ViewPage<Plans> name="plans" live={live} title="Plans" note={(view) => count(view.plans.length, "plan")} body={(view) => <PlanList view={view} />} />;
  }
  return (
    <ViewPage<Plans>
      name="plans"
      live={live}
      title={(view) => (view === null ? "Plan" : (find(view)?.plan.title ?? "No such plan"))}
      crumbs={
        <>
          <Link to="/plans">Plans</Link>
          <span aria-hidden="true">›</span>
          <span>{id}</span>
        </>
      }
      body={(view) => {
        const detail = find(view);
        return detail ? <PlanPage detail={detail} /> : <EmptyState icon="plan" title="No plan with that ID" action={<Link to="/plans">All plans</Link>} />;
      }}
    />
  );
}

type PageProps = { view: View; project: string; params: URLSearchParams; live: Live; after: () => Promise<void>; needs: NeedsYou; projects: Project[] };

// PageFor shows the view an address names. Needs you is not here, because it stays mounted behind every
// other view, so a half-written answer survives a look elsewhere.
export function PageFor({ view, project, params, live, after, needs, projects }: PageProps) {
  const param = (name: string) => params.get(name) ?? "";
  const label = projects.find((row) => row.name === project)?.display_name || project;
  switch (view) {
    case "roadmap":
      return <ViewPage<RoadmapView> name="roadmap" live={live} title="Roadmap" body={(read) => <Roadmap view={read} />} />;
    case "plans":
      return plansPage(live, param("plan"));
    case "documents":
      return documentsPage(live, params);
    case "decisions":
      return (
        <ViewPage<DecisionsView>
          name="decisions"
          live={live}
          title={(read) => (!param("id") ? "Decisions" : read === null || read.decisions.some((row) => row.id === param("id")) ? param("id") : "No such decision")}
          note={(read) => (param("id") ? "" : count(read.decisions.length, "decision"))}
          body={(read) => <Decisions view={read} selected={param("id")} />}
        />
      );
    case "questions":
      return (
        <ViewPage<Questions>
          name="questions"
          live={live}
          category={param("category")}
          title="Open questions"
          note={(read) => `${read.questions.length} open`}
          body={(read) => <OpenQuestions view={read} category={param("category")} selected={param("id")} />}
        />
      );
    case "work":
      return <WorkOrdersPage live={live} group={param("group")} />;
    case "work-order":
      return <WorkOrderPage key={param("id")} live={live} id={param("id")} tab={param("tab")} needs={needs} after={after} />;
    case "floor":
      return <FloorPage live={live} project={project} projects={projects} show={param("show")} after={after} />;
    case "lane":
      return <LanePage key={param("id")} id={param("id")} live={live} />;
    case "measures":
      return <ViewPage<OperationsView> name="operations" live={live} title="Measures" note={() => "The fleet, the counts by state, the sources, and the product measures, from the ledger."} body={(read) => <Operations view={read} />} />;
    case "workers":
      return <WorkersPage live={live} projects={projects} selected={param("worker")} after={after} />;
    case "usage":
      return <UsagePage live={live} project={project} label={label} />;
    case "needs-you":
      return null;
  }
}

export function NoSuchView() {
  return (
    <Page title="No such view">
      <div className="panel">
        <EmptyState icon="info" title="This address holds no view" action={<Link to="/needs-you">Back to Needs you</Link>} />
      </div>
    </Page>
  );
}
