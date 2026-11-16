import { Link } from "../app/route";
import { workAddress } from "../floor/WorkOrders";
import { Lasted } from "../parts/parts";
import { Absent, Digest, EmptyState, TableRegion } from "../ui/display";
import type { PullRequestRow } from "./inReview";

// PullRequests lists the open pull requests ropeshift opened for work in review: where each is, the head commit,
// whether a review read that commit, its checks, and whether the forge says it merges cleanly. A pull request's
// address is shown as text, because nothing in a record's text becomes a link.
export function PullRequests({ rows }: { rows: PullRequestRow[] }) {
  if (rows.length === 0) {
    return (
      <div className="panel">
        <EmptyState icon="pr" title="No pull request is open.">
          A pull request shows here while the work it carries waits for acceptance or a merge, in a repository whose changes land through pull requests.
        </EmptyState>
      </div>
    );
  }
  return (
    <div className="panel">
      <TableRegion label="Open pull requests">
        <table className="table stack-on-phone">
          <thead>
            <tr>
              <th scope="col">Pull request</th>
              <th scope="col">Work order</th>
              <th scope="col">Head commit</th>
              <th scope="col">Review</th>
              <th scope="col">Checks</th>
              <th scope="col">Mergeable</th>
              <th scope="col" className="r">
                In review
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map(({ id, title, since, pr, reviewed, checks, mergeable }) => (
              <tr key={id} data-work={id}>
                <td data-label="Pull request" className="title-cell">
                  <span className="id-link">#{pr.number}</span>
                  <span className="cell-note mono">{pr.url}</span>
                  <span className="cell-note mono">
                    {pr.branch} into {pr.base}
                  </span>
                </td>
                <td data-label="Work order" className="title-cell">
                  <Link to={workAddress(id, "checks")} data-item>
                    {id}
                  </Link>
                  <span className="cell-note">{title}</span>
                </td>
                <td data-label="Head commit">
                  <Digest value={pr.head_commit} />
                </td>
                <td data-label="Review">{reviewed ? "Read by the review" : "Not read by a review"}</td>
                <td data-label="Checks">{checks}</td>
                <td data-label="Mergeable">{mergeable}</td>
                <td data-label="In review" className="r">
                  {since ? <Lasted at={since} /> : <Absent>not read</Absent>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableRegion>
    </div>
  );
}

