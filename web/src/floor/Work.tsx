import type { Completed as CompletedView, CompletedWork, HeldLane, InProgress as InProgressView, OnHold as OnHoldView, Stuck as StuckView, WorkLine, WorkOrder } from "../api/types";
import { Link } from "../app/route";
import { workLabels, WorkCard } from "../needs/WorkCard";
import { Fact, Id, Lasted, Line, Until, words } from "../parts/parts";
import { LaneFacts } from "./LaneFacts";
import { workLabel } from "./states";
import { WakeWords } from "./WakeWords";

const titleOf = (order: WorkOrder) => order.title || order.id;

function WorkTags({ order }: { order: WorkOrder }) {
  return (
    <ul className="tags">
      <li>
        <Id value={order.id} />
      </li>
      {order.lease_worker ? <li>Held by {order.lease_worker}</li> : null}
      {order.max_attempts ? (
        <li>
          Attempt {Math.min((order.attempts ?? 0) + 1, order.max_attempts)} of {order.max_attempts}
        </li>
      ) : null}
    </ul>
  );
}

function RunningLine({ line }: { line: WorkLine }) {
  return (
    <Line side={<Lasted at={line.since} />} kind={workLabel(line.work_order.state)} title={titleOf(line.work_order)} plain>
      <div className="card-body">
        <WorkTags order={line.work_order} />
        {line.lane ? <LaneFacts lane={line.lane} /> : <p className="quiet">No lane yet.</p>}
      </div>
    </Line>
  );
}

export function InProgress({ view }: { view: InProgressView }) {
  if (view.items.length === 0) {
    return <p className="empty">Nothing is in progress.</p>;
  }
  return (
    <ol className="queue queue-work" role="list">
      {view.items.map((line) => (
        <RunningLine key={line.work_order.id} line={line} />
      ))}
    </ol>
  );
}

function HeldLine({ line }: { line: HeldLane }) {
  return (
    <Line side={<Lasted at={line.since} />} kind={workLabel(line.work_order.state)} title={titleOf(line.work_order)} plain>
      <div className="card-body">
        <WorkTags order={line.work_order} />
        {line.wakes.length ? (
          <dl className="facts">
            {line.wakes.map((wake) => (
              <Fact key={wake.id} name="Wakes">
                <WakeWords expression={wake.expression} />
                {wake.next_eval_at ? (
                  <span className="fresh">
                    <Until at={wake.next_eval_at} />
                  </span>
                ) : null}
              </Fact>
            ))}
          </dl>
        ) : (
          <p className="quiet">No wake is recorded.</p>
        )}
        {line.lane ? <LaneFacts lane={line.lane} /> : null}
      </div>
    </Line>
  );
}

export function OnHold({ view }: { view: OnHoldView }) {
  if (view.items.length === 0) {
    return <p className="empty">Nothing is on hold.</p>;
  }
  return (
    <ol className="queue queue-work queue-slack" role="list">
      {view.items.map((line) => (
        <HeldLine key={line.work_order.id} line={line} />
      ))}
    </ol>
  );
}

// A rung stands on a line a person can do something about, as it does in the queue.
export function Stuck({ view, after }: { view: StuckView; after: () => Promise<void> }) {
  if (view.items.length === 0) {
    return <p className="empty">Nothing is stuck.</p>;
  }
  return (
    <ol className="queue" role="list">
      {view.items.map((item) => (
        <Line
          key={`${item.reason}:${item.id}`}
          side={<Lasted at={item.since} />}
          kind={workLabels[item.reason] ?? words(item.reason)}
          title={item.work_order?.title || item.lane?.id || item.id}
          plain={item.actions.length === 0}
        >
          <p className="quiet card-id">
            <Id value={item.id} />
          </p>
          <WorkCard id={item.id} work={item} after={after} />
        </Line>
      ))}
    </ol>
  );
}

const standingWords: Record<string, string> = {
  covered: "Covered by a check",
  failed: "A check failed",
  second_principal: "Only the lane said so",
  member: "No check covered it",
};

// Work that waits for acceptance still wants something, and work that ended wants nothing of anyone.
const wantedWords: Record<string, string> = {
  ...standingWords,
  second_principal: "Needs a second principal",
  member: "Needs a member",
};

function Ended({ line }: { line: CompletedWork }) {
  const { standing, work_order: order } = line;
  return (
    <Line side={<Lasted at={line.since} ended />} kind={workLabel(order.state)} title={titleOf(order)} plain>
      <div className="card-body">
        <WorkTags order={order} />
        {line.reason ? (
          <dl className="facts">
            <Fact name="Lane said" mono>
              {line.reason}
            </Fact>
          </dl>
        ) : null}
        {standing.assessment.criteria.length ? (
          <ul className="criteria" aria-label="Acceptance criteria">
            {standing.assessment.criteria.map((criterion) => (
              <li key={criterion.criterion_id}>
                <span className="mono">{criterion.criterion_id}</span> {words(criterion.standing, order.state === "acceptance" ? wantedWords : standingWords)}
              </li>
            ))}
          </ul>
        ) : null}
        {standing.checks.length ? (
          <ul className="criteria" aria-label="Checks">
            {standing.checks.map((check) => (
              <li key={check.id}>
                <span className="mono">{check.name}</span> {words(check.status)}
                <span className="fresh">{words(check.source)}</span>
              </li>
            ))}
          </ul>
        ) : null}
        {standing.artifacts.length ? (
          <ul className="criteria" aria-label="Artifacts">
            {standing.artifacts.map((artifact) => (
              <li key={artifact.id}>
                {words(artifact.kind)} <span className="mono">{artifact.uri}</span>
              </li>
            ))}
          </ul>
        ) : null}
        {order.state === "acceptance" ? (
          <p>
            <Link to="/">Decide in Needs you</Link>
          </p>
        ) : null}
      </div>
    </Line>
  );
}

export function Completed({ view }: { view: CompletedView }) {
  if (view.items.length === 0) {
    return <p className="empty">Nothing has ended yet.</p>;
  }
  return (
    <ol className="queue queue-work" role="list">
      {view.items.map((line) => (
        <Ended key={line.work_order.id} line={line} />
      ))}
    </ol>
  );
}
