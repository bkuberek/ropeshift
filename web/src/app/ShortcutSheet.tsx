import { useId } from "react";

import { Button, Kbd } from "../ui/controls";
import { Dialog } from "../ui/overlays";
import { shortcutList } from "./keys";

type SheetProps = {
  open: boolean;
  onClose: () => void;
  mac: boolean;
  singleKeys: boolean;
  onSingleKeys: (on: boolean) => void;
};

// ShortcutSheet lists every shortcut, and holds the switch that turns single-key shortcuts off, which a
// person who speaks commands or slips on keys may need (WCAG 2.1.4).
export function ShortcutSheet({ open, onClose, mac, singleKeys, onSingleKeys }: SheetProps) {
  const toggle = useId();
  return (
    <Dialog
      open={open}
      title="Keyboard shortcuts"
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Close</Button>
        </>
      }
    >
      <div className="sheet">
        <label className="check-row" htmlFor={toggle}>
          <input id={toggle} type="checkbox" role="switch" className="switch" checked={singleKeys} onChange={(event) => onSingleKeys(event.target.checked)} />
          <span>
            <strong>Single-key shortcuts</strong>
            <span className="help">
              {singleKeys ? "On. Keys such as G, J, and P act while focus is outside a text field." : "Off. Only the palette's shortcut acts; every action stays one click or one palette search away."}
            </span>
          </span>
        </label>
        {shortcutList(mac).map((group) => (
          <section key={group.name} className="sheet-group" aria-label={group.name}>
            <h3 className="overline">{group.name}</h3>
            <dl className="sheet-rows">
              {group.rows.map((row) => (
                <div key={`${row.keys.join("+")}-${row.does}`} className="sheet-row">
                  <dt>
                    {row.keys.map((key, at) => (
                      <span key={key}>
                        {at > 0 && row.keys[0] === "G" ? <span className="subtle small"> then </span> : null}
                        <Kbd>{key}</Kbd>
                      </span>
                    ))}
                  </dt>
                  <dd>{row.does}</dd>
                </div>
              ))}
            </dl>
          </section>
        ))}
      </div>
    </Dialog>
  );
}
