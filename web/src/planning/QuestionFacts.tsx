import type { Question } from "../api/types";
import { addressOf, Link } from "../app/route";
import { Fact, Id, words } from "../parts/parts";
import { ScopeName } from "../ui/display";
import { waiting } from "./DismissQuestion";
import { judgeOf, Ladder } from "./Ladder";

// The statuses the questions view reads beside the open ones, in the order its filter offers them. A question a merge
// closed reads as merged, and not as dismissed.
export const closedStatuses = ["answered", "merged", "dismissed"] as const;

// statusOf is where a question stands, as a person reads it.
export function statusOf(question: Question): string {
  if (question.merged_into) {
    return "Merged";
  }
  return words(question.status, { needs_human: "Needs a person" });
}

// questionAt is the address of one question on the questions page, read with every status, so a link to a question
// that closed since still finds it.
export const questionAt = (id: string) => addressOf("/questions", { id, status: "all" });

// HeldBy names who holds a question that waits: an orchestrator by its name and the machine it runs on, a member by ID,
// or nobody.
function HeldBy({ question }: { question: Question }) {
  const holder = question.holder;
  if (!holder) {
    return <>Nobody holds it</>;
  }
  if (holder.orchestrator) {
    return (
      <>
        Orchestrator on {holder.machine} <Id value={holder.orchestrator} />
      </>
    );
  }
  return <Id value={holder.id} />;
}

// ClosedBy is the decision or assumption that answered a question: what it says, why, who recorded it, and a link to it.
function ClosedBy({ question }: { question: Question }) {
  const closed = question.closed_by;
  if (!closed) {
    return null;
  }
  return (
    <Fact name="Closed by">
      <p>
        {closed.statement} <span className="muted">· {words(closed.status)}</span>
      </p>
      {closed.rationale ? <p className="muted">{closed.rationale}</p> : null}
      <p className="small">
        {closed.decided_by ? `${judgeOf(closed.decided_by, question.machines)} · ` : ""}
        <Link className="mono" to={addressOf("/decisions", { id: closed.id })}>
          {closed.id}
        </Link>
      </p>
    </Fact>
  );
}

// QuestionFacts are what a member reads of a question wherever it shows, on Open questions and on the asking lane's and
// work order's pages (FR-QD-13): what its answer binds, who asked it, why it reached a person, who holds it, its batch,
// what closed it, and how it got here.
export function QuestionFacts({ question }: { question: Question }) {
  return (
    <>
      <dl className="kv">
        {question.scope ? (
          <Fact name="Scope">
            <ScopeName scope={question.scope} title={question.scope_title} />
          </Fact>
        ) : null}
        {question.asked_by ? (
          <Fact name="Asked by">
            <Id value={question.asked_by} />
          </Fact>
        ) : null}
        {question.escalation_reason ? <Fact name="Reached a person because">{words(question.escalation_reason)}</Fact> : null}
        {waiting(question) ? (
          <Fact name="Held by">
            <HeldBy question={question} />
          </Fact>
        ) : null}
        {question.batch ? (
          <Fact name="Batch">
            <Id value={question.batch.id} /> · {words(question.batch.status)}
          </Fact>
        ) : null}
        <ClosedBy question={question} />
        {question.merged_into ? (
          <Fact name="Merged into">
            <span className="result-links">
              <Link className="mono" to={questionAt(question.merged_into)}>
                {question.merged_into}
              </Link>
            </span>
          </Fact>
        ) : null}
      </dl>
      <Ladder steps={question.ladder_trace} attached={question.attached} machines={question.machines} />
    </>
  );
}
