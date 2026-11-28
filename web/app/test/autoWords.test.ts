// The words of Auto on the screen (P14-04): the reply's footnote, the
// notice, the picker's sentences and when a stronger model is offered.
// Run by Node's own test runner (`npm test` in web/app, decision 0501).

import assert from "node:assert/strict";
import { test } from "node:test";
import { autoFoot, autoLabel, autoLine, autoPick, ladderNames, routeLine, routeNotice, routeReason, strongerOffered, strongerProblem } from "../src/autoWords.ts";
import { shortModelName } from "../src/composerSheet.ts";

const small = { auto: true, model: "recorded-small", label: "Recorded turns, small (test data)", step: 1, steps: 3, effort: "low", reason: "Recorded turns, small (test data), effort low, decision 0.92" };

test("the footnote names the model, the effort and the step", () => {
  assert.equal(routeLine(small), "Auto: Recorded turns, small (test data) · effort low · step 1 of 3");
  assert.equal(routeLine({ auto: true, model: "house-chat" }), "Auto: house-chat");
  assert.equal(routeLine({ auto: false, model: "house-chat" }), "");
  assert.equal(routeLine(undefined), "");
});

test("the reason is Keystone's line, and only on Auto", () => {
  assert.equal(routeReason(small), small.reason);
  assert.equal(routeReason({ ...small, auto: false }), "");
});

test("the notice says the local machine is not answering", () => {
  assert.match(routeNotice({ ...small, notice: "local_unavailable" }), /local machine is not answering/);
  assert.equal(routeNotice(small), "");
});

test("a stronger model is offered below the top step only", () => {
  assert.equal(strongerOffered(small), true);
  assert.equal(strongerOffered({ ...small, step: 3 }), false);
  assert.equal(strongerOffered({ ...small, auto: false }), false);
  assert.equal(strongerOffered(undefined), false);
});

test("the ladder's names drop their size limits", () => {
  assert.deepEqual(ladderNames("house-chat, small-hosted<=100000, mid-hosted ,, large-hosted"), ["house-chat", "small-hosted", "mid-hosted", "large-hosted"]);
  assert.deepEqual(ladderNames(""), []);
  assert.deepEqual(ladderNames("one, one"), ["one"]);
});

test("Auto's pick cannot be a registry name, and the chip keeps its name on a phone", () => {
  assert.ok(autoPick.includes(":"));
  assert.equal(shortModelName(autoLabel), "Auto");
  assert.match(autoLine(3), /from 3 models/);
});

test("the picker's foot says what a pick does to Auto", () => {
  assert.equal(autoFoot({ auto: false, pickedAuto: true, pickedModel: false, pending: true, isNew: false }), "Sent with your next message: the chat goes on Auto.");
  assert.equal(autoFoot({ auto: true, pickedAuto: false, pickedModel: true, pending: true, isNew: false }), "Sent with your next message. It takes the chat off Auto.");
  assert.match(autoFoot({ auto: true, pickedAuto: false, pickedModel: false, pending: false, isNew: false, lastStep: "Cloud chat" }), /The last message used Cloud chat\.$/);
  assert.equal(autoFoot({ auto: false, pickedAuto: false, pickedModel: false, pending: false, isNew: false }), "");
});

test("a refused stronger model says why", () => {
  assert.equal(strongerProblem("no_stronger", "no step above the one that answered may take this message"), "No step above the one that answered may take this message.");
  assert.equal(strongerProblem("no_stronger", ""), "No stronger model may write this reply.");
});
