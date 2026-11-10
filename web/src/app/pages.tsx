import { useEffect } from "react";
import type { ReactNode } from "react";

import { readView } from "../api/client";
import type {
  Completed as CompletedView,
  Decisions as DecisionsView,
  Documents,
  InProgress as InProgressView,
  OnHold as OnHoldView,
  Operations as OperationsView,
  Plans,
  Questions,
  Roadmap as RoadmapView,
  ShopFloor as ShopFloorView,
  Stamp,
  Stuck as StuckView,
} from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { Operations } from "../floor/Operations";
import { ShopFloor } from "../floor/ShopFloor";
import { workLabel } from "../floor/states";
import { Completed, InProgress, OnHold, Stuck } from "../floor/Work";
import { Page, StampLine, useClock, ViewTime } from "../parts/parts";
import { Decisions } from "../planning/Decisions";
import { DocumentList, DocumentPage, OneRequirement, Requirements } from "../planning/Documents";
import { RecordedPage } from "../planning/Recorded";
import { OpenQuestions } from "../planning/OpenQuestions";
import { PlanList, PlanPage } from "../planning/Plans";
import { Roadmap } from "../planning/Roadmap";
import { Link } from "./route";
import type { Route } from "./route";

// A document list runs git for each file, so a page that follows the stream reads it this seldom.
const documentsGapMs = 30_000;

type Spec<T> = {
  name: string;
  live: Live;
  category?: string;
  gapMs?: number;
  title: string | ((view: T | null) => string);
  note?: (view: T) => string;
  body: (view: T) => ReactNode;
};

// ViewPage follows one read model. The title stands from the first moment, so a person who moved here
// has somewhere for focus to land before the data arrives.
function ViewPage<T extends Stamp>({ name, live, category = "", gapMs, title, note, body }: Spec<T>) {
  const read = useView<T>(() => readView<T>(name, category), `${name}?${category}`, live, gapMs);
  const now = useClock();
  const view = read.view;
  return (
    <Page title={typeof title === "function" ? title(view) : title} note={view && note ? note(view) : undefined}>
      {read.failure ? (
        <p className="refusal" role="alert">
          {read.failure}
        </p>
      ) : null}
      {view ? (
        <ViewTime.Provider value={{ asOf: view.as_of, elapsed: Math.max(0, now - read.readAt) }}>
          <div aria-busy={read.busy}>{body(view)}</div>
          <StampLine stamp={view} />
        </ViewTime.Provider>
      ) : null}
      {!view && !read.failure ? <p className="quiet">Reading.</p> : null}
    </Page>
  );
}

const count = (amount: number, noun: string) => `${amount} ${noun}${amount === 1 ? "" : "s"}`;

// tally counts a view's work orders by state, in the order given and any other state after, and leaves
// out a state that holds none, so a count says what it counts: "1 running, 1 claimed".
function tally(orders: { work_order: { state: string } }[], order: string[], none: string): string {
  const counts = new Map<string, number>();
  for (const { work_order } of orders) {
    counts.set(work_order.state, (counts.get(work_order.state) ?? 0) + 1);
  }
  const states = [...order.filter((state) => counts.has(state)), ...[...counts.keys()].filter((state) => !order.includes(state))];
  return states.length ? states.map((state) => `${counts.get(state)} ${workLabel(state).toLowerCase()}`).join(", ") : none;
}

// A page's name for the browser. It holds no record's text, so a tab title never carries an agent's words.
const titles: Record<string, string> = {
  "/": "Needs you",
  "/roadmap": "Roadmap",
  "/plans": "Plans",
  "/requirements": "Requirements",
  "/specs": "Specs",
  "/designs": "Designs",
  "/documents": "Other documents",
  "/document": "Document",
  "/decisions": "Decisions",
  "/questions": "Open questions",
  "/in-progress": "In progress",
  "/on-hold": "On hold",
  "/stuck": "Stuck",
  "/completed": "Completed",
  "/shop-floor": "Shop floor",
  "/operations": "Operations",
};

export function useDocumentTitle(path: string): void {
  useEffect(() => {
    document.title = `${titles[path] ?? "No such view"} - ropeshift`;
  }, [path]);
}

function documentPage(live: Live, title: string, kind: "spec" | "design" | "other", empty: string) {
  return <ViewPage<Documents> name="documents" live={live} gapMs={documentsGapMs} title={title} body={(view) => <DocumentList view={view} kind={kind} empty={empty} />} />;
}

