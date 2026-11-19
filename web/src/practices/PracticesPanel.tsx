import type { PracticesList } from "../api/practices";
import { Ago, ViewTime } from "../parts/parts";
import { addressOf, Link } from "../app/route";
import { Button } from "../ui/controls";
import { Absent, Banner, Panel, Refusal, SkeletonRows } from "../ui/display";
import { plainText } from "../admin/untrustedText";
import { CheckLine, OpenCounts } from "./parts";
import { countsOf, shortCommit } from "./logic";
import type { Check } from "./useCheck";

// PracticesPanel is the Overview's side panel of the practices check: one row for each repository with its open findings
// by severity, what was acknowledged and what the check could not read, the commit the result read and its age, and a link
// to the repository's Practices tab. Run check asks for a check of every repository (FR-PRAC-6).
export function PracticesPanel({ list, failure, readAt, check, archived, now }: { list: PracticesList | null; failure: string; readAt: number; check: Check; archived: boolean; now: number }) {
  const stay = archived ? "This project is archived and read-only" : undefined;
  return (
    <Panel
      title="Practices"
      actions={
        <Button small loading={check.busy} disabled={archived || list === null} title={stay} onClick={() => void check.run([])}>
          Run check
        </Button>
      }
    >
      <ViewTime.Provider value={{ asOf: readAt, elapsed: Math.max(0, now - readAt) }}>
        {list === null ? (
          failure ? <Banner tone="danger">{failure}</Banner> : <SkeletonRows rows={3} />
        ) : list.repositories.length === 0 ? (
          <p className="muted">No repository is registered.</p>
        ) : (
          <ul className="list-rows">
            {list.repositories.map((view) => {
              const counts = countsOf(view);
              return (
                <li key={view.repository} className="list-row">
                  <p className="row-tight">
                    <Link to={addressOf("/repositories", { repo: view.repository, tab: "practices" })} className="list-row-title">
                      {plainText(view.name)}
                    </Link>
                  </p>
                  {view.result ? (
                    <p className="small row-tight">
                      <OpenCounts view={view} />
                      {counts.acknowledged > 0 ? <span className="muted">{counts.acknowledged} acknowledged</span> : null}
                      {counts.unknown > 0 ? <span className="muted">{counts.unknown} unknown</span> : null}
                    </p>
                  ) : (
                    <p className="small muted">
                      <Absent>Not checked yet</Absent>
                    </p>
                  )}
                  <p className="small muted row-tight">
                    {view.result ? (
                      <>
                        <span className="mono" title={view.result.commit}>
                          {shortCommit(view.result.commit)}
                        </span>
                        <Ago at={Date.parse(view.result.checked_at)} />
                      </>
                    ) : null}
                    <CheckLine view={view} check={check} />
                  </p>
                </li>
              );
            })}
          </ul>
        )}
        {list && failure ? <Banner tone="danger">{`What this panel shows is not current. ${failure}`}</Banner> : null}
        {check.failure ? <Refusal>{check.failure}</Refusal> : null}
      </ViewTime.Provider>
    </Panel>
  );
}
