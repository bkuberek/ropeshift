import { readAcceptance, readTimeline } from "../api/admin";
import type { WorkEstimate } from "../api/measures";
import { readEstimate } from "../api/measures";
import type { Segment, SegmentKind, Standing, WorkTimeline } from "../api/types";
import type { Live } from "../api/useView";
import { useView } from "../api/useView";
import { WorkEstimateView } from "../measures/Estimate";
import { words } from "../parts/parts";
import { SecurityNotes } from "../parts/Verdicts";
import { Badge, Absent, Actor, Banner, Digest, EmptyState, Fact, Facts, Panel, RecordChip, SkeletonRows, StateBadge, TableRegion, WorkBadge } from "../ui/display";
import { Checks } from "./Checks";
import type { Round } from "./evidence";
import { changeWords, leftToDo, roundsOf, segmentKinds, segmentWord, spanLabel, spanWords, standingWords, totalsOf, waitingStandingWords } from "./evidence";
import { verdictStates } from "./states";

// The Changes and Activity tabs of a work order show what `ropeshift work acceptance` and `ropeshift work timeline`
// print, from the same routes. The tabs read when they open, and again when the ledger says something changed.

// Branches lists what each attempt's branch changed, as its lane's worker measured it.
function Branches({ standing }: { standing: Standing }) {
  const branches = standing.artifacts.filter((artifact) => artifact.kind === "branch" || artifact.change);
  if (branches.length === 0) {
    return <p className="muted">The work has left no branch yet.</p>;
  }
  return (
    <ul className="criteria" aria-label="Branches">
      {branches.map((branch) => (
        <li key={branch.id}>
          <span className="mono">{branch.uri}</span>
          {branch.digest ? (
            <>
              {" "}
              at <Digest value={branch.digest} />
            </>
          ) : null}
          {" · "}
          {branch.change ? changeWords(branch.change) : <Absent>The change was not measured.</Absent>}
        </li>
      ))}
    </ul>
  );
}

// Criteria lists each criterion with where its evidence stands and the verdict a review gave it, which is bound
// to the commit the review read. A verdict is said in a word and a glyph, and every word of its reasons is a
// model's, so it goes in as text.
function Criteria({ standing }: { standing: Standing }) {
  const table = standing.state === "acceptance" ? waitingStandingWords : standingWords;
  const criteria = standing.assessment.criteria;
  if (criteria.length === 0 && !(standing.verdicts ?? []).length) {
    return <p className="muted">This work order has no acceptance criteria yet.</p>;
  }
  const verdicts = new Map((standing.verdicts ?? []).map((verdict) => [verdict.criterion, verdict]));
  return (
    <ul className="evidence-criteria" aria-label="Criteria">
      {criteria.map((criterion) => {
        const verdict = verdicts.get(criterion.criterion_id);
        return (
          <li key={criterion.criterion_id}>
            <p className="verdict-crit">
              <span className="mono">{criterion.criterion_id}</span> <span className="subtle">{words(criterion.standing, table)}</span>
            </p>
            {verdict ? (
              <>
                <p className="row-tight">
                  <StateBadge table={verdictStates} state={verdict.verdict} />
                  <span className="subtle small">
                    on commit <Digest value={verdict.commit} />
                  </span>
                </p>
                <p className="verdict-reason">{verdict.reasons}</p>
                {verdict.citations?.length ? (
                  <ul className="verdict-cites" aria-label={`Citations for ${verdict.criterion}`}>
                    {verdict.citations.map((citation) => (
                      <li key={citation}>
                        <RecordChip id={citation} />
                      </li>
                    ))}
                  </ul>
                ) : null}
              </>
            ) : (
              <p className="subtle small">No verdict yet.</p>
            )}
          </li>
        );
      })}
    </ul>
  );
}

// ChangesView is what the acceptance route returns: the diff summary, each criterion with its verdict, the checks,
// and what the evidence leaves to do.
export function ChangesView({ standing }: { standing: Standing }) {
  return (
    <div className="stack-8">
      <Panel title="Diff summary" level={3}>
        <Branches standing={standing} />
      </Panel>
      <Panel title="Criteria and verdicts" level={3}>
        <Criteria standing={standing} />
      </Panel>
      {standing.notes?.length ? (
        <Panel title="Security notes" level={3}>
          <SecurityNotes notes={standing.notes} />
        </Panel>
      ) : null}
      <Panel title="Checks" level={3}>
        <Checks standing={standing} />
      </Panel>
      <Panel title="What is left" level={3}>
        <ul className="criteria">
          {leftToDo(standing).map((line) => (
            <li key={line}>{line}</li>
          ))}
        </ul>
      </Panel>
    </div>
  );
}

