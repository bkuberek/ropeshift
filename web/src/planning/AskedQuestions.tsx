import { readView } from "../api/client";
import type { Questions } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { Link } from "../app/route";
import { Banner, Panel, SkeletonRows } from "../ui/display";
import { questionAt, QuestionFacts, statusOf } from "./QuestionFacts";

// AskedQuestions is the questions of one lane, or of one work order, with every status, so each is visible from the
// asking lane's page and its work order's page from the moment the lane asks it (FR-QD-13). Each shows the fields Open
// questions shows and links to the question there, where a member answers, assumes, or merges it.
export function AskedQuestions({ live, lane = "", workOrder = "" }: { live: Live; lane?: string; workOrder?: string }) {
  const filter = { status: "all", lane, work_order: workOrder };
  const read = useView<Questions>(() => readView<Questions>("questions", "", filter), `questions:${lane}:${workOrder}`, live);
  const empty = lane ? "This lane has asked no question." : "No question is about this work or was asked by its lanes.";
  return (
    <Panel title="Questions">
      {read.failure ? <Banner tone="danger">{read.view ? `What this shows is not current. ${read.failure}` : read.failure}</Banner> : null}
      {read.view ? (
        read.view.questions.length ? (
          <ol className="list-rows" role="list">
            {read.view.questions.map((question) => (
              <li key={question.id} className="list-row stack-8">
                <p>
                  <Link to={questionAt(question.id)}>{question.text}</Link>
                </p>
                <p className="muted small">
                  {statusOf(question)} · Rung {question.rung}
                  {question.blocking ? " · Blocking" : ""}
                </p>
                <QuestionFacts question={question} />
              </li>
            ))}
          </ol>
        ) : (
          <p className="muted">{empty}</p>
        )
      ) : read.failure ? null : (
        <SkeletonRows rows={2} />
      )}
    </Panel>
  );
}
