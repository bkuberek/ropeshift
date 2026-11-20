import { describe, expect, it } from "vitest";

import { pressOnCard } from "./cardKeys";

// A card as Needs you shows it: the open one, and one hidden behind it.
function cards(expanded = false) {
  document.body.innerHTML = `
    <main id="main">
      <div hidden><article class="decision"><button data-shortcut="discuss" id="hidden-discuss">Discuss</button></article></div>
      <div>
        <article class="decision">
          <fieldset id="q1"><input type="radio" name="q1" value="A" id="first"><input type="radio" name="q1" value="B" id="chosen" checked></fieldset>
          <button data-shortcut="discuss" id="discuss">Discuss</button>
          <button data-shortcut="choose" id="choose" aria-controls="q1" aria-expanded="${expanded}">Choose another</button>
        </article>
      </div>
    </main>`;
  const clicked: string[] = [];
  for (const button of document.querySelectorAll("button")) {
    button.addEventListener("click", () => clicked.push(button.id));
  }
  return clicked;
}

describe("the keys of a question's card", () => {
  it("presses Discuss on the open card, never on one behind it", () => {
    const clicked = cards();
    expect(pressOnCard("discuss")).toBe(true);
    expect(clicked).toEqual(["discuss"]);
  });

  it("unfolds the other answers with Choose another", () => {
    const clicked = cards();
    expect(pressOnCard("choose")).toBe(true);
    expect(clicked).toEqual(["choose"]);
  });

  it("moves focus to the chosen answer when the other answers already show, and folds nothing", () => {
    const clicked = cards(true);
    expect(pressOnCard("choose")).toBe(true);
    expect(clicked).toEqual([]);
    expect(document.activeElement?.id).toBe("chosen");
  });

  it("says when the open card has no such button", () => {
    document.body.innerHTML = `<main id="main"><article class="decision"><button class="btn-primary">Allow</button></article></main>`;
    expect(pressOnCard("choose")).toBe(false);
  });
});
