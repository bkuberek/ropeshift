import { useEffect, useState } from "react";

// useNarrow says the page is as narrow as a phone, where a list and its detail take turns.
export function useNarrow(): boolean {
  const query = "(max-width: 639px)";
  const [narrow, setNarrow] = useState(() => window.matchMedia?.(query).matches ?? false);
  useEffect(() => {
    const media = window.matchMedia?.(query);
    const change = () => setNarrow(media?.matches ?? false);
    media?.addEventListener?.("change", change);
    return () => media?.removeEventListener?.("change", change);
  }, []);
  return narrow;
}
