// The proof of P10-01: dictated words land in the composer and nothing is
// sent without the person's send action. Run by Node's own test runner
// (`npm test` in web/app, and the `dictation` check in tools/verify), with a
// fake recognizer and a fake clock in place of the browser's (decision 0501).

import assert from "node:assert/strict";
import { test } from "node:test";
import {
  createDictation,
  durationMs,
  durationWords,
  errorWords,
  insertAt,
  readVoiceSettings,
  words,
  type DictationView,
  type Recognizer,
  type ResultEvent,
  type VoiceSettings,
} from "../src/dictation.ts";

/** A recognizer that does what the test says, and remembers what it was asked. */
class FakeRecognizer implements Recognizer {
  lang = "";
  continuous = false;
  interimResults = false;
  maxAlternatives = 0;
  onstart: (() => void) | null = null;
  onresult: ((e: ResultEvent) => void) | null = null;
  onerror: ((e: { error: string }) => void) | null = null;
  onend: (() => void) | null = null;
  calls: string[] = [];
  failStart = false;
  results: { isFinal: boolean; text: string }[] = [];
  start() {
    this.calls.push("start");
    if (this.failStart) throw new Error("InvalidStateError");
  }
  stop() {
    this.calls.push("stop");
  }
  abort() {
    this.calls.push("abort");
  }
  began() {
    this.onstart?.();
  }
  /** Sets result i of the session and sends the whole list, as the browser does. */
  hear(i: number, text: string, isFinal: boolean) {
    this.results[i] = { isFinal, text };
    const list = this.results.map((r) => Object.assign([{ transcript: r.text }], { isFinal: r.isFinal }));
    this.onresult?.({ resultIndex: i, results: list });
  }
  fail(error: string) {
    this.onerror?.({ error });
  }
  ended() {
    this.onend?.();
  }
}

/** A clock whose time moves only when the test says. */
class FakeClock {
  now = 0;
  timers: { at: number; fn: () => void; live: boolean }[] = [];
  after(ms: number, fn: () => void) {
    const t = { at: this.now + ms, fn, live: true };
    this.timers.push(t);
    return () => {
      t.live = false;
    };
  }
  pass(ms: number) {
    this.now += ms;
    for (const t of this.timers) {
      if (t.live && t.at <= this.now) {
        t.live = false;
        t.fn();
      }
    }
  }
}

const settings: VoiceSettings = { dictation: true, language: "", interim: true, maxListenMs: 120_000, finishWaitMs: 5_000 };

/**
 * The composer as Composer.tsx is, without React: the box's text, the
 * insertion point, the send action that asks dictation first, and what was
 * sent. Dictation is given only `insert`.
 */
function composer(opts: { text?: string; cursor?: number; settings?: Partial<VoiceSettings>; make?: "none" | "fake"; secure?: boolean; browserLanguage?: string } = {}) {
  const rec = new FakeRecognizer();
  const clock = new FakeClock();
  const box = { text: opts.text ?? "", at: opts.cursor ?? (opts.text ?? "").length, sent: [] as string[], views: [] as DictationView[] };
  const d = createDictation({
    make: opts.make === "none" ? null : () => rec,
    secure: opts.secure ?? true,
    settings: { ...settings, ...opts.settings },
    browserLanguage: opts.browserLanguage ?? "en-GB",
    insert: (w) => {
      const r = insertAt(box.text, box.at, w);
      box.text = r.text;
      box.at = r.at;
    },
    changed: (v) => box.views.push(v),
    clock,
  });
  const send = () => {
    if (d.holdsSend()) return; // the first line of Composer's send()
    if (box.text.trim() === "") return;
    box.sent.push(box.text.trim());
    box.text = "";
  };
  return { d, rec, clock, box, send };
}

test("dictated words land in the box at the cursor, in order, and wait there", () => {
  const { d, rec, box } = composer({ text: "Dear team,", cursor: 10 });
  d.toggle();
  assert.equal(d.view().phase, "listening");
  assert.equal(d.view().message, words.waiting);
  rec.began();
  assert.equal(d.view().started, true);
  rec.hear(0, "the ferry", false);
  assert.equal(d.view().interim, "the ferry");
  assert.equal(box.text, "Dear team,", "words still being heard are not put in the box");
  rec.hear(0, "the ferry leaves at nine", true);
  assert.equal(box.text, "Dear team, the ferry leaves at nine");
  assert.equal(d.view().interim, "");
  rec.hear(1, " on Sundays", false);
  rec.hear(1, " on Sundays", true);
  assert.equal(box.text, "Dear team, the ferry leaves at nine on Sundays");
  d.toggle(); // the microphone again: finish
  assert.equal(d.view().phase, "finishing");
  assert.deepEqual(rec.calls, ["start", "stop"]);
  rec.ended();
  assert.equal(d.view().phase, "idle");
  assert.equal(d.view().message, words.review);
  assert.deepEqual(box.sent, [], "nothing is sent when dictation stops");
  assert.equal(box.text, "Dear team, the ferry leaves at nine on Sundays");
});

