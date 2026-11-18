import type { CIWatch, PullRequest } from "../api/types";
import { span } from "../measures/format";
import { Digest, Fact, Facts, Panel, StateBadge } from "../ui/display";
import { pullRequestStates } from "./states";

// ciTimes says where the head's CI stands, as the work order's page shows it: the time CI is expected to take, or "no
// history", and how long the checks have run or took, and how long a run waited for a runner.
function ciTimes(ci: CIWatch): { expected: string; elapsed: string; queue: string } {
  const expected = ci.history ? `p50 ${span(ci.expected_p50_ms ?? 0)} · p85 ${span(ci.expected_ms ?? 0)}` : "no history";
  const elapsed = ci.final ? `took ${span(ci.elapsed_ms)}` : `${span(ci.elapsed_ms)} so far`;
  const queue = ci.queued ? `${span(ci.queue_ms)} so far, no runner has picked it up` : span(ci.queue_ms);
  return { expected, elapsed, queue };
}

// PullRequestPanel names the pull request a work order lands through, as its worker last read it from the forge:
// its state, its address, the branch it merges into its base, its head commit, and the commit a merge made. The
// address is the forge's word, so it shows as text and is never a link. Beside them, where the head's required checks
// stand against the repository's CI: the time CI is expected to take, or no history, and the time elapsed and queued.
export function PullRequestPanel({ pr, ci }: { pr: PullRequest; ci?: CIWatch }) {
  const times = ci?.watching ? ciTimes(ci) : null;
  return (
    <Panel title={`Pull request #${pr.number}`} level={3}>
      <Facts>
        <Fact name="State">
          <StateBadge table={pullRequestStates} state={pr.state} />
        </Fact>
        <Fact name="Address" mono>
          {pr.url}
        </Fact>
        <Fact name="Branch" mono>
          {`${pr.branch} into ${pr.base}`}
        </Fact>
        <Fact name="Head commit" mono>
          <Digest value={pr.head_commit} />
        </Fact>
        {pr.merge_commit ? (
          <Fact name="Merge commit" mono>
            <Digest value={pr.merge_commit} />
          </Fact>
        ) : null}
        {times ? (
          <>
            <Fact name="Expected CI time">{times.expected}</Fact>
            <Fact name="CI elapsed">{times.elapsed}</Fact>
            <Fact name="Queue time">{times.queue}</Fact>
          </>
        ) : null}
      </Facts>
    </Panel>
  );
}
