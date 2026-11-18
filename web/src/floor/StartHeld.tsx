import type { StartHold } from "../api/types";
import { addressOf, Link } from "../app/route";
import { Id } from "../ui/display";

// The hold a ready work order stands in says why it is not claimed, in the ledger's own words: the reason's
// name, the work order awaited, and the detail. `ropeshift work show` prints the same three parts on its
// waiting line, and a refused Start or claim says them too, so every place reads alike.

// HeldFact is the hold as the work order's page states it among its facts.
export function HeldFact({ hold }: { hold: StartHold }) {
  return (
    <>
      <span className="mono">{hold.reason}</span>
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

// HeldTag is the short mark beside ready work in a list. Its title carries the dependency and the detail, and
// the work order's page gives them in full.
export function HeldTag({ hold }: { hold?: StartHold }) {
  if (!hold) {
    return null;
  }
  const why = [hold.dependency, hold.detail].filter(Boolean).join(": ");
  return (
    <span className="cond warn" title={why || undefined}>
      Held: {hold.reason}
    </span>
  );
}
