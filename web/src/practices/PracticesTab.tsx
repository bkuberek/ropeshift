import { useEffect, useId, useRef, useState } from "react";

import type { PracticeFinding, PracticeRow, PracticesAcknowledgement, PracticesView } from "../api/practices";
import { plainText } from "../admin/untrustedText";
import { addressOf, Link } from "../app/route";
import { Ago, ViewTime } from "../parts/parts";
import { Button } from "../ui/controls";
import { Absent, Badge, Fact, Facts, LookBadge, Refusal, TableRegion } from "../ui/display";
import { useToast } from "../ui/overlays";
import { AcknowledgeDialog, WithdrawDialog } from "./FindingDialogs";
import { CheckLine } from "./parts";
import { checking, orphansOf, severityLook, shortCommit, standingOf } from "./logic";
import type { Check } from "./useCheck";

// useCopy puts text on the clipboard, and says when the browser did not let the page.
function useCopy() {
  const toast = useToast();
  return (text: string) =>
    void navigator.clipboard?.writeText(text).then(
      () => toast("success", `Copied ${text}.`),
      () => toast("warning", "The browser did not let this page copy. Select the text and copy it."),
    );
}

function Fix({ row }: { row: PracticeRow }) {
  const copy = useCopy();
  return (
    <>
      <p>{row.fix}</p>
      {row.template ? (
        <p className="small row-tight">
          <Link to={addressOf("/templates", { name: row.template })} installation>
            {row.template}
          </Link>
          {row.command ? (
            <>
              <code className="mono">{row.command}</code>
              <Button small icon="copy" aria-label={`Copy ${row.command}`} onClick={() => copy(row.command ?? "")}>
                Copy
              </Button>
            </>
          ) : null}
        </p>
      ) : null}
    </>
  );
}

