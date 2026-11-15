import { plainID } from "../api/client";
import type { RecordQuestion } from "../api/types";

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