test("words go in at the cursor without removing the text around it", () => {
  const { d, rec, box } = composer({ text: "Meet at the harbour.", cursor: 7 });
  d.start();
  rec.hear(0, "noon", true);
  rec.hear(1, "sharp", true);
  assert.equal(box.text, "Meet at noon sharp the harbour.");
  assert.equal(box.at, 18);
});

test("the person's send action, not dictation, sends: Enter or Send while listening finishes for review", () => {
  const { d, rec, box, send } = composer();
  d.start();
  rec.began();
  rec.hear(0, "book two tickets", true);
  rec.hear(1, "for Sunday", false);
  send(); // Enter, or the send button, during listening
  assert.deepEqual(box.sent, []);
  assert.equal(d.view().phase, "finishing");
  send(); // again while finishing
  assert.deepEqual(box.sent, []);
  rec.hear(1, "for Sunday", true);
  rec.ended();
  assert.equal(d.view().phase, "idle");
  assert.deepEqual(box.sent, [], "nothing is sent when the last words arrive");
  assert.equal(box.text, "book two tickets for Sunday");
  send(); // the person, having read it
  assert.deepEqual(box.sent, ["book two tickets for Sunday"]);
});

test("silence, an error and the time limit end dictation and send nothing", () => {
  for (const ending of ["silence", "error", "limit"] as const) {
    const { d, rec, clock, box } = composer();
    d.start();
    rec.began();
    rec.hear(0, "pack the lantern", true);
    if (ending === "silence") rec.ended();
    if (ending === "error") rec.fail("network");
    if (ending === "limit") {
      clock.pass(120_000);
      assert.equal(d.view().phase, "finishing", "the time limit finishes");
      assert.deepEqual(rec.calls, ["start", "stop"]);
      rec.ended();
      assert.equal(d.view().message, `${words.maxed(120_000)} ${words.review}`);
    }
    assert.equal(d.view().phase, "idle", ending);
    assert.equal(box.text, "pack the lantern", ending);
    assert.deepEqual(box.sent, [], ending);
  }
});

test("a browser that never finishes is stopped after voice.finish_wait, keeping what was heard", () => {
  const { d, rec, clock, box } = composer();
  d.start();
  rec.hear(0, "the north", true);
  rec.hear(1, "ferry", false);
  d.finish();
  clock.pass(4_999);
  assert.equal(d.view().phase, "finishing");
  clock.pass(1);
  assert.equal(d.view().phase, "idle");
  assert.deepEqual(rec.calls, ["start", "stop", "abort"]);
  assert.equal(box.text, "the north ferry");
  assert.equal(d.view().message, words.review);
  assert.deepEqual(box.sent, []);
});

test("Escape stops at once and keeps the words still being heard", () => {
  const { d, rec, box } = composer();
  assert.equal(d.escape(), false, "Escape does nothing when idle");
  d.start();
  rec.hear(0, "half a", false);
  assert.equal(d.escape(), true);
  assert.equal(d.view().phase, "idle");
  assert.deepEqual(rec.calls, ["start", "abort"]);
  assert.equal(box.text, "half a");
  assert.equal(d.view().message, `${words.stopped} ${words.review}`);
  rec.hear(0, "half a sentence", true); // late events from the stopped session
  rec.ended();
  assert.equal(box.text, "half a");
  assert.deepEqual(box.sent, []);
});

test("typing in the box stops listening and leaves the box as the person left it", () => {
  const { d, rec, box } = composer();
  d.start();
  rec.hear(0, "the beehives", true);
  rec.hear(1, "need", false);
  box.text += " and"; // the person typed
  d.edited();
  assert.equal(d.view().phase, "idle");
  assert.equal(box.text, "the beehives and");
  assert.equal(d.view().message, words.typed);
  assert.deepEqual(rec.calls, ["start", "abort"]);
});

test("each error has its own plain message", () => {
  const cases: [string, string][] = [
    ["not-allowed", "Microphone permission was refused"],
    ["audio-capture", "No microphone is available."],
    ["no-speech", "No speech heard."],
    ["language-not-supported", "cannot transcribe the language “xx-YY”"],
    ["network", "could not be reached"],
    ["service-not-allowed", "blocked speech recognition"],
    ["something-new", "Listening stopped."],
  ];
  for (const [code, said] of cases) {
    const { d, rec } = composer({ settings: { language: "xx-YY" } });
    d.start();
    rec.fail(code);
    assert.equal(d.view().phase, "idle", code);
    assert.ok(d.view().message.includes(said), `${code}: ${d.view().message}`);
    assert.equal(d.view().problem, true, code);
  }
  // No speech after words were heard is the end of speaking, not an error.
  const { d, rec } = composer();
  d.start();
  rec.hear(0, "that is all", true);
  rec.fail("no-speech");
  assert.equal(d.view().message, words.review);
  assert.equal(d.view().problem, false);
});

