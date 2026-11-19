import { useEffect, useState } from "react";

import { useChange } from "../admin/parts";
import { plainText } from "../admin/untrustedText";
import { acknowledgeFinding, withdrawAcknowledgement } from "../api/practices";
import type { PracticeFinding, PracticesView } from "../api/practices";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { Icon } from "../ui/Icon";
import { Dialog, useToast } from "../ui/overlays";
import { acknowledgedFindings, openFindings, reasonProblem } from "./logic";

type Which = "open" | "acknowledged";

// The repositories that have a finding of the kind asked for, with the findings.
const withFindings = (views: PracticesView[], which: Which) =>
  views.map((view) => ({ view, findings: which === "open" ? openFindings(view) : acknowledgedFindings(view) })).filter((row) => row.findings.length > 0);

type Chosen = { repository: string; name: string; finding: string };

// useChoice is the repository and the finding a dialog stands on. It starts at the pair a row named, or at the first
// finding there is, and keeps that pair: the list it ranges over is read again on every ledger event, and a pair that
// leaves it is reported as gone, never swapped for another, so a reason is never recorded against a finding the member did
// not choose. Picking another repository moves to that repository's first finding.
function useChoice(views: PracticesView[], which: Which, repository?: string, finding?: string) {
  const rows = withFindings(views, which);
  const first = rows[0];
  const named = repository === undefined ? undefined : { repository, name: views.find((view) => view.repository === repository)?.name ?? "", finding: finding ?? "" };
  const start: Chosen | null = named ?? (first ? { repository: first.view.repository, name: first.view.name, finding: first.findings[0]?.id ?? "" } : null);
  const [chosen, setChosen] = useState<Chosen | null>(start);
  // A dialog opened with nothing to choose from takes the first finding that arrives, and keeps it.
  useEffect(() => {
    if (chosen === null && start !== null) {
      setChosen(start);
    }
  }, [chosen, start?.repository, start?.finding]);
  const row = rows.find((one) => one.view.repository === chosen?.repository);
  const current = row?.findings.find((one) => one.id === chosen?.finding);
  return {
    rows,
    chosen,
    finding: current,
    gone: chosen !== null && current === undefined,
    pickRepository: (next: string) => {
      const picked = rows.find((one) => one.view.repository === next);
      setChosen({ repository: next, name: picked?.view.name ?? "", finding: picked?.findings[0]?.id ?? "" });
    },
    pickFinding: (next: string) => setChosen((was) => (was ? { ...was, finding: next } : was)),
  };
}

type Choice = ReturnType<typeof useChoice>;

function Pickers({ choice, locked }: { choice: Choice; locked: boolean }) {
  const chosen = choice.chosen;
  if (!chosen) {
    return null;
  }
  if (locked) {
    return (
      <div className="stack-8">
        <p className="small">
          <strong>{plainText(chosen.name)}</strong>
        </p>
        <p>
          <code className="mono">{plainText(chosen.finding)}</code>
        </p>
      </div>
    );
  }
  const held = choice.rows.find((row) => row.view.repository === chosen.repository);
  const findings = held?.findings.map((one) => one.id) ?? [];
  return (
    <>
      <Field label="Repository">
        <Select value={chosen.repository} onChange={(event) => choice.pickRepository(event.target.value)}>
          {held ? null : <option value={chosen.repository}>{plainText(chosen.name || chosen.repository)}</option>}
          {choice.rows.map((row) => (
            <option key={row.view.repository} value={row.view.repository}>
              {plainText(row.view.name)}
            </option>
          ))}
        </Select>
      </Field>
      <Field label="Finding">
        <Select className="mono" value={chosen.finding} onChange={(event) => choice.pickFinding(event.target.value)}>
          {findings.includes(chosen.finding) ? null : <option value={chosen.finding}>{plainText(chosen.finding)}</option>}
          {findings.map((id) => (
            <option key={id} value={id}>
              {plainText(id)}
            </option>
          ))}
        </Select>
      </Field>
    </>
  );
}

function FindingWords({ finding, withId }: { finding: PracticeFinding; withId: boolean }) {
  return (
    <div className="stack-8">
      {withId ? (
        <p>
          <code className="mono">{plainText(finding.id)}</code>
        </p>
      ) : null}
      {finding.detail ? <p className="small">{plainText(finding.detail)}</p> : null}
      {finding.why ? <p className="small muted">{finding.why}</p> : null}
    </div>
  );
}

