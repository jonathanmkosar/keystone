import { test } from "node:test";
import assert from "node:assert/strict";
import { capShare, reserveShare, resets } from "../src/usageShare.ts";

const cap = { month: "2026-11", spent_micros: 0, limit_micros: 0, warn_percent: 80, warn_at_micros: 0, left_micros: 0, state: "ok" };

test("the ring fills as the month's spend nears the cap", () => {
  assert.deepEqual(capShare({ ...cap, spent_micros: 5_000_000, limit_micros: 20_000_000 }), { share: 0.25, tone: "ok" });
  assert.deepEqual(capShare({ ...cap, spent_micros: 17_000_000, limit_micros: 20_000_000, state: "warning" }), { share: 0.85, tone: "warn" });
  assert.deepEqual(capShare({ ...cap, spent_micros: 25_000_000, limit_micros: 20_000_000, state: "reached" }), { share: 1, tone: "bad" });
});

test("a cap that does not read is a full red ring", () => {
  assert.deepEqual(capShare({ ...cap, state: "unread" }), { share: 1, tone: "bad" });
});

test("with no cap set, any spend fills the ring and none leaves it empty", () => {
  assert.equal(capShare(cap).share, 0);
  assert.equal(capShare({ ...cap, spent_micros: 1 }).share, 1);
});

test("the memory reserve's share, or nothing when it does not read", () => {
  assert.deepEqual(reserveShare({ month: "2026-11", spent_micros: 1_000_000, limit_micros: 4_000_000, left_micros: 3_000_000 }), { share: 0.25, tone: "ok" });
  assert.equal(reserveShare({ month: "2026-11", spent_micros: 0, limit_micros: 0, left_micros: 0, problem: "unread" }), null);
});

test("the cap starts again on the first of the next month, December after November and January after December", () => {
  assert.equal(resets("2026-11"), "Tue, Dec 1");
  assert.equal(resets("2026-12"), "Fri, Jan 1");
  assert.equal(resets("soon"), "");
});
