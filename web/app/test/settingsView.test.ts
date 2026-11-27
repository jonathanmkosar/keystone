// The proof of P11-04, for the screen's side: every setting of the registry
// has a row that shows its name and its one short line, gives its whole
// explanation once opened, and can be reached and found. It reads the real
// settings/*.json, as the route does, so a setting another lane adds is
// held to the same. Run by Node's own test runner (`npm test` in web/app,
// and the `settings at a glance` check in tools/verify; decision 0501).

import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import {
  controlOf,
  formatHint,
  found,
  groupsOf,
  headingOf,
  isChanged,
  keeps,
  moreOf,
  optionText,
  rowOf,
  shortLineOf,
  titleOf,
  valueText,
  type Entry,
} from "../src/settingsView.ts";

type InFile = { key: string; title?: string; summary?: string; type: string; default: string; options?: string[]; scope: string; description: string };

/** The registry as GET /api/v1/settings gives it to a person who has changed nothing. */
function registry(): Entry[] {
  const dir = fileURLToPath(new URL("../../../settings/", import.meta.url));
  const all: Entry[] = [];
  for (const name of readdirSync(dir).filter((n) => n.endsWith(".json")).sort()) {
    const file = JSON.parse(readFileSync(dir + name, "utf8")) as { settings: InFile[] };
    for (const s of file.settings) {
      all.push({ ...s, options: s.options ?? [], value: s.default, overridden: false, changeable: s.scope === "tenant" });
    }
  }
  return all;
}

const reg = registry();
const none = { query: "", section: "", onlyChanged: false };

function entry(over: Partial<Entry>): Entry {
  return { key: "a.b_c", description: "The first sentence. The second.", type: "int", default: "3", options: [], scope: "tenant", value: "3", overridden: false, changeable: true, ...over };
}

test("the registry was read", () => {
  assert.ok(reg.length > 300, `only ${reg.length} settings were read`);
  assert.ok(reg.some((s) => s.changeable) && reg.some((s) => !s.changeable));
});

test("every setting's row shows its own name and its one short line", () => {
  for (const s of reg) {
    const row = rowOf(s);
    assert.equal(row.title, s.title, `${s.key}: the row's name is the registry's title`);
    assert.equal(row.line, s.summary, `${s.key}: the row's line is the registry's summary`);
    assert.ok(row.title && row.line, `${s.key} has a name and a line`);
    assert.ok([...row.line].length <= 80, `${s.key}: the short line is short`);
    assert.ok(!row.title.includes("_") && !row.line.includes(s.key), `${s.key}: no key in the reading line`);
    // The long text is one step away, not in the row.
    assert.ok(!Object.values(row).includes(s.description) || s.description === s.summary, `${s.key}: the row does not carry the description`);
  }
});

test("every setting gives its whole explanation, its default and its key once opened", () => {
  for (const s of reg) {
    const more = moreOf(s);
    if (s.description.trim() === rowOf(s).line.trim()) {
      // The row's short line is the whole description: opened, it is not said twice.
      assert.equal(more.about, "", `${s.key}: a description that is the short line is not repeated`);
    } else {
      assert.equal(more.about, s.description, `${s.key}: the explanation is the registry's description, whole`);
    }
    assert.equal(more.key, s.key);
    assert.ok(more.default !== "", `${s.key}: the default is said, an empty one as a word`);
  }
});

