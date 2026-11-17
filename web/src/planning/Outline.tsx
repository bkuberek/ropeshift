import { useId, useState } from "react";

import { readOutline } from "../api/client";
import type { DeliveryHistory, PlanForecast } from "../api/measures";
import { readDeliveryHistory, readPlanForecast } from "../api/measures";
import type { PlanOutline } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { addressOf, Link } from "../app/route";
import { DeliveryHistoryView } from "../measures/DeliveryHistory";
import { PlanEstimates } from "../measures/PlanEstimates";
import { TabPanel, Tabs } from "../ui/controls";
import { Badge, Banner } from "../ui/display";

// The outline is read from the repository, so a burst of events reads it once a gap, however much the
// ledger says.
const outlineGapMs = 1000;

// The forecast tick keeps a forecast when what it rests on changes, and at least hourly. A keep takes no revision,
// so no event says a pending forecast came: while it is pending, the outline asks again once a gap.
const forecastGapMs = 30_000;

function Part({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <h3 className="overline outline-title">{title}</h3>
      {children}
    </div>
  );
}

const empty = (outline: PlanOutline) =>
  !outline.documents?.length && !outline.milestones?.length && !outline.contracts?.length && !outline.decisions?.length && !outline.questions?.length;

// OutlineBody lays out what the branch holds: the documents changed or new since the lane began, the plan's
// milestones, its contracts with how many criteria and work orders each lists, the answers it drafts, the
// questions nobody has answered, and whether a request to approve the plan stands for the tip.
export function OutlineBody({ outline, estimates }: { outline: PlanOutline; estimates?: React.ReactNode }) {
  if (outline.unread) {
    return (
      <Banner tone="neutral">
        <strong>The plan cannot be read from the branch.</strong> {outline.unread}
      </Banner>
    );
  }
  return (
    <>
      {empty(outline) ? <p className="muted">Nothing is committed to the branch yet. Only committed files show here.</p> : null}
      {outline.documents?.length ? (
        <Part title="Documents">
          <ul className="stack-8">
            {outline.documents.map((document) => (
              <li key={document.path} className="row">
                {document.status === "new" ? (
                  <Badge tone="success" glyph="dot">
                    New
                  </Badge>
                ) : (
                  <Badge tone="active" glyph="half">
                    Changed
                  </Badge>
                )}
                <span className="mono truncate" title={document.path}>
                  {document.path}
                </span>
              </li>
            ))}
          </ul>
        </Part>
      ) : null}
      {outline.milestones?.length ? (
        <Part title="Milestones">
          <ul className="stack-8">
            {outline.milestones.map((milestone) => (
              <li key={milestone.id}>
                {milestone.title || milestone.id} <span className="subtle">priority {milestone.priority}</span>
              </li>
            ))}
          </ul>
        </Part>
      ) : null}
      {outline.contracts?.length ? (
        <Part title="Contracts">
          <ul className="stack-8">
            {outline.contracts.map((contract) => (
              <li key={contract.id}>
                {contract.title || contract.id}{" "}
                <span className="subtle">
                  {contract.criteria} {contract.criteria === 1 ? "criterion" : "criteria"} · {contract.work_orders} {contract.work_orders === 1 ? "work order" : "work orders"}
                </span>
              </li>
            ))}
          </ul>
        </Part>
      ) : null}
      {outline.questions?.length ? (
        <Part title="Open questions">
          <ul className="stack-8">
            {outline.questions.map((question) => (
              <li key={question.id}>
                <Link to={addressOf("/questions", { id: question.id })}>{question.text}</Link>{" "}
                {question.blocking ? (
                  <Badge tone="warning" glyph="question">
                    Blocking
                  </Badge>
                ) : null}
              </li>
            ))}
          </ul>
        </Part>
      ) : null}
      {outline.decisions?.length ? (
        <Part title="Draft decisions">
          <ul className="stack-8">
            {outline.decisions.map((decision) => (
              <li key={decision.id}>
                <span className="mono">{decision.id}</span> {decision.question} <span className="subtle">{decision.answer}</span>
              </li>
            ))}
          </ul>
        </Part>
      ) : null}
      {estimates ? <Part title="Estimates">{estimates}</Part> : null}
      {outline.approval ? (
        <Banner tone="active" action={<Link className="btn btn-sm" to={addressOf("/plan", { id: outline.approval.id, version: String(outline.approval.version) })}>Review version {outline.approval.version}</Link>}>
          A request to approve version {outline.approval.version} stands for this commit.
        </Banner>
      ) : outline.not_submitted ? (
        <Banner tone="neutral">{outline.not_submitted}</Banner>
      ) : null}
    </>
  );
}

