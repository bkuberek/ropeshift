import type { NewWork } from "../api/admin";
import { plainID } from "../api/client";
import { listOf } from "../planning/records";

// What the New work order form asks of a member before it sends. None of it decides what the API decides:
// the API refuses what it refuses, and its words show at the foot of the dialog. These only keep a request
// the ledger would refuse for its form from being sent, and read as the ledger does: an ID is plain, and a
// requirement key may hold a dot.

const requirementKey = /^[A-Za-z0-9_.-]{1,64}$/;
const wholeNumber = /^-?\d+$/;

export type WorkDraft = {
  title: string;
  // criteria are one to a line, as `--criterion` is given once for each.
  criteria: string;
  kind: "change" | "plan";
  statement: string;
  repository: string;
  dependsOn: string;
  requirements: string;
  agent: string;
  size: "" | "small" | "medium" | "large";
  priority: string;
  id: string;
};

export const noWork: WorkDraft = { title: "", criteria: "", kind: "change", statement: "", repository: "", dependsOn: "", requirements: "", agent: "", size: "", priority: "", id: "" };

// idsOf reads the IDs or keys a member wrote: a space, a comma, or a new line ends one, and a repeat is one.
const idsOf = (text: string) => listOf(text, /[\s,]+/);

// criteriaOf reads the criteria a member wrote, one to a line, as the command reads each `--criterion`.
const criteriaOf = (text: string) =>
  text
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");

export type WorkProblems = { title?: string; id?: string; dependsOn?: string; requirements?: string; priority?: string };

// inMore says a problem sits in a field under More, which a closed section would hide.
export const inMore = (problems: WorkProblems): boolean => Boolean(problems.id || problems.dependsOn || problems.requirements || problems.priority);

// workProblems says what stops a work order from being sent: the title the work order is called by, and each
// ID, key, and number a member wrote that the ledger would read as another thing.
export function workProblems(draft: WorkDraft): WorkProblems {
  const problems: WorkProblems = {};
  if (draft.title.trim() === "") {
    problems.title = "Give the work order a title.";
  }
  if (draft.id.trim() !== "" && !plainID.test(draft.id.trim())) {
    problems.id = "An ID is 1 to 64 letters, digits, - and _.";
  }
  if (idsOf(draft.dependsOn).some((id) => !plainID.test(id))) {
    problems.dependsOn = "Each ID is 1 to 64 letters, digits, - and _, and two are told apart by a space or a comma.";
  }
  if (idsOf(draft.requirements).some((key) => !requirementKey.test(key))) {
    problems.requirements = "Each key is 1 to 64 letters, digits, - _ and ., and two are told apart by a space or a comma.";
  }
  const priority = draft.priority.trim();
  if (priority !== "" && !(wholeNumber.test(priority) && Number.isSafeInteger(Number(priority)))) {
    problems.priority = "A priority is a whole number, such as 2.";
  }
  return problems;
}

// workRequest is the request `ropeshift work create` would send for the same flags: what a member left
// empty is left out, and a change, which the API reads when no kind is given, names none.
export function workRequest(id: string, draft: WorkDraft): NewWork {
  const request: NewWork = { id: draft.id.trim() || id, title: draft.title.trim() };
  if (draft.kind === "plan") {
    request.kind = "plan";
  }
  if (draft.statement.trim() !== "") {
    request.statement = draft.statement.trim();
  }
  const criteria = criteriaOf(draft.criteria);
  if (criteria.length > 0) {
    request.criteria = criteria.map((text, at) => ({ id: `c${at + 1}`, text }));
  }
  if (draft.repository !== "") {
    request.repository_id = draft.repository;
  }
  const dependsOn = idsOf(draft.dependsOn);
  if (dependsOn.length > 0) {
    request.depends_on = dependsOn;
  }
  const requirements = idsOf(draft.requirements);
  if (requirements.length > 0) {
    request.requirements = requirements;
  }
  if (draft.agent !== "") {
    request.agent = draft.agent;
  }
  if (draft.size !== "") {
    request.size = draft.size;
  }
  const priority = Number(draft.priority.trim() || "0");
  if (priority !== 0) {
    request.priority = priority;
  }
  return request;
}