test("a browser without speech recognition, or a page that is not secure, cannot start", () => {
  const none = composer({ make: "none" });
  assert.equal(none.d.view().available, false);
  assert.equal(none.d.view().reason, words.unsupported);
  none.d.toggle();
  assert.equal(none.d.view().phase, "idle");
  assert.equal(none.d.view().message, words.unsupported);
  const plain = composer({ secure: false });
  assert.equal(plain.d.view().reason, words.insecure);
  plain.d.start();
  assert.deepEqual(plain.rec.calls, [], "no recognizer is started");
});

test("a recognizer that will not start says so", () => {
  const { d, rec } = composer();
  rec.failStart = true;
  d.start();
  assert.equal(d.view().phase, "idle");
  assert.equal(d.view().message, words.couldNotStart);
});

test("the language is the setting's, or the browser's when it is empty; interim words show only when asked", () => {
  const own = composer({ settings: { language: "fr-FR" } });
  own.d.start();
  assert.equal(own.rec.lang, "fr-FR");
  assert.equal(own.rec.continuous, true);
  const theirs = composer({ browserLanguage: "de-DE" });
  theirs.d.start();
  assert.equal(theirs.rec.lang, "de-DE");
  const hidden = composer({ settings: { interim: false } });
  hidden.d.start();
  hidden.rec.hear(0, "quiet words", false);
  assert.equal(hidden.d.view().interim, "");
  assert.equal(hidden.box.text, "");
  hidden.rec.hear(0, "quiet words", true);
  assert.equal(hidden.box.text, "quiet words");
});

test("no time limit when voice.max_listen is 0s", () => {
  const { d, clock } = composer({ settings: { maxListenMs: 0 } });
  d.start();
  clock.pass(3_600_000);
  assert.equal(d.view().phase, "listening");
});

test("the message is cleared once the person moves on, and dispose stops quietly", () => {
  const { d, rec, box } = composer();
  d.start();
  rec.hear(0, "done", true);
  rec.ended();
  assert.equal(d.view().message, words.review);
  d.quiet();
  assert.equal(d.view().message, "");
  d.start();
  const seen = box.views.length;
  d.dispose();
  assert.equal(rec.calls.at(-1), "abort");
  assert.equal(box.views.length, seen, "dispose draws nothing");
});

test("insertAt never removes what was there", () => {
  const texts = ["", "a", "Hello", "Hello ", " x", "one two", "line\nnext"];
  for (const t of texts) {
    for (let at = -1; at <= t.length + 1; at++) {
      const r = insertAt(t, at, "  new words ");
      const p = Math.max(0, Math.min(at, t.length));
      assert.equal(r.text.slice(0, p), t.slice(0, p));
      assert.ok(r.text.endsWith(t.slice(p)));
      assert.ok(r.text.includes("new words"));
      assert.equal(r.text.slice(r.at - "new words".length, r.at), "new words");
    }
  }
  assert.deepEqual(insertAt("abc", 3, "   "), { text: "abc", at: 3 });
});

test("settings are read from the route's list, with Go durations", () => {
  assert.equal(durationMs("2m"), 120_000);
  assert.equal(durationMs("1m30s"), 90_000);
  assert.equal(durationMs("5s"), 5_000);
  assert.equal(durationMs("500ms"), 500);
  assert.equal(durationMs("0s"), 0);
  assert.equal(durationMs("0"), 0);
  assert.equal(durationMs("1.5h"), 5_400_000);
  for (const bad of ["", "5", "-5s", "5 s", "m", "2x"]) assert.equal(durationMs(bad), undefined, bad);
  assert.equal(durationWords(120_000), "2 minutes");
  assert.equal(durationWords(1_000), "1 second");
  assert.equal(durationWords(90_000), "90 seconds");
  const list = [
    { key: "voice.dictation", value: "true" },
    { key: "voice.language", value: " en-AU " },
    { key: "voice.interim", value: "false" },
    { key: "voice.max_listen", value: "90s" },
    { key: "voice.finish_wait", value: "5s" },
    { key: "appearance.theme", value: "dark" },
  ];
  assert.deepEqual(readVoiceSettings(list), { dictation: true, language: "en-AU", interim: false, maxListenMs: 90_000, finishWaitMs: 5_000 });
  assert.equal(readVoiceSettings(list.slice(1)), undefined, "a missing setting draws no microphone");
  assert.equal(readVoiceSettings([...list.slice(0, 3), { key: "voice.max_listen", value: "soon" }, list[4]]), undefined);
  assert.ok(errorWords("language-not-supported", "").includes("that language"));
});
