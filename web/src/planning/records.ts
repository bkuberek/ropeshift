import { plainID } from "../api/client";
import type { Decision, OverturnDecision, RecordDecision, RecordQuestion } from "../api/types";

// What the dialogs that record questions, decisions, and categories ask of a member before they send. None
// of it decides what the API decides: the API refuses what it refuses, and its words show at the field or
// at the dialog's foot. These only keep a request the ledger would refuse for its form from being sent.

const maxCategoryName = 63;
const categoryName = /^[a-z][a-z0-9-]*$/;

// categoryProblem says why a name cannot be a category, or an empty string for one that can. The ledger
// takes lowercase words joined by hyphens, so one category cannot appear under two spellings.
export function categoryProblem(name: string): string {
  if (name === "") {
    return "Give the category a name.";
  }
  if (name.length > maxCategoryName || !categoryName.test(name)) {
    return `A category is lowercase words joined by hyphens, such as release-planning. It starts with a letter, and it is at most ${maxCategoryName} characters.`;
  }
  return "";
}

// What a question can be about: the kinds of record the ladder holds the work of.
export const subjectKinds = [
  { value: "work_order", about: "A work order", name: "Work order" },
  { value: "contract", about: "A contract", name: "Contract" },
  { value: "plan", about: "A plan", name: "Plan" },
] as const;

export type QuestionDraft = { text: string; categories: string[]; subjectKind: string; subjectID: string; blocking: boolean };

export const noQuestion: QuestionDraft = { text: "", categories: [], subjectKind: "", subjectID: "", blocking: false };

// questionProblems says what stops a question from being sent: what it asks, and what it is about when it
// is about something. A subject is a kind and an ID together, or neither.
export function questionProblems(draft: QuestionDraft): { text?: string; subject?: string } {
  const problems: { text?: string; subject?: string } = {};
  if (draft.text.trim() === "") {
    problems.text = "Write the question.";
  }
  if (draft.subjectKind !== "") {
    const name = subjectKinds.find((kind) => kind.value === draft.subjectKind)?.name.toLowerCase() ?? "record";
    if (draft.subjectID.trim() === "") {
      problems.subject = `Name the ${name}, or choose nothing in particular.`;
    } else if (!plainID.test(draft.subjectID.trim())) {
      problems.subject = "An ID is 1 to 64 letters, digits, - and _.";
    }
  }
  return problems;
}

// questionRequest is the request `ropeshift question record` would send for the same flags: what a member
// left empty is left out.
export function questionRequest(id: string, draft: QuestionDraft): RecordQuestion {
  const request: RecordQuestion = { id, text: draft.text.trim() };
  if (draft.categories.length > 0) {
    request.categories = draft.categories;
  }
  if (draft.subjectKind !== "") {
    request.subject_kind = draft.subjectKind;
    request.subject_id = draft.subjectID.trim();
    if (draft.blocking) {
      request.blocking = true;
    }
  }
  return request;
}

// listOf reads a list a member wrote, one entry to a line, and drops the empty ones and the repeats. IDs are
// written the same way, and a comma or a space between two is read as the end of one.
export function listOf(text: string, separator: RegExp = /\n/): string[] {
  const seen = new Set<string>();
  for (const part of text.split(separator)) {
    const entry = part.trim();
    if (entry !== "") {
      seen.add(entry);
    }
  }
  return [...seen];
}

// The prefix plan approval names the decisions it makes under: anyone who reads a plan can work such an ID
// out, so no other writer names a decision under it.
const planDecisionPrefix = "pd-";

// decisionIDProblem says why a member cannot name a new decision by an ID, or an empty string for one that can
// be named, and for none, which the page names itself. It reads as the ledger does: an ID is plain, and the
// prefix is read as written.
export function decisionIDProblem(id: string): string {
  if (id === "") {
    return "";
  }
  if (!plainID.test(id)) {
    return "An ID is 1 to 64 letters, digits, - and _.";
  }
  if (id.startsWith(planDecisionPrefix)) {
    return `A decision's ID may not start with ${planDecisionPrefix}, which only plan approval names decisions under.`;
  }
  return "";
}

