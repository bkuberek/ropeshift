import { useEffect, useState } from "react";

import { readCategories } from "../api/admin";

// useCategories reads the project's categories, built-in and added, once a dialog that offers them opens. It
// is null while the read is out, and empty when it failed, so a dialog still stands and the API refuses what
// a category it does not know would have been.
export function useCategories(): string[] | null {
  const [names, setNames] = useState<string[] | null>(null);
  useEffect(() => {
    let current = true;
    readCategories().then(
      (read) => current && setNames(read.categories),
      () => current && setNames([]),
    );
    return () => {
      current = false;
    };
  }, []);
  return names;
}
