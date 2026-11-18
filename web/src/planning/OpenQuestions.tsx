import type { Question, Questions as View } from "../api/types";
import { Link } from "../app/route";
import { Fact, Id, Line, words } from "../parts/parts";
import { ScopeName } from "../ui/display";
import { NoOrchestrator } from "../needs/NoOrchestrator";
import { CategoryFilter } from "./CategoryFilter";
import { Ladder } from "./Ladder";
import { waiting } from "./DismissQuestion";
import { QuestionActions } from "./QuestionActions";

const statusWords: Record<string, string> = { needs_human: "Needs a person" };

function QuestionLine({ question, others }: { question: Question; others: Question[] }) {
  return (
    <Line side={<span className="wait-word">{words(question.status, statusWords)}</span>} kind={`Rung ${question.rung}`} title={question.text} plain>
      <div className="panel-body">
        <ul className="chips">
          {question.blocking ? <li className="chip-strong">Blocking</li> : null}
          {question.categories?.map((category) => (
            <li key={category}>{words(category)}</li>
          ))}
        </ul>
        {question.options?.length ? (
          <ul className="criteria" aria-label="Options">
            {question.options.map((option) => (
              <li key={option}>
                {option}
                {option === question.recommendation ? <span className="choice-note"> Recommended</span> : null}
              </li>
            ))}
          </ul>
        ) : null}
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
          {question.claimed_by ? (
            <Fact name="Held by">
              <Id value={question.claimed_by} />
            </Fact>
          ) : null}
        </dl>
        <Ladder steps={question.ladder_trace} />
        <QuestionActions question={question} others={others} />
      </div>
    </Line>
  );
}

export function OpenQuestions({ view, category, selected }: { view: View; category: string; selected: string }) {
  const shown = selected ? view.questions.filter((question) => question.id === selected) : view.questions;
  return (
    <>
      {selected ? (
        <p className="back">
          <Link to="/questions">All open questions</Link>
        </p>
      ) : (
        <CategoryFilter path="/questions" categories={view.categories} category={category} />
      )}
      {shown.some(waiting) ? <NoOrchestrator what="the questions that wait" /> : null}
      {shown.length ? (
        <ol className="record-list queue-work" role="list">
          {shown.map((question) => (
            <QuestionLine key={question.id} question={question} others={view.questions} />
          ))}
        </ol>
      ) : (
        <p className="empty">{selected ? "That question is not open." : category ? `Nothing open in ${words(category)}.` : "Nothing is open."}</p>
      )}
    </>
  );
}
