import type { PullRequest } from "../api/types";
import { Digest, Fact, Facts, Panel, StateBadge } from "../ui/display";
import { pullRequestStates } from "./states";

// PullRequestPanel names the pull request a work order lands through, as its worker last read it from the forge:
// its state, its address, the branch it merges into its base, its head commit, and the commit a merge made. The
// address is the forge's word, so it shows as text and is never a link.
export function PullRequestPanel({ pr }: { pr: PullRequest }) {
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
      </Facts>
    </Panel>
  );
}
