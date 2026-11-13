import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";

import { decideApproval, inProjectOf, readApproval } from "../api/client";
import type { Approval, BodyChange, CardNeed, FieldChange } from "../api/types";
import { cardStates } from "../floor/states";
import { timeOf } from "../admin/parts";
import { Button } from "../ui/controls";
import { DecisionCard, DecisionSection } from "../ui/DecisionCard";
import { Diff } from "../ui/Diff";
import { Actor, Refusal, StateBadge } from "../ui/display";
import { Icon } from "../ui/Icon";
import { lineDiff } from "../ui/lineDiff";
import type { Frame } from "./frame";
import { useAct } from "./useAct";

const voidWords = "The subject changed since the card was prepared. Ask the delegate to prepare it again.";

const channelWords: Record<string, string> = { web: "on the web", cli: "on the command line" };

const shown = (value: unknown) => (typeof value === "string" ? value : JSON.stringify(value));

// short is the start of a digest a person compares by eye.
const short = (digest: string) => digest.replace(/^sha256:/, "").slice(0, 7);

function Changes({ changes, operation, subject }: { changes: CardNeed["detail"]["changes"]; operation: string; subject: string }) {
  if (!Array.isArray(changes)) {
    const body = changes as BodyChange;
    return <Diff lines={lineDiff(body.before ?? "", body.after ?? "")} label="What the card changes" />;
  }
  const rows = changes as FieldChange[];
  return (
    <>
      {rows.length === 0 ? <p className="small muted">The call sends no arguments.</p> : null}
      <ul className="changes">
        {rows.map((row) => (
          <li key={row.field}>
            <span className="mono">{row.field}</span>
            {": "}
            {"from" in row ? (
              <>
                <code>{shown(row.from)}</code> <span aria-hidden="true">→</span>
                <span className="visually-hidden">to</span>{" "}
              </>
            ) : null}
            <code>{shown(row.to)}</code>
          </li>
        ))}
      </ul>
      {operation === "pane.input" ? (
        <p className="small">
          These keys are bound to {subject.replace(/^lane:/, "lane ")}, not to what its pane shows: they reach the lane when you confirm, whatever its pane shows then.
        </p>
      ) : null}
    </>
  );
}

// Resolution says how a card came out, who decided it, where, and when. A card's buttons leave the page
// once it is decided, so the outcome takes the focus that would otherwise fall to the page itself.
function Resolution({ approval }: { approval: Approval }) {
  const [kind = "", id = ""] = (approval.decided_by ?? "").split(/:(.*)/);
  const where = channelWords[approval.channel ?? ""];
  const note = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (document.activeElement === document.body || document.activeElement === null) {
      note.current?.focus();
    }
  }, []);
  return (
    <div className="resolved-note" role="status" tabIndex={-1} ref={note}>
      <StateBadge table={cardStates} state={approval.state} />
      <span className="small muted">
        {approval.decided_by ? (
          <>
            by <Actor kind={kind} id={id} />
          </>
        ) : null}
        {where ? ` ${where}` : ""}
        {approval.decided_at ? `, ${timeOf(approval.decided_at)}` : ""}
      </span>
    </div>
  );
}

type Props = { need: CardNeed; after: () => Promise<void>; frame: Frame; outcome?: Approval };

// ConfirmCard is a confirmation card a delegate prepared (ADR-0024): the exact call it runs, why the
// delegate proposes it, and what it changes, bound to its subject as the card was prepared. Confirm runs
// that call as this member, with the digest on this page, so a card whose subject moved is void instead.
export function ConfirmCard({ need, after, frame, outcome }: Props) {
  const { act, busy, failure } = useAct(after);
  const { approval, detail } = need;
  const card = outcome ?? approval;
  const decide = (allow: boolean) => act(() => decideApproval(approval.id, approval.subject_digest, allow, ""));
  const open = card.state === "pending";
  let resolution: ReactNode = null;
  if (!open) {
    resolution = <Resolution approval={card} />;
  }
  return (
    <DecisionCard
      className={open ? "card-confirm" : "card-confirm is-resolved"}
      kind={{ icon: "card", name: "Confirmation card" }}
      project={frame.project}
      title={frame.title}
      provenance={
        <>
          Prepared by{" "}
          <span className="actor">
            <Icon name="key" size="sm" />
            <strong>{need.label}</strong>
          </span>
          {need.member ? ` for ${need.member}` : ""}
          {frame.provenance ? <> · {frame.provenance}</> : null}
        </>
      }
      binding={
        open ? (
          <>
            Bound to <span className="mono">{approval.subject}</span> at <span className="digest">{short(approval.subject_digest)}</span>. If it changes before you confirm, this card is void.
          </>
        ) : undefined
      }
      resolution={resolution ?? undefined}
      actions={
        <>
          <Button disabled={busy} onClick={() => void decide(false)}>
            Decline
          </Button>
          <Button variant="primary" loading={busy} disabled={busy} onClick={() => void decide(true)}>
            Confirm
          </Button>
        </>
      }
    >
      <DecisionSection title="What it changes">
        <Changes changes={detail.changes} operation={detail.operation} subject={approval.subject} />
      </DecisionSection>
      <DecisionSection title="Why the delegate proposes it">
        <p>{detail.why}</p>
      </DecisionSection>
      <details className="disclosure">
        <summary>Exact operation</summary>
        <pre className="op">
          {`${detail.method} ${detail.path}\n${JSON.stringify(detail.arguments, null, 2)}\nsubject: ${approval.subject}\ndigest: ${approval.subject_digest}`}
        </pre>
      </details>
      {card.state === "failed" && card.reason ? <Refusal>{card.reason}</Refusal> : null}
      {card.state === "void" ? <p className="small">{voidWords}</p> : null}
      {failure && open ? <Refusal>{failure}</Refusal> : null}
    </DecisionCard>
  );
}

// ResolvedCard shows how a card that left Needs you came out, as the project's ledger holds it now: here
// after a member decided it, or in another place.
export function ResolvedCard({ need, project, after, frame }: { need: CardNeed; project: string; after: () => Promise<void>; frame: Frame }) {
  const [outcome, setOutcome] = useState<Approval | null>(null);
  useEffect(() => {
    let live = true;
    const read = () => readApproval(need.approval.id);
    void (project ? inProjectOf(project, read) : read()).then(
      (record) => live && setOutcome(record),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [need.approval.id, project]);
  return outcome ? <ConfirmCard need={need} after={after} frame={frame} outcome={outcome} /> : null;
}
