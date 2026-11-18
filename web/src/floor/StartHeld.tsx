import type { StartHold } from "../api/types";
import { addressOf, Link } from "../app/route";
import { Id } from "../ui/display";
import { holdWord } from "./holds";

// The hold a ready work order stands in says why it is not claimed: a phrase for the reason, the work order
// awaited, and the ledger's detail. The reason's own name, which `ropeshift work show` prints on its waiting
// line and a refused Start or claim names, stays in the title a pointer brings up.

// holdText reads a hold as a refusal of a claim does, without its prefix.
function holdText(hold: StartHold): string {
  const head = hold.dependency ? `${hold.reason} ${hold.dependency}` : hold.reason;
  return hold.detail ? `${head}: ${hold.detail}` : head;
}

// HeldFact is the hold as the work order's page states it among its facts.
export function HeldFact({ hold }: { hold: StartHold }) {
  return (
    <>
      <span title={hold.reason}>{holdWord(hold.reason)}</span>
      {hold.dependency ? (
        <>
          {" "}
          <Link to={addressOf("/work-order", { id: hold.dependency })} className="id-link">
            <Id value={hold.dependency} />
          </Link>
        </>
      ) : null}
      {hold.detail ? <span className="cell-note">{hold.detail}</span> : null}
    </>
  );
}

// HeldTag is the short mark beside ready work in a list. Its title carries the reason's name, the dependency,
// and the detail, and the work order's page gives them in full.
export function HeldTag({ hold }: { hold?: StartHold }) {
  if (!hold) {
    return null;
  }
  return (
    <span className="cond warn" title={holdText(hold)}>
      Held: {holdWord(hold.reason)}
    </span>
  );
}
