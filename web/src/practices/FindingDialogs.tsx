import { useRef, useState } from "react";

import { useChange } from "../admin/parts";
import { plainText } from "../admin/untrustedText";
import { acknowledgeFinding, withdrawAcknowledgement } from "../api/practices";
import type { PracticeFinding, PracticesView } from "../api/practices";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { Dialog, useToast } from "../ui/overlays";
import { acknowledgedFindings, openFindings, reasonProblem } from "./logic";

type Which = "open" | "acknowledged";

// The repositories that have a finding of the kind asked for, with the findings.
const withFindings = (views: PracticesView[], which: Which) =>
  views.map((view) => ({ view, findings: which === "open" ? openFindings(view) : acknowledgedFindings(view) })).filter((row) => row.findings.length > 0);

// useChoice is the repository and the finding a dialog stands on. It starts at the pair a row named, or at the first
// finding there is, and a change of repository moves to that repository's first finding.
function useChoice(views: PracticesView[], which: Which, repository?: string, finding?: string) {
  const rows = withFindings(views, which);
  const start = rows.find((row) => row.view.repository === repository && row.findings.some((one) => one.id === finding)) ?? rows[0];
  const [chosen, setChosen] = useState({ repository: start?.view.repository ?? "", finding: start ? (finding && start.findings.some((one) => one.id === finding) ? finding : (start.findings[0]?.id ?? "")) : "" });
  const row = rows.find((one) => one.view.repository === chosen.repository) ?? rows[0];
  const current = row?.findings.find((one) => one.id === chosen.finding) ?? row?.findings[0];
  return {
    rows,
    view: row?.view,
    finding: current,
    pickRepository: (next: string) => setChosen({ repository: next, finding: rows.find((one) => one.view.repository === next)?.findings[0]?.id ?? "" }),
    pickFinding: (next: string) => setChosen((was) => ({ ...was, finding: next })),
  };
}

function Pickers({ choice, locked }: { choice: ReturnType<typeof useChoice>; locked: boolean }) {
  if (locked) {
    return (
      <p className="small">
        <strong>{plainText(choice.view?.name ?? "")}</strong>
      </p>
    );
  }
  return (
    <>
      <Field label="Repository">
        <Select value={choice.view?.repository ?? ""} onChange={(event) => choice.pickRepository(event.target.value)}>
          {choice.rows.map((row) => (
            <option key={row.view.repository} value={row.view.repository}>
              {plainText(row.view.name)}
            </option>
          ))}
        </Select>
      </Field>
      <Field label="Finding">
        <Select className="mono" value={choice.finding?.id ?? ""} onChange={(event) => choice.pickFinding(event.target.value)}>
          {(choice.rows.find((row) => row.view === choice.view)?.findings ?? []).map((finding) => (
            <option key={finding.id} value={finding.id}>
              {plainText(finding.id)}
            </option>
          ))}
        </Select>
      </Field>
    </>
  );
}

function FindingWords({ finding }: { finding: PracticeFinding }) {
  return (
    <div className="stack-8">
      <p>
        <code className="mono">{plainText(finding.id)}</code>
      </p>
      <p className="small">{plainText(finding.detail)}</p>
      <p className="small muted">{finding.why}</p>
    </div>
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
  const field = useRef<HTMLTextAreaElement>(null);
  const problem = tried ? reasonProblem(reason) : "";
  const send = async () => {
    setTried(true);
    if (!choice.view || !choice.finding || reasonProblem(reason) !== "") {
      return;
    }
    const target = choice.finding.id;
    const done = await change.run(() => acknowledgeFinding(choice.view?.repository ?? "", target, reason));
    if (done) {
      toast("success", `Acknowledged ${target}.`);
      onClose();
      after();
    }
  };
  return (
    <Dialog
      open
      title="Acknowledge finding"
      onClose={onClose}
      initialFocus={choice.finding ? field : undefined}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          {choice.finding ? (
            <Button variant="primary" loading={change.busy} onClick={() => void send()}>
              Acknowledge
            </Button>
          ) : null}
        </>
      }
    >
      {choice.finding ? (
        <form
          className="field-stack"
          noValidate
          onSubmit={(event) => {
            event.preventDefault();
            void send();
          }}
        >
          <Pickers choice={choice} locked={repository !== undefined} />
          <FindingWords finding={choice.finding} />
          <Field label="Reason" help="Why this finding stays open. It stays beside the finding, with your name and the time, and the finding stays in the list." error={problem || undefined}>
            <textarea ref={field} className="textarea" rows={3} autoComplete="off" value={reason} onChange={(event) => setReason(event.target.value)} />
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
    if (!choice.view || !choice.finding) {
      return;
    }
    const target = choice.finding.id;
    const done = await change.run(() => withdrawAcknowledgement(choice.view?.repository ?? "", target));
    if (done) {
      toast("success", `Withdrew the acknowledgement of ${target}.`);
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
          {choice.finding ? (
            <Button variant="primary" loading={change.busy} onClick={() => void send()}>
              Withdraw acknowledgement
            </Button>
          ) : null}
        </>
      }
    >
      {choice.finding ? (
        <div className="field-stack">
          <Pickers choice={choice} locked={repository !== undefined} />
          <FindingWords finding={choice.finding} />
          {held ? (
            <p className="small">
              Acknowledged by {plainText(held.member)}: {plainText(held.reason)}
            </p>
          ) : null}
          <p className="small muted">The finding reads as an open finding again.</p>
          {change.failure ? <Refusal>{change.failure}</Refusal> : null}
        </div>
      ) : (
        <p className="muted">No finding is acknowledged.</p>
      )}
    </Dialog>
  );
}
