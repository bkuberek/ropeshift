import type { Question, Questions as View } from "../api/types";
import { addressOf, Link } from "../app/route";
import { Fact, Id, Line, words } from "../parts/parts";

const statusWords: Record<string, string> = { needs_human: "Needs a person" };

function Ladder({ question }: { question: Question }) {
  if (!question.ladder_trace?.length) {
    return null;
  }
  return (
    <details className="older">
      <summary>How it got here</summary>
      <ol className="rows" role="list">
        {question.ladder_trace.map((step) => (
          <li key={step.rung} className="row">
            <p>Rung {step.rung}</p>
            {step.outcome ? <p className="quiet">{step.outcome}</p> : null}
            {step.candidates?.length ? <p className="mono quiet">{step.candidates.join(", ")}</p> : null}
          </li>
        ))}
      </ol>
    </details>
  );
}

function QuestionLine({ question }: { question: Question }) {
  return (
    <Line side={<span className="wait-word">{words(question.status, statusWords)}</span>} kind={`Rung ${question.rung}`} title={question.text} plain>
      <div className="card-body">
        <ul className="tags">
          {question.blocking ? <li className="tag-strong">Blocking</li> : null}
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
        <dl className="facts">
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
        <Ladder question={question} />
        {question.status === "needs_human" ? (
          <p>
            <Link to="/">Answer in Needs you</Link>
          </p>
        ) : null}
      </div>
    </Line>
  );
}

function Filters({ categories, category }: { categories: string[]; category: string }) {
  return (
    <nav className="filters" aria-label="Filter by category">
      <Link to="/questions" stay aria-current={category === "" ? "true" : undefined}>
        All
      </Link>
      {categories.map((name) => (
        <Link key={name} to={addressOf("/questions", { category: name })} stay aria-current={name === category ? "true" : undefined}>
          {words(name)}
        </Link>
      ))}
    </nav>
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
        <Filters categories={view.categories} category={category} />
      )}
      {shown.length ? (
        <ol className="queue queue-work" role="list">
          {shown.map((question) => (
            <QuestionLine key={question.id} question={question} />
          ))}
        </ol>
      ) : (
        <p className="empty">{selected ? "That question is not open." : category ? `Nothing open in ${words(category)}.` : "Nothing is open."}</p>
      )}
    </>
  );
}
