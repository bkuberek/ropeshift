import type { Collision, Consolidation } from "../api/types";

// mergeCommitKeepsHistory says why an adoption branch that imports a design repository's history merges only with a
// merge commit.
export const mergeCommitKeepsHistory =
  "Only a merge commit keeps the design history: a squash or a rebase merge keeps the files and drops the design repository's commits, because it drops the merge's second parent.";

// personMergesText tells a person at person_merges how to merge a consolidating adoption branch.
export const personMergesText = `At person_merges you merge this branch yourself: merge it with a merge commit. ${mergeCommitKeepsHistory}`;

const keptWords: Record<Collision["kept"], string> = { workspace: "the workspace's version", union: "the lines of both" };

// keptText says which version of a path both repositories held the merge kept.
export function keptText(collision: Collision): string {
  return keptWords[collision.kept] ?? collision.kept;
}

// importedText says what history the adoption imported and in which merge, or why the merge could not be read.
export function importedText(consolidation: Consolidation): string {
  if (consolidation.unread) {
    return `The adoption imports the history of ${consolidation.design}, and its merge could not be read: ${consolidation.unread}`;
  }
  return `The adoption imports the history of ${consolidation.design} in merge commit ${consolidation.merge ?? ""}, whose second parent ${consolidation.design_tip ?? ""} is the design repository's tip.`;
}

// truncatedText says a list of colliding paths was cut short.
export const truncatedText = "The list is cut short: more paths were held by both.";

// noRemovalText says why approving removes no repository.
export function noRemovalText(why: string): string {
  return `Approving removes no repository: ${why}`;
}

// removalText says which repository approving removes from the project.
export function removalText(removes: NonNullable<Consolidation["removes"]>): string {
  return `Approving removes repository ${removes.name || removes.id} from the project, and keeps its record as history.`;
}
