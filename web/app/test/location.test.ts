// Location per message (P10-06): the composer control's pure part, with a
// stand-in geolocation, so no real position is ever read. The coordinates
// are invented: a point in the open Atlantic.
// Run by Node's own test runner (`npm test` in web/app, decision 0501).

import assert from "node:assert/strict";
import { test } from "node:test";
import { askPosition, availability, chipText, clampDecimals, distanceWords, durationMs, failureWords, hint, readLocationSettings, roundTo, rounded, sentOf, words, type GeoLike } from "../src/location.ts";

const settings = { on: true, decimals: 2, timeoutMs: 10_000 };

/** A geolocation that answers each ask as it is told, and keeps what it was asked with. */
function standIn(answer: { at?: [number, number]; code?: number; throws?: boolean }) {
  const asks: unknown[] = [];
  const geo: GeoLike = {
    getCurrentPosition(found, failed, options) {
      asks.push(options);
      if (answer.throws) throw new Error("no");
      if (answer.at) found({ coords: { latitude: answer.at[0], longitude: answer.at[1] } });
      else failed({ code: answer.code ?? 2 });
    },
  };
  return { geo, asks };
}

test("a position is rounded to its decimals, with no negative zero", () => {
  assert.equal(roundTo(12.345678, 2), 12.35);
  assert.equal(roundTo(-45.678912, 2), -45.68);
  assert.equal(roundTo(12.345678, 1), 12.3);
  assert.equal(roundTo(12.345678, 3), 12.346);
  assert.equal(roundTo(12.345678, 4), 12.3457);
  assert.ok(Object.is(roundTo(-0.004, 2), 0));
  assert.deepEqual(rounded(12.345678, -45.678912, 2), { latitude: 12.35, longitude: -45.68, decimals: 2 });
});

test("the decimals stay between one and four", () => {
  assert.equal(clampDecimals(0), 1);
  assert.equal(clampDecimals(9), 4);
  assert.equal(clampDecimals(Number.NaN), 2);
  assert.equal(roundTo(12.345678, 9), 12.3457);
});

test("the distance and the chip say how far it was rounded", () => {
  assert.deepEqual([1, 2, 3, 4].map(distanceWords), ["11 km", "1 km", "110 m", "11 m"]);
  assert.equal(chipText(2), "Location: about 1 km");
  assert.match(hint(2), /about 1 km/);
  assert.match(hint(2), /this message only/);
  assert.match(hint(2), /hosted models included/);
});

test("the settings are read from the registry's list", () => {
  const list = [
    { key: "location.share", value: "true" },
    { key: "location.decimals", value: "3" },
    { key: "location.timeout", value: "10s" },
  ];
  assert.deepEqual(readLocationSettings(list), { on: true, decimals: 3, timeoutMs: 10_000 });
  assert.equal(readLocationSettings(list.slice(1)), undefined);
  assert.equal(readLocationSettings([...list.slice(0, 2), { key: "location.timeout", value: "soon" }]), undefined);
  assert.equal(durationMs("1m30s"), 90_000);
  assert.equal(durationMs("0"), 0);
});

test("a tap gives the rounded position and nothing finer", async () => {
  const { geo, asks } = standIn({ at: [12.345678, -45.678912] });
  const r = await askPosition(geo, true, settings);
  assert.deepEqual(r, { ok: true, place: { latitude: 12.35, longitude: -45.68, decimals: 2 } });
  assert.deepEqual(asks, [{ enableHighAccuracy: false, timeout: 10_000, maximumAge: 0 }]);
  assert.deepEqual(sentOf(r.ok ? r.place : undefined), { latitude: 12.35, longitude: -45.68 });
  assert.equal(sentOf(undefined), undefined);
});

test("a refusal, no position and a timeout each have plain words", async () => {
  assert.deepEqual(await askPosition(standIn({ code: 1 }).geo, true, settings), { ok: false, problem: words.denied });
  assert.deepEqual(await askPosition(standIn({ code: 2 }).geo, true, settings), { ok: false, problem: words.unavailable });
  assert.deepEqual(await askPosition(standIn({ code: 3 }).geo, true, settings), { ok: false, problem: words.timeout });
  assert.deepEqual(await askPosition(standIn({ at: [Number.NaN, 0] }).geo, true, settings), { ok: false, problem: words.unavailable });
  assert.deepEqual(await askPosition(standIn({ throws: true }).geo, true, settings), { ok: false, problem: words.unsupported });
  assert.equal(failureWords(1), words.denied);
});

test("a browser without geolocation, and a page that is not secure, never ask", async () => {
  const { geo, asks } = standIn({ at: [12.3, -45.6] });
  assert.deepEqual(await askPosition(geo, false, settings), { ok: false, problem: words.insecure });
  assert.equal(asks.length, 0, "a page that is not secure asked the browser");
  assert.deepEqual(await askPosition(undefined, true, settings), { ok: false, problem: words.unsupported });
  assert.deepEqual(availability(false, geo), { available: false, reason: words.insecure });
  assert.deepEqual(availability(true, undefined), { available: false, reason: words.unsupported });
  assert.deepEqual(availability(true, geo), { available: true, reason: "" });
});

test("a wait of nothing waits as long as the browser does", async () => {
  const { geo, asks } = standIn({ at: [1, 2] });
  await askPosition(geo, true, { ...settings, timeoutMs: 0 });
  assert.deepEqual(asks, [{ enableHighAccuracy: false, timeout: Infinity, maximumAge: 0 }]);
});
