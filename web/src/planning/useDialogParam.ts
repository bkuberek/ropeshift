import { useEffect, useState } from "react";

import { go, useRoute } from "../app/route";

// useDialogParam holds a dialog that a button opens and an address can open: the command palette goes to
// `?dialog=name` on the view that owns the dialog, and a link can say the same. Closing it takes the
// parameter off the address, so a reload does not open it again.
export function useDialogParam(name: string) {
  const route = useRoute();
  const asked = route.params.get("dialog") === name;
  // It opens in an effect, never in the first render, so a view a person just moved to has taken its own
  // focus before the dialog takes it, and the dialog is where focus ends.
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (asked) {
      setOpen(true);
    }
  }, [asked]);
  const close = () => {
    setOpen(false);
    if (asked) {
      const rest = new URLSearchParams(route.params);
      rest.delete("dialog");
      const query = rest.toString();
      go(`${route.pathname}${query ? `?${query}` : ""}`, { stay: true, replace: true });
    }
  };
  return { open, show: () => setOpen(true), close };
}
