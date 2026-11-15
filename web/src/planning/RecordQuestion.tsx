import { useRef, useState } from "react";

import { newID } from "../admin/logic";
import { useChange } from "../admin/parts";
import { recordQuestion } from "../api/client";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { Dialog, useToast } from "../ui/overlays";
import { CategoryChoices } from "./CategoryChoices";
import { noQuestion, questionProblems, questionRequest, subjectKinds } from "./records";
import type { QuestionDraft } from "./records";
import { useCategories } from "./useCategories";
import { useDialogParam } from "./useDialogParam";

// RecordQuestionDialog records a question as it is, as `ropeshift question record` does: its text, its
// categories, and the work order, contract, or plan it is about. A question that blocks keeps work on its
// subject waiting for the answer.
function RecordQuestionDialog({ onClose }: { onClose: () => void }) {
  const names = useCategories();
  const [draft, setDraft] = useState<QuestionDraft>(noQuestion);
  const [tried, setTried] = useState(false);
  const change = useChange();
  const toast = useToast();
  // The ID of this attempt holds until the dialog closes, so a press after a refusal finds what the first made.
  const id = useRef(newID());
  const field = useRef<HTMLTextAreaElement>(null);
  const problems = tried ? questionProblems(draft) : {};
  const kind = subjectKinds.find((row) => row.value === draft.subjectKind);
  const set = (next: Partial<QuestionDraft>) => setDraft((was) => ({ ...was, ...next }));

  const record = async () => {
    setTried(true);
    const stopped = questionProblems(draft);
    if (stopped.text || stopped.subject) {
      return;
    }
    const recorded = await change.run(async () => {
      await recordQuestion(questionRequest(id.current, draft));
      return true;
    });
    if (recorded) {
      toast("success", "Recorded the question.");
      onClose();
    }
  };

  return (
    <Dialog
      open
      title="Record question"
      onClose={onClose}
      initialFocus={field}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" icon="plus" loading={change.busy} onClick={() => void record()}>
            Record question
          </Button>
        </>
      }
    >
      <form
        className="field-stack"
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void record();
        }}
      >
        <p className="small muted">The question is recorded as it is. It waits on the ladder, and reaches a person when nothing before it can answer.</p>
        <Field label="Question" error={problems.text}>
          <textarea ref={field} className="textarea" rows={3} autoComplete="off" value={draft.text} onChange={(event) => set({ text: event.target.value })} />
        </Field>
        <CategoryChoices names={names} chosen={draft.categories} onChange={(categories) => set({ categories })} />
        <Field label="About" help="What the question concerns, when it concerns one record.">
          <Select value={draft.subjectKind} onChange={(event) => set({ subjectKind: event.target.value, subjectID: "", blocking: false })}>
            <option value="">Nothing in particular</option>
            {subjectKinds.map((row) => (
              <option key={row.value} value={row.value}>
                {row.about}
              </option>
            ))}
          </Select>
        </Field>
        {kind ? (
          <Field label={`${kind.name} ID`} error={problems.subject}>
            <input className="input mono" autoComplete="off" spellCheck={false} value={draft.subjectID} onChange={(event) => set({ subjectID: event.target.value })} />
          </Field>
        ) : null}
        <div className="field">
          <label className="check-row">
            <input type="checkbox" disabled={!kind} checked={draft.blocking} onChange={(event) => set({ blocking: event.target.checked })} />
            <span>Work on it waits for the answer</span>
          </label>
          <p className="help">{kind ? `Work on the ${kind.name.toLowerCase()} stays held until the question is answered.` : "Choose what the question is about first."}</p>
        </div>
        {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      </form>
    </Dialog>
  );
}

// RecordQuestion is the button that opens the dialog, in the open questions' heading. The address can open it
// too, which is how the command palette does.
export function RecordQuestion() {
  const { open, show, close } = useDialogParam("record");
  return (
    <>
      <Button icon="plus" onClick={show}>
        Record question
      </Button>
      {open ? <RecordQuestionDialog onClose={close} /> : null}
    </>
  );
}
