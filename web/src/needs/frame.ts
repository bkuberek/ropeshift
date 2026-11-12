import type { ReactNode } from "react";

import type { ProjectLabel } from "../api/types";

// Frame is what Needs you says of an item around its card: the project, what is asked, and who asked and
// how long it has waited.
export type Frame = { project?: ProjectLabel; title: string; provenance?: ReactNode };
