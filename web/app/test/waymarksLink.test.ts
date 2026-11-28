import { test } from "node:test";
import assert from "node:assert/strict";
import { waymarksLink } from "../src/waymarksLink.ts";

const on = { key: "knowledge.adapter", value: "waymarks" };

test("the link opens the web address when there is one", () => {
  assert.equal(waymarksLink([on, { key: "knowledge.waymarks_address", value: "http://127.0.0.1:7878" }, { key: "knowledge.waymarks_web_address", value: "https://wiki.example.com" }]), "https://wiki.example.com/");
});

test("without a web address the link opens the Waymarks address", () => {
  assert.equal(waymarksLink([on, { key: "knowledge.waymarks_address", value: "http://127.0.0.1:7878" }]), "http://127.0.0.1:7878/");
});

test("no link while Waymarks is not the knowledge store, or with no address", () => {
  assert.equal(waymarksLink([{ key: "knowledge.adapter", value: "builtin" }, { key: "knowledge.waymarks_address", value: "http://127.0.0.1:7878" }]), "");
  assert.equal(waymarksLink([on]), "");
});

test("only an http or https address with no name or password is linked", () => {
  for (const value of ["javascript:alert(1)", "data:text/html,hi", "file:///c:/x", "https://someone:secret@wiki.example.com", "not an address"]) {
    assert.equal(waymarksLink([on, { key: "knowledge.waymarks_web_address", value }]), "", value);
  }
});
