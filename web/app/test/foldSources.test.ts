import { test } from "node:test";
import assert from "node:assert/strict";
import { foldSources, sourcesLabel } from "../src/foldSources.ts";

test("the importer's list at the end of a reply is folded", () => {
  const text = "Beans like a sunny fence [1].\n\nSources:\n\n- [1] [Beans by the fence](https://gardening.example/beans)\n- [2] <https://gardening.example/soil>";
  const f = foldSources(text);
  assert.ok(f);
  assert.equal(f.count, 2);
  assert.equal(f.before, "Beans like a sunny fence [1].");
  assert.match(f.list, /^- \[1\]/);
  assert.equal(f.after, "");
});

test("a research report's list is folded and its closing line stays", () => {
  const text = "# Hedges\n\nText [1].\n\n## Sources\n\n1. [One](https://a.example/1)\n2. [Two](https://a.example/2) (read as a search result's extract)\n\nKeystone's research mode wrote this from 2 parts.";
  const f = foldSources(text);
  assert.ok(f);
  assert.equal(f.count, 2);
  assert.equal(f.before, "# Hedges\n\nText [1].");
  assert.equal(f.after, "Keystone's research mode wrote this from 2 parts.");
});

test("bold, other words and bare addresses count", () => {
  assert.equal(foldSources("x\n\n**References**\nhttps://a.example/1\nhttps://a.example/2")?.count, 2);
  assert.equal(foldSources("x\n\n**Sources:**\n* one\n* two\n* three")?.count, 3);
  assert.equal(foldSources("x\n\nCitations\n[1] https://a.example")?.count, 1);
});

test("a heading with no list after it, or in a code block, is not folded", () => {
  assert.equal(foldSources("## Sources\n\nThis report cites no source.\n"), null);
  assert.equal(foldSources("```\nSources:\n- [1] x\n```"), null);
  assert.equal(foldSources("The sources were good.\n- a"), null);
  assert.equal(foldSources(""), null);
});

test("an item's indented line carries on the item", () => {
  const f = foldSources("Sources:\n- [1] A page\n  about hedges\n- [2] B\nAfter.");
  assert.ok(f);
  assert.equal(f.count, 2);
  assert.equal(f.after, "After.");
});

test("the label counts", () => {
  assert.equal(sourcesLabel(1), "1 source");
  assert.equal(sourcesLabel(4), "4 sources");
});
