// The admin's router policy on the screen (P14-05): greyed models, the
// hidden rows while Auto is required, the new chat's policy and the words
// of a refused message. Run by Node's own test runner (`npm test` in
// web/app, decision 0501).

import assert from "node:assert/strict";
import { test } from "node:test";
import { allowedByHand, allowedNames, autoRequired, autoRequiredNote, newChatPolicy, notAllowedWhy, policyProblem } from "../src/policyWords.ts";

const models = [{ name: "house-medium" }, { name: "house-small" }, { name: "paid-large" }];
const ladder = [{ name: "house-small" }, { name: "paid-large" }];

test("a model outside the allowed list may not be picked by hand", () => {
  const choice = { models, ladder, may_switch_off: true, allowed: ["house-small"] };
  assert.equal(allowedByHand(choice, "house-small"), true);
  assert.equal(allowedByHand(choice, "paid-large"), false);
  // An older server sends no list: every model.
  assert.equal(allowedByHand({ models }, "paid-large"), true);
  assert.equal(allowedByHand(undefined, "paid-large"), true);
  assert.match(notAllowedWhy, /admin/);
});

test("Auto is required only while the person may not switch it off and has a ladder", () => {
  assert.equal(autoRequired({ models, ladder, may_switch_off: false }), true);
  assert.equal(autoRequired({ models, ladder, may_switch_off: true }), false);
  assert.equal(autoRequired({ models, ladder: [], may_switch_off: false }), false);
  assert.equal(autoRequired({ models, ladder }), false);
  assert.match(autoRequiredNote, /Auto/);
});

test("the new chat works the policy out from the two settings", () => {
  const names = models.map((m) => m.name);
  assert.deepEqual(allowedNames(" house-small, ,paid-large,house-small "), ["house-small", "paid-large"]);
  assert.deepEqual(newChatPolicy(names, "", "true", 2), { allowed: names, may_switch_off: true });
  assert.deepEqual(newChatPolicy(names, "paid-large, not-mine", "false", 2), { allowed: ["paid-large"], may_switch_off: false });
  // With no ladder there is no Auto to stay on.
  assert.equal(newChatPolicy(names, "", "false", 0).may_switch_off, true);
});

test("a refused message says why, in the server's words or Keystone's own", () => {
  assert.equal(policyProblem("model_not_allowed", "your admin has not allowed Paid Large to be picked by hand"), "Your admin has not allowed Paid Large to be picked by hand.");
  assert.match(policyProblem("auto_required", ""), /on Auto/);
  assert.match(policyProblem("model_not_allowed", ""), /Pick another model/);
  assert.equal(policyProblem("queue_full", "anything"), "");
});
