import { useContext, useState } from "react";

import { confirmRevision, reworkForDecisions } from "../api/client";
import type { PlanRevisionView, RevisionDecision, RevisionFinishedWork } from "../api/types";
import { addressOf, CurrentProject, ItemProject, Link } from "../app/route";
import { StartSessionDialog } from "../planning/StartSession";
import { Button } from "../ui/controls";
import { DecisionCard, DecisionSection } from "../ui/DecisionCard";
import { Fact, Facts, Id, Refusal } from "../ui/display";
import type { Frame } from "./frame";
import { useAct } from "./useAct";

const kindWords = { reversible: "Reversible", one_way: "One-way" } as const;

function Decisions({ decisions }: { decisions: RevisionDecision[] }) {
  return (
    <ol className="drafts" aria-label="Decisions since the approved version">
      {decisions.map((decision) => (
        <li key={decision.id} className="draft">
          <p className="question-text">{decision.statement}</p>
          <p className="muted">
            {kindWords[decision.reversibility]}
            {decision.scope ? `, binds ${decision.scope}` : ""}
            {" · "}
            <Id value={decision.id} />
          </p>
        </li>
      ))}
    </ol>
  );
}

// Finished is the work a reversible decision reached, which is not reworked until a member says so: one rework for
// each work order, naming the decisions that reached it.
function Finished({ work, busy, rework }: { work: RevisionFinishedWork[]; busy: boolean; rework: (work: RevisionFinishedWork) => void }) {
  return (
    <ul className="links" aria-label="Finished work these decisions reached">
      {work.map((row) => (
        <li key={row.work_order} className="row-tight">
          <span>
            {row.title || row.work_order} <span className="muted">(decisions {row.decisions.join(", ")})</span>
          </span>
          {row.rework ? (
            <span className="muted">
              Reworked as <Id value={row.rework} />
            </span>
          ) : (
            <Button disabled={busy} aria-label={`Create rework for ${row.title || row.work_order}`} onClick={() => rework(row)}>
              Create rework
            </Button>
          )}
        </li>
      ))}
    </ul>
  );
}

// PlanRevisionCard is one plan whose approved version decisions have moved past (FR-PLAN-11). A member confirms that
// the version stands with those decisions, which releases the work that waited, or starts a planning session that
// revises the plan, whose approval shows the decisions beside what it changes. Finished work a reversible decision
// reached gets rework only when the member asks for it, one work order at a time.
export function PlanRevisionCard({ need, after, frame }: { need: PlanRevisionView; after: () => Promise<void>; frame: Frame }) {
  const [revising, setRevising] = useState(false);
  const { act, busy, failure } = useAct(after);
  const item = useContext(ItemProject);
  const current = useContext(CurrentProject);
  const project = item ?? current;
  const version = need.revision?.approved_version ?? 0;
  return (
    <DecisionCard
      kind={{ icon: "plan", name: "Plan revision" }}
      project={frame.project}
      title={frame.title}
      provenance={frame.provenance}
      binding={`Version ${version} stays approved. Approving a new version or confirming this one ends the wait.`}
      actions={
        <>
          <Button disabled={busy} onClick={() => setRevising(true)}>
            Revise the plan
          </Button>
          <Button variant="primary" loading={busy} disabled={busy} onClick={() => void act(() => confirmRevision(need.plan))}>
            {`Confirm version ${version}`}
          </Button>
        </>
      }
    >
      <p>
        A member decided something inside the approved plan. Its unstarted work waits in contract review until you confirm that version
        against these decisions, or approve a new version that reflects them.
      </p>
      <Facts>
        <Fact name="Plan">{need.title}</Fact>
        <Fact name="Approved version">{version}</Fact>
        <Fact name="Waiting">{need.waiting.length ? `${need.waiting.length} work ${need.waiting.length === 1 ? "order" : "orders"} in contract review` : "No work waits"}</Fact>
      </Facts>
      <DecisionSection title={`Decisions since version ${version}`}>
        <Decisions decisions={need.decisions} />
      </DecisionSection>
      {need.finished.length ? (
        <DecisionSection title="Finished work they reached">
          <Finished work={need.finished} busy={busy} rework={(row) => void act(() => reworkForDecisions(row.work_order, row.decisions))} />
        </DecisionSection>
      ) : null}
      <p>
        <Link to={addressOf("/plans", { plan: need.plan })}>Read the plan</Link>
      </p>
      {failure ? <Refusal>{failure}</Refusal> : null}
      {revising ? <StartSessionDialog project={project ?? ""} revise={{ plan: need.plan, title: need.title }} onClose={() => setRevising(false)} /> : null}
    </DecisionCard>
  );
}
