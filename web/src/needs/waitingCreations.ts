import { createContext } from "react";

import type { NeedItem } from "../api/types";

// WaitingCreations holds the repositories an approved plan version creates that Needs you lists, each by the project that
// holds it, its approval, and its name, so a row of the version's registration links the item that waits for a member.
export const WaitingCreations = createContext<ReadonlySet<string>>(new Set());

// waitingKey names one repository creation among the combined Needs you's projects.
export const waitingKey = (project: string, approval: string, name: string) => `${project}|${approval}/${name}`;

// waitingIn lists the creations the items of Needs you hold.
export function waitingIn(items: readonly NeedItem[]): Set<string> {
  const out = new Set<string>();
  for (const item of items) {
    if (item.kind === "repository_creation") {
      out.add(waitingKey(item.project?.name ?? "", item.repository_creation.approval, item.repository_creation.name));
    }
  }
  return out;
}

// isWaiting says Needs you lists the creation. An item that names no project is found whichever project the page shows.
export function isWaiting(waiting: ReadonlySet<string>, project: string, approval: string, name: string): boolean {
  return waiting.has(waitingKey(project, approval, name)) || waiting.has(waitingKey("", approval, name));
}