type PaneTab = "outline" | "history";
const paneTabs: { id: PaneTab; label: string }[] = [
  { id: "outline", label: "Outline" },
  { id: "history", label: "History" },
];

// Finished work changes the history only when a work order ends, so a burst of events reads it once a gap.
const historyGapMs = 5000;

// HistoryTab reads the delivery history the planning session's pack holds, as `ropeshift measures history` prints it,
// when the tab is chosen, and again when the ledger says something happened.
function HistoryTab({ live }: { live: Live }) {
  const read = useView<DeliveryHistory>(() => readDeliveryHistory(), "planning:history", live, historyGapMs);
  return (
    <div className="stack small">
      {read.failure ? <Banner tone="danger">{read.view ? `What this shows is not current. ${read.failure}` : read.failure}</Banner> : null}
      {read.view ? <DeliveryHistoryView view={read.view} /> : read.failure ? null : <p className="muted">Reading the history.</p>}
    </div>
  );
}

// Outline follows the plan a planning lane is writing, from its branch. It reads again when the ledger
// says something happened, which is when the session may have committed. A page showing a session that ended
// gives it a title of its own. A settled session is one a member accepted, whose outline is the commit they
// accepted and never changes, so it is read once.
export function Outline({ lane, live, title = "Plan in progress", settled = false }: { lane: string; live: Live; title?: string; settled?: boolean }) {
  const heading = useId();
  const tabs = useId();
  const [tab, setTab] = useState<PaneTab>("outline");
  const followed = settled ? { heard: 0, checkSession: live.checkSession } : live;
  const read = useView(() => readOutline(lane), `outline:${lane}`, followed, outlineGapMs);
  // The estimates and forecasts of the version a request stands for, once the ledger holds its plan. They are
  // read as `ropeshift plan forecast` reads them, and the tick makes them, so they read again at most once a gap.
  const standing = read.view?.approval;
  const plan = standing?.plan_id ?? "";
  const version = standing?.version ?? 0;
  const forecast = useView<PlanForecast | null>(() => (plan ? readPlanForecast(plan, version) : Promise.resolve(null)), `outline:forecast:${plan}:${version}`, live, forecastGapMs, (held) =>
    held?.state === "pending",
  );
  const titles = new Map((read.view?.milestones ?? []).map((milestone) => [milestone.id, milestone.title]));
  const estimates = forecast.view ? (
    <PlanEstimates forecast={forecast.view} titleOf={(id) => titles.get(id) || id} compact />
  ) : forecast.failure ? (
    <p className="muted">{`The estimates are not current. ${forecast.failure}`}</p>
  ) : null;
  return (
    <aside className="panel outline" aria-labelledby={heading}>
      <div className="panel-head">
        <h2 id={heading}>{title}</h2>
        {read.view && !read.failure ? <span className="fresh">live</span> : null}
      </div>
      <div className="panel-body stack small" aria-busy={read.busy}>
        <Tabs label={title} idBase={tabs} selected={tab} tabs={paneTabs} onSelect={setTab} />
        <TabPanel idBase={tabs} selected={tab}>
          {tab === "history" ? (
            <HistoryTab live={live} />
          ) : (
            <div className="stack small">
              {read.failure ? <Banner tone="danger">{read.view ? `What this shows is not current. ${read.failure}` : read.failure}</Banner> : null}
              {read.view ? <OutlineBody outline={read.view} estimates={estimates} /> : null}
              {!read.view && !read.failure ? <p className="muted">Reading the branch.</p> : null}
            </div>
          )}
        </TabPanel>
      </div>
    </aside>
  );
}
