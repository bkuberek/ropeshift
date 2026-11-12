import { describe, expect, it } from "vitest";

import type { Entry } from "./ranking";
import { arrange, parseQuery, scoreOf } from "./ranking";

const entry = (group: Entry["group"], title: string, more: Partial<Entry> = {}): Entry => ({ key: `${group}:${title}`, group, title, ...more });

const actions = [entry("Actions", "Pause fleet"), entry("Actions", "Toggle theme")];
const views = [entry("Views", "Needs you"), entry("Views", "Floor"), entry("Views", "Work orders")];
const projects = [entry("Projects", "Harbor", { id: "harbor" })];
const records = [entry("Records", "Stable cursor pagination", { id: "WO-214" }), entry("Records", "Highlight matched terms", { id: "WO-219" })];
const recent = [entry("Records", "Synonym loader", { id: "WO-217" })];

describe("a query", () => {
  it("narrows to actions with >, and to records with #", () => {
    expect(parseQuery("> pause")).toEqual({ scope: "actions", words: ["pause"], text: "pause" });
    expect(parseQuery("#wo-2")).toEqual({ scope: "records", words: ["wo-2"], text: "wo-2" });
    expect(parseQuery("  Floor  ")).toEqual({ scope: "all", words: ["floor"], text: "Floor" });
  });
});

describe("a match", () => {
  it("ignores case and word order", () => {
    const record = entry("Records", "Stable cursor pagination", { id: "WO-214" });
    expect(scoreOf(parseQuery("PAGINATION stable").words, record)).not.toBeNull();
    expect(scoreOf(parseQuery("cursor wo-214").words, record)).not.toBeNull();
    expect(scoreOf(parseQuery("cursor nothing").words, record)).toBeNull();
  });

  it("ranks an exact ID, then an ID it begins, then a title it begins, then words that begin words, then the rest", () => {
    const exact = scoreOf(["wo-214"], entry("Records", "x", { id: "WO-214" }));
    const prefix = scoreOf(["wo-2"], entry("Records", "x", { id: "WO-214" }));
    const title = scoreOf(["floor"], entry("Views", "Floor plan"));
    const starts = scoreOf(["plan"], entry("Views", "Floor plan"));
    const inside = scoreOf(["loor"], entry("Views", "Floor plan"));
    const order = [exact, prefix, title, starts, inside];
    expect(order.every((score) => score !== null)).toBe(true);
    expect([...order].sort((a, b) => (a ?? 0) - (b ?? 0))).toEqual(order);
  });

  it("finds an entry by its keywords too", () => {
    expect(scoreOf(["shop"], entry("Views", "Floor", { keywords: "shop floor lanes" }))).not.toBeNull();
  });
});

describe("the palette's list", () => {
  it("shows the current view's actions, then recent records, then views, with no query", () => {
    const groups = arrange("", { actions, views, projects, records: [], recent });
    expect(groups.map((group) => group.name)).toEqual(["Actions", "Recent", "Views"]);
    expect(groups[0]?.entries.map((row) => row.title)).toEqual(["Pause fleet", "Toggle theme"]);
  });

  it("groups matches as actions, views, records, then projects", () => {
    const groups = arrange("o", { actions, views, projects, records, recent });
    expect(groups.map((group) => group.name)).toEqual(["Actions", "Views", "Records", "Projects"]);
  });

  it("keeps the search's order for records, which the server ranked", () => {
    const groups = arrange("wo-2", { actions, views, projects, records: [...records].reverse(), recent });
    expect(groups.find((group) => group.name === "Records")?.entries.map((row) => row.id)).toEqual(["WO-219", "WO-214"]);
  });

  it("orders each other group by how well it matches", () => {
    const groups = arrange("work", { actions, views: [entry("Views", "Floor", { keywords: "work lanes" }), entry("Views", "Work orders")], projects, records: [], recent });
    expect(groups[0]?.entries.map((row) => row.title)).toEqual(["Work orders", "Floor"]);
  });

  it("lists only actions after >, and only records after #", () => {
    expect(arrange(">", { actions, views, projects, records, recent }).map((group) => group.name)).toEqual(["Actions"]);
    expect(arrange("# wo", { actions, views, projects, records, recent }).map((group) => group.name)).toEqual(["Records"]);
  });

  it("leaves out a group with nothing in it", () => {
    expect(arrange("zzz", { actions, views, projects, records: [], recent })).toEqual([]);
  });
});