// PracticeTable lists every practice in table order with its status, why it matters, and how to meet it (FR-PRAC-2).
function PracticeTable({ rows, findings }: { rows: PracticeRow[]; findings: PracticeFinding[] }) {
  return (
    <TableRegion label="Practices">
      <table className="table stack-on-phone" aria-label="Practices and their statuses">
        <thead>
          <tr>
            <th scope="col">Practice</th>
            <th scope="col">Status</th>
            <th scope="col">Why it matters</th>
            <th scope="col">Fix</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => {
            const standing = standingOf(row, findings);
            return (
              <tr key={row.id}>
                <td data-label="Practice">
                  <code className="mono">{row.id}</code>
                </td>
                <td data-label="Status">
                  <span className="row-tight">
                    <LookBadge look={standing.look} />
                    {standing.muted ? <span className="small muted">{standing.muted}</span> : null}
                  </span>
                  {row.reason ? <span className="cell-note">{plainText(row.reason)}</span> : null}
                </td>
                <td data-label="Why it matters" className="wrap">
                  {row.why}
                </td>
                <td data-label="Fix" className="wrap">
                  <Fix row={row} />
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </TableRegion>
  );
}

// Where a secret was found: its rule, file, line, and commit, and never the text it matched.
function Where({ finding }: { finding: PracticeFinding }) {
  if (!finding.rule) {
    return null;
  }
  return (
    <span className="small muted row-tight">
      <span>rule</span>
      <span className="mono">{plainText(finding.rule)}</span>
      <span>file</span>
      <span className="mono">{plainText(finding.path ?? "")}</span>
      <span>line</span>
      <span className="mono">{finding.line ?? 0}</span>
      <span>commit</span>
      <span className="mono">{plainText(finding.commit ?? "")}</span>
    </span>
  );
}

// Held is an acknowledgement as a row shows it: the reason, the member who gave it, and when.
function Held({ held }: { held: PracticesAcknowledgement }) {
  return (
    <>
      <p>{plainText(held.reason)}</p>
      <p className="small muted">
        {plainText(held.member)} · <Ago at={Date.parse(held.acknowledged_at)} />
      </p>
    </>
  );
}

// OrphanTable lists the acknowledgements of findings the latest result does not list, which a member may still withdraw.
function OrphanTable({ held, archived, onWithdraw }: { held: PracticesAcknowledgement[]; archived: boolean; onWithdraw: (id: string) => void }) {
  return (
    <>
      <p className="small muted">These acknowledgements name findings the latest result does not list. They stay until you withdraw them.</p>
      <TableRegion label="Acknowledgements">
        <table className="table stack-on-phone" aria-label="Acknowledgements of findings not in the latest result">
          <thead>
            <tr>
              <th scope="col">Finding</th>
              <th scope="col">Acknowledgement</th>
            </tr>
          </thead>
          <tbody>
            {held.map((one) => (
              <tr key={one.finding}>
                <td data-label="Finding" className="wrap">
                  <code className="mono">{plainText(one.finding)}</code>
                </td>
                <td data-label="Acknowledgement" className="wrap">
                  <Held held={one} />
                  <Button small disabled={archived} onClick={() => onWithdraw(one.finding)}>
                    Withdraw
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableRegion>
    </>
  );
}

function FindingTable({ findings, archived, onAcknowledge, onWithdraw }: { findings: PracticeFinding[]; archived: boolean; onAcknowledge: (id: string) => void; onWithdraw: (id: string) => void }) {
  return (
    <TableRegion label="Findings">
      <table className="table stack-on-phone" aria-label="Findings and their acknowledgements">
        <thead>
          <tr>
            <th scope="col">Finding</th>
            <th scope="col">State</th>
            <th scope="col">Acknowledgement</th>
          </tr>
        </thead>
        <tbody>
          {findings.map((finding) => (
            <tr key={finding.id}>
              <td data-label="Finding" className="wrap">
                <code className="mono">{plainText(finding.id)}</code>
                <span className="cell-note">{plainText(finding.detail)}</span>
                <Where finding={finding} />
              </td>
              <td data-label="State">
                {finding.acknowledged ? (
                  <span className="row-tight">
                    <Badge tone="neutral" glyph="check">
                      Acknowledged
                    </Badge>
                    <span className="small muted">{finding.severity}</span>
                  </span>
                ) : (
                  <LookBadge look={severityLook[finding.severity]} />
                )}
              </td>
              <td data-label="Acknowledgement" className="wrap">
                {finding.acknowledgement ? <Held held={finding.acknowledgement} /> : null}
                {/* One button for both, so the focus a dialog returns to stays on it when the finding changes hands. */}
                <Button small disabled={archived} onClick={() => (finding.acknowledgement ? onWithdraw(finding.id) : onAcknowledge(finding.id))}>
                  {finding.acknowledgement ? "Withdraw" : "Acknowledge"}
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </TableRegion>
  );
}

// PracticesTab is the Practices tab of a repository's record: the result's commit, base branch, worker, and age, Run check,
// every practice with its status, and the findings, each of which a member may acknowledge with a reason (FR-PRAC-6,
// FR-PRAC-10). An acknowledged finding stays in the list with its reason, member, and time.
export function PracticesTab({
  repository,
  name,
  views,
  check,
  archived,
  after,
  readAt,
  now,
}: {
  repository: string;
  name: string;
  views: PracticesView[];
  check: Check;
  archived: boolean;
  after: () => void;
  readAt: number;
  now: number;
}) {
  const heading = useId();
  const title = useRef<HTMLHeadingElement>(null);
  const settling = useRef(false);
  const view = views.find((row) => row.repository === repository);
  // A dialog returns focus to the button that opened it. When the list that is read next no longer holds that button, the
  // focus falls to the page, so it goes to the tab's heading instead.
  useEffect(() => {
    if (settling.current) {
      settling.current = false;
      if (document.activeElement === document.body || document.activeElement === null) {
        title.current?.focus();
      }
    }
  }, [views]);
  const changed = () => {
    settling.current = true;
    after();
  };
  const orphans = view ? orphansOf(view) : [];
  const [dialog, setDialog] = useState<{ kind: "acknowledge" | "withdraw"; finding: string } | null>(null);
  const result = view?.result;
  const busy = view !== undefined && checking(view.request, check.ended);
  return (
    <section className="panel" aria-labelledby={heading}>
      <div className="panel-head">
        <h2 id={heading} ref={title} tabIndex={-1}>Practices of {plainText(name)}</h2>
        <Button small variant="primary" loading={check.busy} disabled={archived} onClick={() => void check.run([repository])}>
          Run check
        </Button>
      </div>
      <div className="panel-body stack">
        <ViewTime.Provider value={{ asOf: readAt, elapsed: Math.max(0, now - readAt) }}>
          {view ? <CheckLine view={view} check={check} /> : null}
          {check.failure ? <Refusal>{check.failure}</Refusal> : null}
          {result ? (
            <>
              <Facts label="Result">
                <Fact name="Commit" mono>
                  {shortCommit(result.commit)}
                </Fact>
                <Fact name="Base branch" mono>
                  {plainText(result.base_branch)}
                </Fact>
                <Fact name="Worker">{plainText(result.worker)}</Fact>
                <Fact name="Checked">
                  <Ago at={Date.parse(result.checked_at)} />
                </Fact>
              </Facts>
              {result.truncated ? <p className="small muted">Secret findings beyond the bound are left out of this list.</p> : null}
              {result.skipped ? <p className="small muted">The secret scan skipped {result.skipped} {result.skipped === 1 ? "file" : "files"} for their size or binary content.</p> : null}
              <PracticeTable rows={result.practices} findings={result.findings} />
              {result.findings.length === 0 ? (
                <p className="muted">No findings.</p>
              ) : (
                <FindingTable
                  findings={result.findings}
                  archived={archived}
                  onAcknowledge={(finding) => setDialog({ kind: "acknowledge", finding })}
                  onWithdraw={(finding) => setDialog({ kind: "withdraw", finding })}
                />
              )}
            </>
          ) : busy ? null : (
            <p className="muted">
              <Absent>Not checked yet</Absent>
            </p>
          )}
          {orphans.length > 0 ? <OrphanTable held={orphans} archived={archived} onWithdraw={(finding) => setDialog({ kind: "withdraw", finding })} /> : null}
        </ViewTime.Provider>
      </div>
      {dialog?.kind === "acknowledge" ? <AcknowledgeDialog views={views} repository={repository} finding={dialog.finding} onClose={() => setDialog(null)} after={changed} /> : null}
      {dialog?.kind === "withdraw" ? <WithdrawDialog views={views} repository={repository} finding={dialog.finding} onClose={() => setDialog(null)} after={changed} /> : null}
    </section>
  );
}
