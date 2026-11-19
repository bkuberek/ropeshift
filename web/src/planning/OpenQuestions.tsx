import type { Question, Questions as View } from "../api/types";
import { addressOf, Link } from "../app/route";
import { Line, words } from "../parts/parts";
import { NoOrchestrator } from "../needs/NoOrchestrator";
import { CategoryFilter } from "./CategoryFilter";
import { waiting } from "./DismissQuestion";
import { QuestionActions } from "./QuestionActions";
import { closedStatuses, QuestionFacts, statusOf, statusPhrase } from "./QuestionFacts";

function QuestionLine({ question, others }: { question: Question; others: Question[] }) {
  return (
    <Line side={<span className="wait-word">{statusOf(question)}</span>} kind={`Rung ${question.rung}`} title={question.text} plain>
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
        <QuestionFacts question={question} />
        <QuestionActions question={question} others={others} />
      </div>
    </Line>
  );
}

// StatusFilter narrows the questions to the open ones, the ones closed one way, or every one, a link each so the
// address keeps the filter, with the category in force kept.
function StatusFilter({ status, category }: { status: string; category: string }) {
  const options = [{ value: "", name: "Open" }, ...closedStatuses.map((value) => ({ value, name: words(value) })), { value: "all", name: "All" }];
  return (
    <nav className="filters" aria-label="Filter by status">
      {options.map((option) => (
        <Link key={option.value} to={addressOf("/questions", { status: option.value, category })} stay aria-current={option.value === status ? "true" : undefined}>
          {option.name}
        </Link>
      ))}
    </nav>
  );
}

// emptyWords says what a list that holds nothing would have held.
function emptyWords(status: string, category: string, selected: string): string {
  if (selected) {
    return status ? "No question has that ID." : "That question is not open.";
  }
  if (status === "all") {
    return category ? `No question in ${words(category)}.` : "No question is recorded.";
  }
  if (status) {
    return category ? `No question in ${words(category)} is ${statusPhrase(status)}.` : `No question is ${statusPhrase(status)}.`;
  }
  return category ? `Nothing open in ${words(category)}.` : "Nothing is open.";
}

// OpenQuestions lists the questions still open, at every rung, or with a status those closed each way or every one, so a
// member sees the whole ladder (FR-QD-13).
export function OpenQuestions({ view, category, selected, status = "" }: { view: View; category: string; selected: string; status?: string }) {
  const shown = selected ? view.questions.filter((question) => question.id === selected) : view.questions;
  return (
    <>
      {selected ? (
        <p className="back">
          <Link to={addressOf("/questions", { status })}>{status ? "All questions" : "All open questions"}</Link>
        </p>
      ) : (
        <>
          <StatusFilter status={status} category={category} />
          <CategoryFilter path="/questions" categories={view.categories} category={category} keep={{ status }} />
        </>
      )}
      {shown.some(waiting) ? <NoOrchestrator what="the questions that wait" /> : null}
      {shown.length ? (
        <ol className="record-list queue-work" role="list">
          {shown.map((question) => (
            <QuestionLine key={question.id} question={question} others={view.questions} />
          ))}
        </ol>
      ) : (
        <p className="empty">{emptyWords(status, category, selected)}</p>
      )}
    </>
  );
}