test("nothing in the registry is unreachable: each setting is in one group of one list", () => {
  const mine = reg.filter((s) => s.changeable);
  const install = reg.filter((s) => !s.changeable);
  assert.equal(mine.length + install.length, reg.length);
  for (const list of [mine, install, reg]) {
    const seen = new Map<string, number>();
    for (const g of groupsOf(list)) {
      assert.ok(g.heading.trim() !== "" && g.settings.length > 0, "a group has a heading and settings");
      for (const s of g.settings) {
        seen.set(s.key, (seen.get(s.key) ?? 0) + 1);
        assert.equal(headingOf(s.key), g.heading);
      }
    }
    assert.equal(seen.size, list.length, "every setting is in a group");
    assert.ok([...seen.values()].every((n) => n === 1), "no setting is in two groups");
  }
  // Each can be changed from its row, or says who changes it.
  for (const s of reg) {
    const row = rowOf(s);
    const more = moreOf(s);
    if (s.changeable) {
      assert.ok(row.flips !== more.editable, `${s.key} is flipped in its row or has a box once opened, and not both`);
      assert.equal(more.who, "");
    } else {
      assert.ok(!row.flips && !more.editable && !more.canReset, `${s.key} is not the person's and has no control`);
      assert.ok(more.who.startsWith("The admin sets this"), `${s.key} says who sets it`);
      // Its value is read in the row, or whole once opened: never cut and lost.
      assert.ok(more.text === s.value || (row.value !== "" && !row.value.endsWith("…")), `${s.key} shows its value`);
    }
  }
});

test("headings are plain words in a steady order", () => {
  const headings = groupsOf(reg).map((g) => g.heading);
  assert.equal(new Set(headings).size, headings.length, "no heading twice");
  for (const h of headings) assert.match(h, /^[A-Z][A-Za-z -]*$/, `"${h}" is plain words`);
  assert.equal(headings[0], "Appearance");
  // The same headings, in the same order, for a part of the list.
  const part = groupsOf(reg.filter((s) => s.changeable)).map((g) => g.heading);
  assert.deepEqual(part, headings.filter((h) => part.includes(h)));
  // A key no prefix covers still gets a heading, after the known ones.
  assert.equal(headingOf("zebra_crossing.wait"), "Zebra crossing");
  const withNew = groupsOf([...reg, entry({ key: "zebra_crossing.wait" })]).map((g) => g.heading);
  assert.equal(withNew[withNew.length - 1], "Zebra crossing");
  // A new setting under a known prefix lands under that prefix's heading.
  assert.equal(headingOf("agents.task_something_new"), headingOf("agents.task_budget_usd"));
});

test("the search finds every setting by its name, its short line and its key", () => {
  for (const s of reg) {
    assert.ok(found(s, s.title!), `${s.key} is found by its name`);
    assert.ok(found(s, s.title!.toUpperCase()), `${s.key} is found in any case`);
    assert.ok(found(s, s.summary!.replace(/[.,;]/g, " ")), `${s.key} is found by the words of its short line`);
    assert.ok(found(s, s.key), `${s.key} is found by its key`);
    assert.ok(found(s, s.key.replace(/[._]/g, " ")), `${s.key} is found by its key typed with spaces`);
    assert.ok(keeps(s, none), `${s.key} is kept with no filter`);
    assert.ok(keeps(s, { ...none, section: headingOf(s.key) }), `${s.key} is kept by its own section`);
    assert.ok(!keeps(s, { ...none, section: "No such section" }));
    assert.ok(!found(s, "qqzzxx"));
  }
  const theme = reg.find((s) => s.key === "appearance.theme")!;
  assert.ok(found(theme, "  dark   device "), "every word typed must match, in any order");
  assert.ok(!found(theme, "dark budget"));
});

test("the Changed filter keeps only what the person changed", () => {
  const changed = entry({ value: "4", overridden: true });
  assert.ok(keeps(changed, { ...none, onlyChanged: true }));
  assert.ok(!keeps(entry({}), { ...none, onlyChanged: true }));
  assert.ok(rowOf(changed).changed && moreOf(changed).canReset);
  assert.ok(!rowOf(entry({})).changed && !moreOf(entry({})).canReset);
  // A stored value that equals the default reads as the default, with no mark.
  const same = entry({ overridden: true });
  assert.ok(!isChanged(same) && !rowOf(same).changed && !moreOf(same).canReset && !keeps(same, { ...none, onlyChanged: true }));
  // A changed value that is not the person's to change cannot be reset by them.
  assert.ok(!moreOf(entry({ value: "4", overridden: true, changeable: false, scope: "tenant-admin" })).canReset);
});

