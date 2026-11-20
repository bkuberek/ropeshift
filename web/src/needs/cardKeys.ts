// The buttons of an open card a key presses: Choose another for C and Discuss for D. Neither decides anything:
// one shows a question's other answers, and the other opens a conversation, so a single key may press them.
export type CardKey = "choose" | "discuss";

// pressOnCard presses the button of the open card that a key names, and says whether the card had one. When a
// question's other answers already show, C moves focus to the chosen one, or the first, and folds nothing.
export function pressOnCard(key: CardKey): boolean {
  const button = [...document.querySelectorAll<HTMLButtonElement>(`#main .decision [data-shortcut="${key}"]`)].find((element) => !element.closest("[hidden]") && !element.disabled);
  if (!button) {
    return false;
  }
  if (key === "choose" && button.getAttribute("aria-expanded") === "true") {
    const answers = document.getElementById(button.getAttribute("aria-controls") ?? "");
    (answers?.querySelector<HTMLElement>("input[type=radio]:checked") ?? answers?.querySelector<HTMLElement>("input[type=radio], textarea"))?.focus();
    return true;
  }
  button.click();
  return true;
}
