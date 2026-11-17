import { fullQuestionTitle, questionTitle } from "./kinds";

// QuestionHeading titles a single question by its own text, cut when long, with the whole text one hover away.
export function QuestionHeading({ text }: { text: string }) {
  const title = questionTitle(text);
  const full = fullQuestionTitle(text);
  return <span title={full === title ? undefined : full}>{title}</span>;
}
