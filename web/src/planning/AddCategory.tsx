import { useRef, useState } from "react";

import { useChange } from "../admin/parts";
import { addCategory } from "../api/admin";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { Field } from "../ui/forms";
import { Dialog, useToast } from "../ui/overlays";
import { categoryProblem } from "./records";
import { useDialogParam } from "./useDialogParam";

// AddCategoryDialog adds a category to the workspace, as `ropeshift category add` does. A category stays: the
// ledger has no way to remove one, so the dialog says what a name must be before it sends one.
function AddCategoryDialog({ onClose }: { onClose: () => void }) {
  const [name, setName] = useState("");
  const [tried, setTried] = useState(false);
  const change = useChange();
  const toast = useToast();
  const field = useRef<HTMLInputElement>(null);
  const problem = tried ? categoryProblem(name) : "";

  const add = async () => {
    setTried(true);
    if (categoryProblem(name) !== "") {
      return;
    }
    const added = await change.run(async () => {
      await addCategory(name);
      return true;
    });
    if (added) {
      toast("success", `Added the category ${name}.`);
      onClose();
    }
  };

  return (
    <Dialog
      open
      title="Add category"
      onClose={onClose}
      initialFocus={field}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" icon="plus" loading={change.busy} onClick={() => void add()}>
            Add category
          </Button>
        </>
      }
    >
      <form
        className="field-stack"
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void add();
        }}
      >
        <p className="small muted">A category says what a question or a decision is about, so a member filters to what they can answer. It joins the built-in ones in this project.</p>
        <Field label="Name" help="Lowercase words joined by hyphens, such as release-planning." error={problem || undefined}>
          <input ref={field} className="input mono" autoComplete="off" spellCheck={false} value={name} onChange={(event) => setName(event.target.value)} />
        </Field>
        {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      </form>
    </Dialog>
  );
}

// AddCategory is the button that opens the dialog, beside the category filter of a view. The address can open
// it too, which is how the command palette does.
export function AddCategory() {
  const { open, show, close } = useDialogParam("category");
  return (
    <>
      <Button small variant="ghost" icon="plus" onClick={show}>
        Add category
      </Button>
      {open ? <AddCategoryDialog onClose={close} /> : null}
    </>
  );
}