// Gone says the pair the member chose left the list while the dialog was open, and what it was: the finding stays chosen,
// the dialog cannot send it, and the pickers, if there are any, choose another.
function Gone({ children }: { children: string }) {
  return (
    <p className="msg msg-warning" role="alert">
      <Icon name="warning" size="sm" />
      <span>{children}</span>
    </p>
  );
}

type Props = {
  views: PracticesView[];
  // repository and finding are the pair a row of a finding's table names. The command palette names none, and the member picks.
  repository?: string;
  finding?: string;
  onClose: () => void;
  after: () => void;
};

// AcknowledgeDialog leaves a finding open with the member's reason, which is required, as `ropeshift practices acknowledge`
// does (FR-PRAC-10). The finding stays in every view of the result and reads Acknowledged with the reason, the member, and
// the time.
export function AcknowledgeDialog({ views, repository, finding, onClose, after }: Props) {
  const choice = useChoice(views, "open", repository, finding);
  const [reason, setReason] = useState("");
  const [tried, setTried] = useState(false);
  const change = useChange();
  const toast = useToast();
  const problem = tried ? reasonProblem(reason) : "";
  const send = async () => {
    setTried(true);
    if (!choice.chosen || choice.gone || reasonProblem(reason) !== "") {
      return;
    }
    const { repository: target, finding: id } = choice.chosen;
    const done = await change.run(() => acknowledgeFinding(target, id, reason));
    if (done) {
      toast("success", `Acknowledged ${id}.`);
      onClose();
      after();
    }
  };
  return (
    <Dialog
      open
      title="Acknowledge finding"
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          {choice.chosen ? (
            <Button variant="primary" loading={change.busy} disabled={choice.gone} onClick={() => void send()}>
              Acknowledge
            </Button>
          ) : null}
        </>
      }
    >
      {choice.chosen ? (
        <form
          className="field-stack"
          noValidate
          onSubmit={(event) => {
            event.preventDefault();
            void send();
          }}
        >
          <Pickers choice={choice} locked={repository !== undefined} />
          {choice.finding ? <FindingWords finding={choice.finding} withId={repository === undefined} /> : <Gone>This finding is no longer open.</Gone>}
          <Field label="Reason" help="Why this finding stays open. It stays beside the finding, with your name and the time, and the finding stays in the list." error={problem || undefined}>
            <textarea className="textarea" rows={3} autoComplete="off" value={reason} onChange={(event) => setReason(event.target.value)} />
          </Field>
          {change.failure ? <Refusal>{change.failure}</Refusal> : null}
        </form>
      ) : (
        <p className="muted">No repository has an open finding.</p>
      )}
    </Dialog>
  );
}

// WithdrawDialog takes an acknowledgement back, so the finding reads plain again, as `ropeshift practices withdraw` does.
export function WithdrawDialog({ views, repository, finding, onClose, after }: Props) {
  const choice = useChoice(views, "acknowledged", repository, finding);
  const change = useChange();
  const toast = useToast();
  const send = async () => {
    if (!choice.chosen || choice.gone) {
      return;
    }
    const { repository: target, finding: id } = choice.chosen;
    const done = await change.run(() => withdrawAcknowledgement(target, id));
    if (done) {
      toast("success", `Withdrew the acknowledgement of ${id}.`);
      onClose();
      after();
    }
  };
  const held = choice.finding?.acknowledgement;
  return (
    <Dialog
      open
      title="Withdraw acknowledgement"
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          {choice.chosen ? (
            <Button variant="primary" loading={change.busy} disabled={choice.gone} onClick={() => void send()}>
              Withdraw acknowledgement
            </Button>
          ) : null}
        </>
      }
    >
      {choice.chosen ? (
        <div className="field-stack">
          <Pickers choice={choice} locked={repository !== undefined} />
          {choice.finding ? <FindingWords finding={choice.finding} withId={repository === undefined} /> : <Gone>This acknowledgement was already withdrawn.</Gone>}
          {held ? (
            <p className="small">
              Acknowledged by {plainText(held.member)}: {plainText(held.reason)}
            </p>
          ) : null}
          {choice.finding ? <p className="small muted">The finding reads as an open finding again.</p> : null}
          {change.failure ? <Refusal>{change.failure}</Refusal> : null}
        </div>
      ) : (
        <p className="muted">No finding is acknowledged.</p>
      )}
    </Dialog>
  );
}