// ChangesTab reads a work order's acceptance and shows it.
export function ChangesTab({ id, live }: { id: string; live: Live }) {
  const { view, failure } = useView<Standing>(() => readAcceptance(id), `work-order:${id}:acceptance`, live);
  if (!view) {
    return failure ? <Banner tone="danger">{failure}</Banner> : <SkeletonRows rows={4} />;
  }
  return (
    <>
      {failure ? <Banner tone="danger">{`This tab is not current. ${failure}`}</Banner> : null}
      <ChangesView standing={view} />
    </>
  );
}

const iso = (at: number) => new Date(at).toISOString();

// Time shows a moment in the browser's own time zone, with the exact one in the element for anything that reads it.
// It names the day only when the moment falls on another day than the timeline began.
function Time({ at, start }: { at: number; start: number }) {
  const clock = new Date(at).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" });
  const other = new Date(at).toDateString() !== new Date(start).toDateString();
  return <time dateTime={iso(at)}>{other ? `${new Date(at).toLocaleDateString(undefined, { month: "short", day: "numeric" })}, ${clock}` : clock}</time>;
}

// KindBadge says a segment's kind in its word, its glyph, and its tone.
function KindBadge({ segment }: { segment: Segment }) {
  const look = segmentKinds[segment.kind];
  return (
    <Badge tone={look?.tone ?? "neutral"} glyph={look?.glyph ?? "ring"}>
      {segmentWord(segment)}
    </Badge>
  );
}

// The place of a segment on the shared axis, as a share of the whole span.
function placeOf(segment: Segment, start: number, span: number): { left: string; width: string } {
  const share = (ms: number) => `${(Math.max(0, ms) / Math.max(1, span)) * 100}%`;
  return { left: share(segment.from - start), width: share(segment.to - segment.from) };
}

// Bars draws one row for each round, each segment to scale on one axis. The bar is an image with a sentence for a
// label, and the table under it is its accessible equivalent.
function Bars({ rounds, start, end }: { rounds: Round[]; start: number; end: number }) {
  const span = end - start;
  return (
    <ol className="timeline-rows" aria-label="Rounds">
      {rounds.map((round) => {
        const sentence = `${round.label}: ${round.segments.map((segment) => `${segmentWord(segment)} ${spanLabel(segment.to - segment.from)}`).join(", ")}.`;
        return (
          <li key={round.round} className="timeline-row">
            <span className="timeline-round">{round.label}</span>
            <div className="timeline-bar" role="img" aria-label={sentence}>
              {round.segments.map((segment, at) => (
                <span
                  key={at}
                  className={`seg fill-${segmentKinds[segment.kind]?.fill ?? "tint"}`}
                  data-tone={segmentKinds[segment.kind]?.tone ?? "neutral"}
                  data-kind={segment.kind}
                  style={placeOf(segment, start, span)}
                  title={`${segmentWord(segment)}, ${spanLabel(segment.to - segment.from)}`}
                />
              ))}
            </div>
          </li>
        );
      })}
    </ol>
  );
}

// Key names each kind the bar draws, with the fill that stands for it.
function Key({ kinds }: { kinds: SegmentKind[] }) {
  return (
    <ul className="timeline-key" aria-label="Key">
      {kinds.map((kind) => (
        <li key={kind}>
          <span className={`seg-swatch fill-${segmentKinds[kind]?.fill ?? "tint"}`} data-tone={segmentKinds[kind]?.tone ?? "neutral"} aria-hidden="true" />
          {segmentKinds[kind]?.word ?? kind}
        </li>
      ))}
    </ul>
  );
}

