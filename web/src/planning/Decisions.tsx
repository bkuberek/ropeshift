import type { Decision, Decisions as View } from "../api/types";
import { addressOf, Link } from "../app/route";
import { Fact, Id, Line, words } from "../parts/parts";
import { ScopeName } from "../ui/display";
import { CategoryFilter } from "./CategoryFilter";
import { OverturnDecisionButton } from "./OverturnDecision";
import { inForce } from "./records";

const decisionAt = (id: string) => addressOf("/decisions", { id });

function Lineage({ decision }: { decision: Decision }) {
  return (
    <ol className="lineage" aria-label="Lineage">
      {decision.lineage.map((id) => (
        <li key={id}>
          {id === decision.id ? (
            <span className="mono" aria-current="true">
              {id}
            </span>
          ) : (
            <Link className="mono" to={decisionAt(id)}>
              {id}
            </Link>
          )}
        </li>
      ))}
    </ol>
  );
}

function Linked({ ids }: { ids: string[] }) {
  return (
    <ul className="links">
      {ids.map((id) => (
        <li key={id}>
          <Link className="mono" to={decisionAt(id)}>
            {id}
          </Link>
        </li>
      ))}
    </ul>
  );
}

function DecisionLine({ decision }: { decision: Decision }) {
  return (
    <Line side={<span className="wait-word">{words(decision.status)}</span>} kind={<Id value={decision.id} />} title={decision.statement} plain>
      <div className="panel-body">
        {decision.rationale ? <p className="lead">{decision.rationale}</p> : null}
        {decision.lineage.length > 1 ? <Lineage decision={decision} /> : null}
        <dl className="kv">
          {decision.supersedes ? (
            <Fact name="Replaces">
              <Linked ids={[decision.supersedes]} />
            </Fact>
          ) : null}
          {decision.superseded_by ? (
            <Fact name="Replaced by">
              <Linked ids={[decision.superseded_by]} />
            </Fact>
          ) : null}
          {decision.scope ? (
            <Fact name="Scope">
              <ScopeName scope={decision.scope} title={decision.scope_title} />
            </Fact>
          ) : null}
          {decision.reversibility ? <Fact name="Reversibility">{words(decision.reversibility)}</Fact> : null}
          {decision.categories?.length ? <Fact name="Categories">{decision.categories.map((name) => words(name)).join(", ")}</Fact> : null}
          {decision.decided_by ? (
            <Fact name="Decided by">{decision.decided_by_name ? <span title={decision.decided_by}>{decision.decided_by_name}</span> : <Id value={decision.decided_by} />}</Fact>
          ) : null}
          {decision.policy_version ? <Fact name="Policy version">{decision.policy_version}</Fact> : null}
          {decision.question_id ? (
            <Fact name="Question">
              <Id value={decision.question_id} />
            </Fact>
          ) : null}
          {decision.citations?.length ? (
            <Fact name="Cites" mono>
              {decision.citations.join(", ")}
            </Fact>
          ) : null}
          {decision.overlaps?.length ? (
            <Fact name="Overlaps">
              <Linked ids={decision.overlaps} />
            </Fact>
          ) : null}
          {decision.no_conflict?.length ? (
            <Fact name="Judged compatible with">
              <Linked ids={decision.no_conflict} />
            </Fact>
          ) : null}
        </dl>
        {inForce(decision) ? (
          <div className="row-tight">
            <OverturnDecisionButton decision={decision} />
          </div>
        ) : null}
      </div>
    </Line>
  );
}

// The values of the filter of who recorded a decision. A kind of recorder and one recorder's ID each carry a prefix, so
// no recorder's ID reads as a kind: a member whose ID is "orchestrator" is filtered by id:orchestrator.
const byOrchestrator = "kind:orchestrator";
const byMember = "kind:member";
const byRecorder = (id: string) => `id:${id}`;

// recordedBy says whether a decision was recorded by who the filter names. A kind is the one the ledger recorded the
// decision under, so a decision whose orchestrator's worker is gone still counts as an orchestrator's.
function recordedBy(decision: Decision, by: string): boolean {
  switch (by) {
    case "":
      return true;
    case byOrchestrator:
      return decision.decided_by_kind === "orchestrator";
    case byMember:
      return decision.decided_by_kind === "member";
  }
  return by.startsWith("id:") && decision.decided_by === by.slice("id:".length);
}

// recorderName names one recorder: an orchestrator by the machine its worker runs on, or by its ID when the ledger no
// longer names the worker, and anyone else by ID.
function recorderName(decision: Decision): string {
  if (decision.decided_by_name) {
    return `Orchestrator on ${decision.decided_by_name}`;
  }
  return decision.decided_by_kind === "orchestrator" ? `Orchestrator ${decision.decided_by}` : (decision.decided_by ?? "");
}

// RecorderFilter narrows the decisions to those anyone, any orchestrator, any member, or one recorder recorded, a link
// each so the address keeps the filter, with the category in force kept (FR-QD-13).
function RecorderFilter({ decisions, by, category }: { decisions: Decision[]; by: string; category: string }) {
  const recorders: { value: string; name: string }[] = [];
  for (const decision of decisions) {
    const value = decision.decided_by ? byRecorder(decision.decided_by) : "";
    if (value && !recorders.some((recorder) => recorder.value === value)) {
      recorders.push({ value, name: recorderName(decision) });
    }
  }
  const options = [{ value: "", name: "Anyone" }, { value: byOrchestrator, name: "An orchestrator" }, { value: byMember, name: "A member" }, ...recorders];
  return (
    <nav className="filters" aria-label="Filter by who recorded">
      {options.map((option) => (
        <Link key={option.value} to={addressOf("/decisions", { by: option.value, category })} stay aria-current={option.value === by ? "true" : undefined}>
          {option.name}
        </Link>
      ))}
    </nav>
  );
}

// emptyWords says who the filter named, when they recorded nothing the list holds.
function emptyWords(by: string, category: string): string {
  if (by === byOrchestrator) {
    return "No orchestrator recorded a decision here.";
  }
  if (by === byMember) {
    return "No member recorded a decision here.";
  }
  if (by.startsWith("id:")) {
    return `${by.slice("id:".length)} recorded no decision here.`;
  }
  return category ? `Nothing decided in ${words(category)}.` : "No decisions yet.";
}

export function Decisions({ view, selected, category, categories, by = "" }: { view: View; selected: string; category: string; categories: string[]; by?: string }) {
  const recorded = view.decisions.filter((decision) => recordedBy(decision, by));
  const shown = selected ? view.decisions.filter((decision) => decision.id === selected) : recorded;
  return (
    <>
      {selected ? (
        <p className="back">
          <Link to="/decisions">All decisions</Link>
        </p>
      ) : (
        <>
          <RecorderFilter decisions={view.decisions} by={by} category={category} />
          {categories.length ? <CategoryFilter path="/decisions" categories={categories} category={category} keep={{ by }} /> : null}
        </>
      )}
      {!selected && recorded.length === 0 ? <p className="empty">{emptyWords(by, category)}</p> : null}
      {shown.length ? (
        <ol className="record-list queue-work" role="list">
          {shown.map((decision) => (
            <DecisionLine key={decision.id} decision={decision} />
          ))}
        </ol>
      ) : null}
    </>
  );
}
