// The words of the phone's sheets (decision 0590): what each row behind the
// composer's + and the header's ... says, and the model chip's short name.
// Run by Node's own test runner (`npm test` in web/app, decision 0501).

import assert from "node:assert/strict";
import { test } from "node:test";
import { attachedState, composerExpanded, connectorsState, filesState, firstName, footnote, phoneGreeting, phonePlaceholder, phoneQuery, projectState, rowLabel, saidActionsShown, shortModelName, styleState, switchState } from "../src/composerSheet.ts";

test("the model chip drops what is in brackets at the end of a label", () => {
  assert.equal(shortModelName("Qwen 3.8 27B (Mac mini)"), "Qwen 3.8 27B");
  assert.equal(shortModelName("Recorded turns (test data)"), "Recorded turns");
  assert.equal(shortModelName("Harbour model [local] (office)"), "Harbour model");
  assert.equal(shortModelName("  Plain model  "), "Plain model");
});

test("the model chip drops what follows a middle dot", () => {
  assert.equal(shortModelName("Recorded turns · recorded test replies"), "Recorded turns");
});

test("the model chip keeps the whole label when nothing would be left", () => {
  assert.equal(shortModelName("(unnamed)"), "(unnamed)");
  assert.equal(shortModelName(""), "");
});

test("brackets inside a label stay", () => {
  assert.equal(shortModelName("Model (beta) four"), "Model (beta) four");
});

test("a row is read out by its name and its state", () => {
  assert.equal(rowLabel("Style", "Concise"), "Style: Concise");
  assert.equal(rowLabel("Hand off", ""), "Hand off");
});

test("the files row counts what is ready, and says nothing when there is none", () => {
  assert.equal(filesState(0), "");
  assert.equal(filesState(1), "1 ready");
  assert.equal(filesState(3), "3 ready");
});

test("the attach a chat row counts the attached chats against the most", () => {
  assert.equal(attachedState(0, 5), "none");
  assert.equal(attachedState(2, 5), "2 attached");
  assert.equal(attachedState(5, 5), "5 of 5, the most");
  assert.equal(attachedState(1, 0), "1 attached");
});

test("a switch reads on or off", () => {
  assert.equal(switchState(true), "on");
  assert.equal(switchState(false), "off");
  assert.equal(rowLabel("Research", switchState(true)), "Research: on");
});

test("the connectors row says how many are on", () => {
  assert.equal(connectorsState(1, 1), "1 on");
  assert.equal(connectorsState(1, 3), "1 of 3 on");
  assert.equal(connectorsState(0, 2), "0 of 2 on");
  assert.equal(connectorsState(0, 0), "none");
  assert.equal(rowLabel("Connectors", connectorsState(1, 1)), "Connectors: 1 on");
});

test("the style and project rows name theirs, or say none", () => {
  assert.equal(styleState("Concise"), "Concise");
  assert.equal(styleState(undefined), "none");
  assert.equal(styleState("  "), "none");
  assert.equal(projectState("Garden"), "Garden");
  assert.equal(projectState(null), "none");
});

test("the phone's layout is the sidebar drawer's width", () => {
  assert.equal(phoneQuery, "(max-width: 760px)");
});

// P10-07: the phone's composer, idle or expanded (decision 0610).

const idle = { focused: false, text: "", files: 0 };

test("an empty composer with no focus is the idle pill", () => {
  assert.equal(composerExpanded(idle), false);
  assert.equal(composerExpanded({ ...idle, dictating: false }), false);
});

test("the focus expands the composer into the card", () => {
  assert.equal(composerExpanded({ ...idle, focused: true }), true);
});

test("text keeps the card when the focus leaves, even text that is only spaces", () => {
  assert.equal(composerExpanded({ ...idle, text: "Tide times for Saturday" }), true);
  assert.equal(composerExpanded({ ...idle, text: "  " }), true);
});

test("a file waiting to go keeps the card", () => {
  assert.equal(composerExpanded({ ...idle, files: 1 }), true);
});

test("dictation keeps the card while it listens", () => {
  assert.equal(composerExpanded({ ...idle, dictating: true }), true);
});

test("the phone's greeting has the first name only", () => {
  assert.equal(firstName("Test Person One"), "Test");
  assert.equal(firstName("  Robin  "), "Robin");
  assert.equal(firstName(""), "");
  assert.equal(phoneGreeting("afternoon", "Test Person One"), "Good afternoon, Test");
  assert.equal(phoneGreeting("morning", " "), "Good morning");
});

test("a person's message waits for a tap on a phone, unless it is pinned", () => {
  assert.equal(saidActionsShown({ phone: true, tapped: false, pinned: false }), false);
  assert.equal(saidActionsShown({ phone: true, tapped: true, pinned: false }), true);
  assert.equal(saidActionsShown({ phone: true, tapped: false, pinned: true }), true);
  assert.equal(saidActionsShown({ phone: false, tapped: false, pinned: false }), true);
});

test("the phone's words are short and keep the footnote", () => {
  assert.equal(phonePlaceholder, "Message Keystone");
  assert.match(footnote, /^Keystone can make mistakes\./);
});
