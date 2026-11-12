import { lazy, Suspense } from "react";
import type { ReactNode } from "react";

import type { Prefs } from "../admin/Settings";
import { readView } from "../api/client";
import type { Decisions as DecisionsView, Documents, NeedsYou, Operations as OperationsView, Plans, Project, Questions, Roadmap as RoadmapView, Stamp } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { FloorPage } from "../floor/Floor";
import { UsagePage, WorkersPage } from "../floor/Installation";
import { Operations } from "../floor/Operations";
import { WorkOrderPage, WorkOrdersPage } from "../floor/WorkOrders";
import { LanePage } from "../lanes/LanePane";
import { Page, StampLine, useClock, ViewTime } from "../parts/parts";
import { Decisions } from "../planning/Decisions";
import { DocumentsBody, DocumentsTabs } from "../planning/Documents";
import { OpenQuestions } from "../planning/OpenQuestions";
import { PlanningPage } from "../planning/Planning";
import { PlanList, PlanPage } from "../planning/Plans";
import { Roadmap } from "../planning/Roadmap";
import { Banner, EmptyState, SkeletonRows } from "../ui/display";
import { Link } from "./route";
import type { View } from "./routes";
import { titleOf } from "./routes";

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
  actions?: ReactNode;
  // above stands between the title and the view's body, such as its tabs.
  above?: ReactNode;
  body: (view: T) => ReactNode;
};

// ViewPage follows one read model. The title stands from the first moment, so a person who moved here
// has somewhere for focus to land, and skeleton rows stand where the body will be. A failed read shows
// in place of the body, and a view that has data keeps it under a banner that says it is not current.
function ViewPage<T extends Stamp>({ name, live, category = "", gapMs, title, note, crumbs, actions, above, body }: Spec<T>) {
  const read = useView<T>(() => readView<T>(name, category), `${name}?${category}`, live, gapMs);
  const now = useClock();
  const view = read.view;
  return (
    <Page title={typeof title === "function" ? title(view) : title} note={view && note ? note(view) : undefined} crumbs={crumbs} actions={actions}>
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

// The documents view and the traceability matrix are one view with two tabs, each with an address of its own.
function documentsPage(live: Live, params: URLSearchParams, project: string, tab: "reader" | "traceability") {
  const keyed = params.get("key") ?? "";
  return (
    <ViewPage<Documents>
      name="documents"
      live={live}
      gapMs={documentsGapMs}
      title="Documents"
      note={() => "Requirements, specs, and designs at their versions, traced to criteria, work, and verdicts."}
      actions={<DocumentsTabs tab={tab} keyed={keyed} />}
      body={(view) => <DocumentsBody view={view} live={live} params={params} tab={tab} project={project} />}
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

// The pages that manage the installation and a project's records load when a person first opens one,
// so the views of daily work load without them.
const AgentsPage = lazy(() => import("../admin/Agents").then((page) => ({ default: page.AgentsPage })));
const AuditView = lazy(() => import("../admin/Audit").then((page) => ({ default: page.AuditView })));
const ImportPage = lazy(() => import("../admin/Import").then((page) => ({ default: page.ImportPage })));
const MembersPage = lazy(() => import("../admin/Members").then((page) => ({ default: page.MembersPage })));
const ProjectsPage = lazy(() => import("../admin/Projects").then((page) => ({ default: page.ProjectsPage })));
const RepositoriesPage = lazy(() => import("../admin/Repositories").then((page) => ({ default: page.RepositoriesPage })));
const SettingsPage = lazy(() => import("../admin/Settings").then((page) => ({ default: page.SettingsPage })));

// Loading stands while a page's code loads: its title at once, and skeleton rows where its body will be.
function Loading({ view }: { view: View }) {
  return (
    <Page title={titleOf(view)}>
      <div className="panel">
        <SkeletonRows rows={5} />
      </div>
    </Page>
  );
}

type PageProps = {
  view: View;
  project: string;
  // scoped says the address names the project, which makes a view with both scopes the project's.
  scoped: boolean;
  params: URLSearchParams;
  live: Live;
  after: () => Promise<void>;
  needs: NeedsYou;
  projects: Project[];
  reloadProjects: () => Promise<void>;
  prefs: Prefs;
  onSignedOut: () => void;
};

// PageFor shows the view an address names. Needs you is not here, because it stays mounted behind every
// other view, so a half-written answer survives a look elsewhere.
export function PageFor(props: PageProps) {
  return (
    <Suspense fallback={<Loading view={props.view} />}>
      <ViewFor {...props} />
    </Suspense>
  );
}

function ViewFor({ view, project, scoped, params, live, after, needs, projects, reloadProjects, prefs, onSignedOut }: PageProps) {
  const param = (name: string) => params.get(name) ?? "";
  const label = projects.find((row) => row.name === project)?.display_name || project;
  const archived = projects.find((row) => row.name === project)?.state === "archived";
  const scope = scoped ? "project" : "installation";
  switch (view) {
    case "roadmap":
      return <ViewPage<RoadmapView> name="roadmap" live={live} title="Roadmap" body={(read) => <Roadmap view={read} />} />;
    case "plans":
      return plansPage(live, param("plan"));
    case "planning":
      return <PlanningPage live={live} project={project} label={label} lane={param("lane")} after={after} />;
    case "documents":
      return documentsPage(live, params, project, "reader");
    case "traceability":
      return documentsPage(live, params, project, "traceability");
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
      return <WorkersPage live={live} projects={projects} selected={param("worker")} after={after} dialog={param("dialog")} />;
    case "projects":
      return <ProjectsPage live={live} projects={projects} reload={reloadProjects} dialog={param("dialog")} />;
    case "import":
      return <ImportPage projects={projects} chosen={param("project")} reload={reloadProjects} />;
    case "members":
      return <MembersPage live={live} onSignedOut={onSignedOut} />;
    case "repositories":
      return <RepositoriesPage live={live} project={project} label={label} selected={param("repo")} dialog={param("dialog")} archived={archived} />;
    case "settings":
      return <SettingsPage live={live} project={scoped ? project : ""} label={label} archived={archived} section={param("section")} worker={param("worker")} prefs={prefs} />;
    case "agents":
      return <AgentsPage live={live} project={project} label={label} archived={archived} scope={scope} agent={param("agent")} tab={param("tab")} />;
    case "audit":
      return <AuditView live={live} project={project} label={label} scope={scope} actor={param("actor")} kind={param("kind")} />;
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