function requirementsPage(live: Live, key: string) {
  const find = (view: Documents | null) => view?.requirements.find((requirement) => requirement.key === key);
  if (!key) {
    return <ViewPage<Documents> name="documents" live={live} gapMs={documentsGapMs} title="Requirements" body={(view) => <Requirements view={view} />} />;
  }
  return (
    <ViewPage<Documents>
      name="documents"
      live={live}
      gapMs={documentsGapMs}
      title={(view) => (view === null || find(view) ? key : "No such requirement")}
      body={(view) => {
        const requirement = find(view);
        return requirement ? (
          <OneRequirement requirement={requirement} />
        ) : (
          <p className="back">
            <Link to="/requirements">All requirements</Link>
          </p>
        );
      }}
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
      body={(view) => {
        const detail = find(view);
        return detail ? (
          <PlanPage detail={detail} />
        ) : (
          <p className="back">
            <Link to="/plans">All plans</Link>
          </p>
        );
      }}
    />
  );
}

function decisionsPage(live: Live, id: string) {
  return (
    <ViewPage<DecisionsView>
      name="decisions"
      live={live}
      title={(view) => (!id ? "Decisions" : view === null || view.decisions.some((row) => row.id === id) ? id : "No such decision")}
      note={(view) => (id ? "" : count(view.decisions.length, "decision"))}
      body={(view) => <Decisions view={view} selected={id} />}
    />
  );
}

function questionsPage(live: Live, category: string, id: string) {
  return (
    <ViewPage<Questions>
      name="questions"
      live={live}
      category={category}
      title="Open questions"
      note={(view) => `${view.questions.length} open`}
      body={(view) => <OpenQuestions view={view} category={category} selected={id} />}
    />
  );
}

// PageFor shows the view an address names. Needs you is not here, because it stays mounted behind every
// other view, so a half-written answer survives a look elsewhere.
export function PageFor({ route, live, after }: { route: Route; live: Live; after: () => Promise<void> }) {
  const { path, params } = route;
  const param = (name: string) => params.get(name) ?? "";
  switch (path) {
    case "/roadmap":
      return <ViewPage<RoadmapView> name="roadmap" live={live} title="Roadmap" body={(view) => <Roadmap view={view} />} />;
    case "/plans":
      return plansPage(live, param("plan"));
    case "/requirements":
      return requirementsPage(live, param("key"));
    case "/specs":
      return documentPage(live, "Specs", "spec", "No specs yet.");
    case "/designs":
      return documentPage(live, "Designs", "design", "No designs yet.");
    case "/documents":
      return documentPage(live, "Other documents", "other", "No other documents yet.");
    case "/document":
      // A planning document the ledger recorded is named by its reference, and a file by its path.
      return param("id") ? (
        <RecordedPage id={param("id")} version={param("version")} live={live} />
      ) : (
        <DocumentPage reference={{ path: param("path"), instance: param("instance"), version: param("version") }} live={live} />
      );
    case "/decisions":
      return decisionsPage(live, param("id"));
    case "/questions":
      return questionsPage(live, param("category"), param("id"));
    case "/in-progress":
      return <ViewPage<InProgressView> name="in-progress" live={live} title="In progress" note={(view) => tally(view.items, ["running", "claimed"], "0 running")} body={(view) => <InProgress view={view} />} />;
    case "/on-hold":
      return <ViewPage<OnHoldView> name="on-hold" live={live} title="On hold" note={(view) => `${view.items.length} parked`} body={(view) => <OnHold view={view} />} />;
    case "/stuck":
      return <ViewPage<StuckView> name="stuck" live={live} title="Stuck" note={(view) => `${view.items.length} stopped`} body={(view) => <Stuck view={view} after={after} />} />;
    case "/completed":
      return <ViewPage<CompletedView> name="completed" live={live} title="Completed" note={(view) => tally(view.items, ["done", "failed", "canceled", "acceptance"], "0 ended")} body={(view) => <Completed view={view} />} />;
    case "/shop-floor":
      return <ViewPage<ShopFloorView> name="shop-floor" live={live} title="Shop floor" note={(view) => count(view.lanes.length, "lane")} body={(view) => <ShopFloor view={view} />} />;
    case "/operations":
      return <ViewPage<OperationsView> name="operations" live={live} title="Operations" body={(view) => <Operations view={view} />} />;
    default:
      return (
        <Page title="No such view">
          <p className="back">
            <Link to="/">Back to Needs you</Link>
          </p>
        </Page>
      );
  }
}
