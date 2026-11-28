import { test } from "node:test";
import assert from "node:assert/strict";
import { workLabel, type WorkEntry } from "../src/workBox.ts";

const search = (over: Partial<Extract<WorkEntry, { kind: "tool" }>> = {}): WorkEntry => ({
  kind: "tool",
  running: false,
  line: { running: "Searching the web for hedges", asked: "Search the web for hedges", done: "Searched the web for hedges" },
  ...over,
});

test("while the reply runs, the line is what is happening now", () => {
  assert.equal(workLabel([{ kind: "thinking", running: true }], true), "Thinking");
  assert.equal(workLabel([{ kind: "thinking", running: false, seconds: 3 }, search({ running: true })], true), "Searching the web for hedges");
  assert.equal(workLabel([search({ running: true, waiting: true })], true), "Search the web for hedges: waiting for you");
});

test("once done, a summary", () => {
  assert.equal(workLabel([{ kind: "thinking", running: false, seconds: 4.2 }], false), "Thought for 4 seconds");
  assert.equal(workLabel([{ kind: "thinking", running: false }], false), "Thought");
  assert.equal(workLabel([search()], false), "Searched the web for hedges");
  assert.equal(workLabel([{ kind: "thinking", running: false, seconds: 1 }, search()], false), "Thought for 1 second, searched the web for hedges");
  assert.equal(workLabel([{ kind: "thinking", running: false, seconds: 2 }, search(), search(), { kind: "thinking", running: false, seconds: 3 }], false), "Thought for 5 seconds, used 2 tools");
  assert.equal(workLabel([search(), search({ failed: true }), search()], false), "Used 3 tools (1 failed)");
  assert.equal(workLabel([search({ failed: true })], false), "Search the web for hedges");
});

test("a stopped reply's running step reads as done", () => {
  assert.equal(workLabel([{ kind: "thinking", running: true }], false), "Thought");
});