export type DecisionDraft = {
  statement: string;
  rationale: string;
  assumed: boolean;
  reversibility: "" | "reversible" | "one_way";
  scope: string;
  categories: string[];
  citations: string;
  supersedes: string;
  questionID: string;
  noConflict: string;
  id: string;
};

export const noDecision: DecisionDraft = {
  statement: "",
  rationale: "",
  assumed: false,
  reversibility: "",
  scope: "",
  categories: [],
  citations: "",
  supersedes: "",
  questionID: "",
  noConflict: "",
  id: "",
};

// idsOf reads the decision or question IDs a member wrote.
const idsOf = (text: string) => listOf(text, /[\s,]+/);

export type DecisionProblems = { statement?: string; id?: string; questionID?: string; noConflict?: string };

// decisionProblems says what stops a decision from being sent: what it states, and each ID a member wrote
// the ledger would read as another thing.
export function decisionProblems(draft: DecisionDraft): DecisionProblems {
  const problems: DecisionProblems = {};
  if (draft.statement.trim() === "") {
    problems.statement = "State what was decided.";
  }
  const named = decisionIDProblem(draft.id.trim());
  if (named) {
    problems.id = named;
  }
  if (draft.questionID.trim() !== "" && !plainID.test(draft.questionID.trim())) {
    problems.questionID = "An ID is 1 to 64 letters, digits, - and _.";
  }
  if (idsOf(draft.noConflict).some((id) => !plainID.test(id))) {
    problems.noConflict = "Each ID is 1 to 64 letters, digits, - and _, and two are told apart by a space or a comma.";
  }
  return problems;
}

// decisionRequest is the request `ropeshift decision record` would send for the same flags: what a member left
// empty is left out, and the decision is `assumed` when it is recorded as an assumption.
export function decisionRequest(id: string, draft: DecisionDraft): RecordDecision {
  const request: RecordDecision = { id: draft.id.trim() || id, status: draft.assumed ? "assumed" : "decided", statement: draft.statement.trim() };
  if (draft.rationale.trim() !== "") {
    request.rationale = draft.rationale.trim();
  }
  if (draft.scope.trim() !== "") {
    request.scope = draft.scope.trim();
  }
  if (draft.reversibility !== "") {
    request.reversibility = draft.reversibility;
  }
  if (draft.supersedes !== "") {
    request.supersedes = draft.supersedes;
  }
  if (draft.questionID.trim() !== "") {
    request.question_id = draft.questionID.trim();
  }
  const citations = listOf(draft.citations);
  if (citations.length > 0) {
    request.citations = citations;
  }
  if (draft.categories.length > 0) {
    request.categories = draft.categories;
  }
  const noConflict = idsOf(draft.noConflict);
  if (noConflict.length > 0) {
    request.no_conflict = noConflict;
  }
  return request;
}

// inForce says a decision still stands: it is decided or assumed, and no other decision replaced or overturned it.
export const inForce = (decision: Decision) => decision.status === "decided" || decision.status === "assumed";

export type OverturnDraft = { statement: string; rationale: string; citations: string; id: string };

export const noOverturn: OverturnDraft = { statement: "", rationale: "", citations: "", id: "" };

// overturnProblems says what stops an overturn from being sent: what stands instead, and an ID a member named
// the new decision by, which reads as a decision's ID does.
export function overturnProblems(draft: OverturnDraft): { statement?: string; id?: string } {
  const problems: { statement?: string; id?: string } = {};
  if (draft.statement.trim() === "") {
    problems.statement = "State what stands instead.";
  }
  const named = decisionIDProblem(draft.id.trim());
  if (named) {
    problems.id = named;
  }
  return problems;
}

// overturnRequest is the request `ropeshift decision overturn` would send for the same flags.
export function overturnRequest(id: string, draft: OverturnDraft): OverturnDecision {
  const request: OverturnDecision = { id: draft.id.trim() || id, statement: draft.statement.trim() };
  if (draft.rationale.trim() !== "") {
    request.rationale = draft.rationale.trim();
  }
  const citations = listOf(draft.citations);
  if (citations.length > 0) {
    request.citations = citations;
  }
  return request;
}
