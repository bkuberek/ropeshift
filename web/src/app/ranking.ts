import type { ReactNode } from "react";

import type { IconName } from "../ui/Icon";

// Entry is one line of the command palette: an action, a view, a record, or a project.
export type Entry = {
  key: string;
  group: "Actions" | "Views" | "Records" | "Projects";
  title: string;
  // id is a record's or a project's identifier, which a person may type exactly.
  id?: string;
  keywords?: string;
  icon?: IconName;
  // meta stands at the line's end: a shortcut, a kind and state, a project.
  meta?: ReactNode;
  // to is where the entry goes, and run what it does instead when it opens something in place.
  to?: string;
  run?: () => void;
};

export type Query = { scope: "all" | "actions" | "records"; words: string[]; text: string };

// parseQuery reads what a person typed: > narrows to actions, # to records, and the words match in any
// order and any case.
export function parseQuery(typed: string): Query {
  const trimmed = typed.trim();
  const scope = trimmed.startsWith(">") ? "actions" : trimmed.startsWith("#") ? "records" : "all";
  const text = scope === "all" ? trimmed : trimmed.slice(1).trim();
  return { scope, words: text.toLowerCase().split(/\s+/).filter(Boolean), text };
}

const wordBreak = /[\s\-_/.:]+/;

// scoreOf says how well an entry holds every word, the lower the better, or null when it does not: an
// exact ID, then an ID the words begin, then a title they begin, then words that each begin a word, then
// words found anywhere.
export function scoreOf(words: string[], entry: Entry): number | null {
  if (words.length === 0) {
    return 0;
  }
  const id = (entry.id ?? "").toLowerCase();
  const title = entry.title.toLowerCase();
  const text = `${id} ${title} ${(entry.keywords ?? "").toLowerCase()}`;
  if (!words.every((word) => text.includes(word))) {
    return null;
  }
  const whole = words.join(" ");
  if (id && id === whole) {
    return 0;
  }
  if (id && id.startsWith(whole)) {
    return 1;
  }
  if (title.startsWith(whole)) {
    return 2;
  }
  const starts = text.split(wordBreak);
  return words.every((word) => starts.some((part) => part.startsWith(word))) ? 3 : 4;
}

export type Sources = { actions: Entry[]; views: Entry[]; records: Entry[]; recent: Entry[]; projects: Entry[] };

export type Group = { name: string; entries: Entry[] };

function ranked(words: string[], entries: Entry[], keepOrder = false): Entry[] {
  const scored = entries.map((entry, at) => ({ entry, at, score: scoreOf(words, entry) })).filter((row) => row.score !== null);
  if (!keepOrder) {
    scored.sort((a, b) => (a.score ?? 0) - (b.score ?? 0) || a.at - b.at);
  }
  return scored.map((row) => row.entry);
}

// arrange lists the palette's groups for what a person typed. With nothing typed it shows the current
// view's actions, then recent records, then views. A group with nothing in it is left out.
export function arrange(typed: string, sources: Sources): Group[] {
  const query = parseQuery(typed);
  const groups: Group[] =
    query.words.length === 0 && query.scope === "all"
      ? [
          { name: "Actions", entries: sources.actions },
          { name: "Recent", entries: sources.recent },
          { name: "Views", entries: sources.views },
        ]
      : [
          { name: "Actions", entries: query.scope === "records" ? [] : ranked(query.words, sources.actions) },
          { name: "Views", entries: query.scope === "all" ? ranked(query.words, sources.views) : [] },
          { name: "Records", entries: query.scope === "actions" ? [] : ranked(query.words, query.words.length ? sources.records : sources.recent, true) },
          { name: "Projects", entries: query.scope === "all" ? ranked(query.words, sources.projects) : [] },
        ];
  return groups.filter((group) => group.entries.length > 0);
}
