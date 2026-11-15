import { useEffect, useRef, useState } from "react";

import { newID } from "../admin/logic";
import { useChange } from "../admin/parts";
import { readPlans, startContractReview } from "../api/admin";
import type { Plan } from "../api/types";
import { addressOf, go } from "../app/route";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { Field, Select } from "../ui/forms";
import { Dialog, useToast } from "../ui/overlays";
import { useDialogParam } from "./useDialogParam";

type Read = { plans: Plan[] } | { failure: string } | null;

// usePlans reads the plans the project holds, once the dialog opens, for a dialog that asks which plan to
// review. A dialog on a plan's own page asks nothing, so it reads nothing.
function usePlans(wanted: boolean): Read {
  const [read, setRead] = useState<Read>(null);
  useEffect(() => {
    if (!wanted) {
      return;
    }
    let current = true;
    void (async () => {
      try {
        const list = await readPlans();
        if (current) {
          setRead({ plans: list.plans });
        }
      } catch (error) {
        if (current) {
          setRead({ failure: error instanceof Error ? error.message : String(error) });
        }
      }
    })();
    return () => {
      current = false;
    };
  }, [wanted]);
  return read;
}

// A title two plans share is told apart by the plan's ID.
const labelsOf = (plans: Plan[]) => plans.map((plan) => ({ id: plan.id, label: plans.filter((other) => other.title === plan.title).length > 1 ? `${plan.title} (${plan.id})` : plan.title }));

// StartReviewDialog starts a contract review of a plan as `ropeshift plan review` does: a work order that
// reads the plan and its contracts at their newest versions, and stands ready for a member to claim. The work
// the review reads stays where it is. A plan's own page names the plan, and the list of plans asks which.
function StartReviewDialog({ plan, onClose }: { plan?: Plan; onClose: () => void }) {
  const found = usePlans(plan === undefined);
  const [chosen, setChosen] = useState("");
  const [tried, setTried] = useState(false);
  const change = useChange();
  const toast = useToast();
  // The ID of this attempt holds until the dialog closes, so a press after a refusal finds what the first made.
  const id = useRef(newID());
  const plans = found && "plans" in found ? found.plans : [];

  useEffect(() => {
    if (plans.length === 1) {
      setChosen((was) => was || (plans[0]?.id ?? ""));
    }
  }, [found]);

  const target = plan ?? plans.find((row) => row.id === chosen);
  // What stops a choice: the plans could not be read, the project holds none, or none is chosen yet.
  const problem =
    found && "failure" in found
      ? `The plans could not be read. ${found.failure}`
      : found && plans.length === 0
        ? "This project has no plan to review yet."
        : tried && !target
          ? "Choose the plan to review."
          : undefined;
  const start = async () => {
    setTried(true);
    if (!target) {
      return;
    }
    const review = await change.run(() => startContractReview(target.id, id.current));
    if (review) {
      toast("success", `Started the contract review of ${target.title}.`);
      onClose();
      go(addressOf("/work-order", { id: review.id }));
    }
  };

  return (
    <Dialog
      open
      title="Start contract review"
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" icon="play" loading={change.busy} onClick={() => void start()}>
            Start contract review
          </Button>
        </>
      }
    >
      <form
        className="field-stack"
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void start();
        }}
      >
        <p className="small muted">
          A contract review reads the plan and its contracts at their newest versions and collects every question a lane would have to stop and ask. It is recorded as a work order that is ready for a worker to claim, and the work it reads stays where it is.
        </p>
        {plan ? (
          <p>
            Plan: <strong>{plan.title}</strong>
          </p>
        ) : (
          // The choice stays on the page whatever the read found, so focus stays where the dialog put it.
          <Field label="Plan" help="The plan whose contracts the review reads." error={problem}>
            <Select value={chosen} onChange={(event) => setChosen(event.target.value)}>
              <option value="">{found === null ? "Reading…" : "Choose a plan"}</option>
              {labelsOf(plans).map((row) => (
                <option key={row.id} value={row.id}>
                  {row.label}
                </option>
              ))}
            </Select>
          </Field>
        )}
        {change.failure ? <Refusal>{change.failure}</Refusal> : null}
      </form>
    </Dialog>
  );
}

// StartReview is the button that opens the dialog, on the plans and on a plan's page. A plan's page passes the
// plan, and the dialog asks nothing more. The address can open it too, which is how the command palette does.
export function StartReview({ plan }: { plan?: Plan }) {
  const { open, show, close } = useDialogParam("review");
  return (
    <>
      <Button icon="play" onClick={show}>
        Start contract review
      </Button>
      {open ? <StartReviewDialog plan={plan} onClose={close} /> : null}
    </>
  );
}
