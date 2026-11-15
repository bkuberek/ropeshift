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