test("a setting added with no title and no summary still has a name and a line", () => {
  const bare = entry({ key: "chats.search_max_results", description: "The most each run may spend, 0.5 by default (see docs/x.md). More follows here." });
  assert.equal(titleOf(bare), "Search max results");
  assert.equal(shortLineOf(bare), "The most each run may spend, 0.5 by default (see docs/x.md).");
  assert.equal(rowOf(bare).title, "Search max results");
  assert.equal(shortLineOf(entry({ description: "One sentence with no stop" })), "One sentence with no stop");
  assert.equal(titleOf(entry({ title: "  " })), "B c");
});

test("an opened row does not say its short line twice", () => {
  const short = entry({ summary: "The most chats one person may have.", description: "The most chats one person may have." });
  assert.equal(moreOf(short).about, "", "a description that is the short line is not repeated");
  assert.equal(moreOf(entry({ description: "One sentence with no stop" })).about, "", "nor one that became the short line");
  const long = entry({ summary: "The most chats one person may have.", description: "The most chats one person may have. More follows." });
  assert.equal(moreOf(long).about, long.description, "a longer description is given whole");
  // The registry has such settings, so the rule is used and not only tested.
  assert.ok(reg.some((s) => moreOf(s).about === ""), "some setting's description is its short line");
});

test("the control is chosen by the type and the options alone", () => {
  assert.equal(controlOf(entry({ type: "bool", default: "true", value: "true" })), "switch");
  assert.equal(controlOf(entry({ type: "string", options: ["a", "b"], default: "a", value: "a" })), "select");
  assert.equal(controlOf(entry({ type: "int" })), "number");
  assert.equal(controlOf(entry({ type: "float", default: "0.5", value: "0.5" })), "number");
  assert.equal(controlOf(entry({ type: "duration", default: "5m", value: "5m" })), "duration");
  assert.equal(controlOf(entry({ type: "string", default: "", value: "" })), "text");
  assert.equal(controlOf(entry({ type: "string", default: "x".repeat(61), value: "" })), "long-text");
  assert.equal(controlOf(entry({ type: "string", default: "", value: "two\nlines" })), "long-text");
  // Every setting a person may change has one of them, so none is listed with nothing to change it with.
  for (const s of reg.filter((s) => s.changeable)) {
    assert.ok(["switch", "select", "number", "duration", "text", "long-text"].includes(controlOf(s)), s.key);
    assert.ok(["bool", "int", "float", "string", "duration"].includes(s.type), `${s.key} has a type the screen knows`);
  }
  assert.ok(rowOf(entry({ type: "bool", default: "true", value: "true" })).flips);
  assert.ok(!rowOf(entry({ type: "bool", default: "true", value: "true", changeable: false, scope: "install" })).flips, "a switch that is not the person's is words, not a switch");
});

test("a value reads plainly in a row", () => {
  const bool = entry({ type: "bool" });
  assert.equal(valueText(bool, "true"), "On");
  assert.equal(valueText(bool, "false"), "Off");
  assert.equal(valueText(entry({ type: "string" }), ""), "Empty");
  assert.equal(valueText(entry({ type: "string" }), "  two\n lines "), "two lines");
  const long = valueText(entry({ type: "string" }), "x".repeat(100), 28);
  assert.equal([...long].length, 28);
  assert.ok(long.endsWith("…"));
  assert.equal(rowOf(entry({ value: "12000" })).value, "12000");
  const period = entry({ type: "string", options: ["this_month", "last_7_days"], default: "this_month", value: "last_7_days" });
  assert.equal(rowOf(period).value, "Last 7 days");
  assert.equal(moreOf(period).default, "This month");
  assert.equal(optionText("built-in"), "Built-in");
  assert.equal(rowOf(entry({ type: "string", default: "x".repeat(100), value: "x".repeat(100) })).value, "", "a long text is read once its row is opened");
  assert.equal(formatHint(entry({ type: "duration" })), "A length of time, such as 30s, 5m or 1h.");
  assert.equal(formatHint(entry({ type: "string", options: ["a"] })), "");
  assert.equal(moreOf(entry({ changeable: false, scope: "install" })).hint, "", "no hint on a box that is not there");
});