function Segments({ timeline }: { timeline: WorkTimeline["timeline"] }) {
  const last = timeline.segments.length - 1;
  return (
    <TableRegion label="Segments of the timeline">
      <table className="table stack-on-phone" aria-label="Segments">
        <thead>
          <tr>
            <th scope="col">Start</th>
            <th scope="col">End</th>
            <th scope="col" className="r">
              Length
            </th>
            <th scope="col">Kind</th>
            <th scope="col" className="r">
              Round
            </th>
            <th scope="col">Lane</th>
            <th scope="col">Ended by</th>
          </tr>
        </thead>
        <tbody>
          {timeline.segments.map((segment, at) => (
            <tr key={at} data-kind={segment.kind}>
              <td data-label="Start">
                <Time at={segment.from} start={timeline.start} />
              </td>
              <td data-label="End">{at === last && !timeline.terminal ? <Absent>under way</Absent> : <Time at={segment.to} start={timeline.start} />}</td>
              <td data-label="Length" className="r">
                <span aria-hidden="true">{spanLabel(segment.to - segment.from)}</span>
                <span className="visually-hidden">{spanWords(segment.to - segment.from)}</span>
              </td>
              <td data-label="Kind">
                <KindBadge segment={segment} />
              </td>
              <td data-label="Round" className="r">
                {segment.round}
              </td>
              <td data-label="Lane">{segment.lane ? <span className="mono">{segment.lane}</span> : <Absent>none</Absent>}</td>
              <td data-label="Ended by">{segment.ended ? <Actor kind={segment.ended.actor.Kind} id={segment.ended.actor.ID} /> : <Absent>not yet</Absent>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </TableRegion>
  );
}

// cost says an estimated cost as the command line does: at list price, and at least what it shows when some of it
// was never reported.
const costWords = (usd: number, atLeast: boolean | undefined) => `${atLeast ? "at least " : ""}$${usd.toFixed(2)}, estimated, at list price`;

function Record({ read }: { read: NonNullable<WorkTimeline["record"]> }) {
  const { usage, change } = read;
  return (
    <Panel title="Delivery record" level={3}>
      <Facts label="Delivery record">
        <Fact name="Outcome">
          <WorkBadge state={read.outcome} />
        </Fact>
        <Fact name="Rounds">{read.rounds}</Fact>
        <Fact name="Delivery">{spanLabel(read.delivery_ms)}</Fact>
        <Fact name="Lane">{spanLabel(read.lane_ms)}</Fact>
        <Fact name="Waiting for a person">{spanLabel(read.person_ms)}</Fact>
        <Fact name="Cost">{costWords(usage.cost_usd, usage.cost_lower_bound)}</Fact>
        <Fact name="Tokens">
          {usage.input_tokens.toLocaleString("en-US")} in, {usage.output_tokens.toLocaleString("en-US")} out
        </Fact>
        <Fact name="Change">{change ? changeWords(change) : <Absent>not measured</Absent>}</Fact>
      </Facts>
      {read.person_waits.length ? (
        <>
          <h4 className="h4">Waits for a person</h4>
          <ul className="criteria" aria-label="Waits for a person">
            {read.person_waits.map((wait, at) => (
              <li key={at}>{spanLabel(wait)}</li>
            ))}
          </ul>
        </>
      ) : null}
    </Panel>
  );
}

// ActivityView is where a work order's time went: the totals, with a wait for a person apart from agent time, the
// segments to scale and in a table, and the delivery record once a change or a rework has ended.
export function ActivityView({ read, estimate }: { read: WorkTimeline; estimate?: WorkEstimate | null }) {
  const { timeline, record } = read;
  // The estimate band and the baseline beside it, in full, with the actual delivery time once the work has one.
  const estimated = estimate ? (
    <Panel title="Estimate" level={3}>
      <WorkEstimateView read={estimate} actualMs={record?.delivery_ms} />
      {record ? null : <p className="small muted">Actual: none yet.</p>}
    </Panel>
  ) : null;
  if (timeline.segments.length === 0) {
    return (
      <div className="stack-8">
        <EmptyState icon="history" title="No timeline yet">
          A timeline starts when the work order first becomes ready.
        </EmptyState>
        {estimated}
      </div>
    );
  }
  const totals = totalsOf(timeline);
  const kinds = totals.kinds.map(([kind]) => kind);
  return (
    <div className="stack-8">
      <Panel title="Where the time went" level={3}>
        <Facts label="Totals">
          <Fact name="Elapsed">{spanLabel(totals.elapsed)}</Fact>
          <Fact name="Agent time">{spanLabel(totals.agent)}</Fact>
          <Fact name="Waiting for a person">{spanLabel(totals.person)}</Fact>
          <Fact name="Other waits">{spanLabel(totals.other)}</Fact>
        </Facts>
        {timeline.terminal ? null : <p className="muted">Under way. The last segment runs to the time this was read.</p>}
      </Panel>
      <Panel title="Timeline" level={3}>
        <Bars rounds={roundsOf(timeline.segments)} start={timeline.start} end={timeline.end} />
        <Key kinds={kinds} />
        <Segments timeline={timeline} />
      </Panel>
      {record ? <Record read={record} /> : timeline.terminal ? <p className="muted">Only a change or a rework leaves a delivery record.</p> : null}
      {estimated}
    </div>
  );
}

// ActivityTab reads a work order's timeline and its estimate, and shows them. A failed estimate read leaves the
// timeline standing and says so beside it.
export function ActivityTab({ id, live }: { id: string; live: Live }) {
  const { view, failure } = useView<WorkTimeline>(() => readTimeline(id), `work-order:${id}:timeline`, live);
  const estimate = useView<WorkEstimate>(() => readEstimate(id), `work-order:${id}:estimate`, live);
  if (!view) {
    return failure ? <Banner tone="danger">{failure}</Banner> : <SkeletonRows rows={4} />;
  }
  return (
    <>
      {failure ? <Banner tone="danger">{`This tab is not current. ${failure}`}</Banner> : null}
      {estimate.failure ? <Banner tone="danger">{`The estimate is not current. ${estimate.failure}`}</Banner> : null}
      <ActivityView read={view} estimate={estimate.view} />
    </>
  );
}
