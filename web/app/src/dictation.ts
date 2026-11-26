// Dictation (P10-01): speech into the composer, for the person to review
// before sending.
//
// This file is the machine. It has no imports, so Node's own test runner
// loads it as it is (web/app/test/dictation.test.ts, decision 0501). The
// browser's recognizer, a clock, the settings and the composer's insert are
// given to it; it calls out only to put final words into the box and to say
// its state changed. It is given no way to send. The composer asks it first
// whenever its send action runs (holdsSend): while dictation runs, Send and
// Enter finish the transcript for review and send nothing.
//
// Borrowed from New Keystone's voice.js: the review-before-send flow, the
// wait for the last words after stopping, and the wording of each error.

/** Where dictation is. */
export type Phase = "idle" | "listening" | "finishing";

/** What the screen draws. */
export type DictationView = {
  /** The browser can turn speech into text on this page. */
  available: boolean;
  /** Why it cannot, for a person. Empty when it can. */
  reason: string;
  phase: Phase;
  /** The browser said it has started listening. */
  started: boolean;
  /** Words still being heard, not yet in the box. Always empty when voice.interim is off. */
  interim: string;
  /** What the person is told: the state, or why it stopped. */
  message: string;
  /** The message is an error. */
  problem: boolean;
};

/** One alternative of a recognition result, as the browser gives it. */
export type Alternative = { readonly transcript: string };

/** One result: final once the browser will not change it again. */
export type RecognitionResult = { readonly isFinal: boolean; readonly length: number; readonly [index: number]: Alternative };

/** The browser's result event: every result of the session so far. */
export type ResultEvent = {
  readonly resultIndex: number;
  readonly results: { readonly length: number; readonly [index: number]: RecognitionResult };
};

/** The browser's error event. */
export type RecognitionError = { readonly error: string };

/**
 * The part of the browser's SpeechRecognition the machine uses. A local
 * transcriber, such as one on the home-network gateway, would be another
 * thing of this shape (decision 0500); none is built.
 */
export interface Recognizer {
  lang: string;
  continuous: boolean;
  interimResults: boolean;
  maxAlternatives: number;
  onstart: (() => void) | null;
  onresult: ((e: ResultEvent) => void) | null;
  onerror: ((e: RecognitionError) => void) | null;
  onend: (() => void) | null;
  start(): void;
  stop(): void;
  abort(): void;
}

/** Runs fn after ms milliseconds; the answer cancels it. */
export type Clock = { after(ms: number, fn: () => void): () => void };

/** The voice settings, read from GET /api/v1/settings (settings/voice.json). */
export type VoiceSettings = {
  /** voice.dictation: the composer has the microphone button. */
  dictation: boolean;
  /** voice.language: a language tag, or empty for the browser's own. */
  language: string;
  /** voice.interim: the words still being heard are shown. */
  interim: boolean;
  /** voice.max_listen: the longest one go listens, in milliseconds. 0 is no limit. */
  maxListenMs: number;
  /** voice.finish_wait: how long to wait for the last words after stopping. */
  finishWaitMs: number;
};

const units: Record<string, number> = { ns: 1e-6, us: 1e-3, "µs": 1e-3, "μs": 1e-3, ms: 1, s: 1000, m: 60_000, h: 3_600_000 };

/** A Go duration, such as 2m, 90s or 1m30s, in milliseconds; undefined when it is not one or is negative. */
export function durationMs(text: string): number | undefined {
  const t = text.trim();
  if (t === "0") return 0;
  if (t === "") return undefined;
  const part = /(\d+(?:\.\d*)?|\.\d+)(ns|us|µs|μs|ms|s|m|h)/y;
  let ms = 0;
  let at = 0;
  while (at < t.length) {
    part.lastIndex = at;
    const m = part.exec(t);
    if (!m) return undefined;
    ms += Number(m[1]) * units[m[2]];
    at = part.lastIndex;
  }
  return ms;
}

/** A length of time in words: "2 minutes", "90 seconds". */
export function durationWords(ms: number): string {
  const plural = (n: number, unit: string) => `${n} ${unit}${n === 1 ? "" : "s"}`;
  if (ms >= 60_000 && ms % 60_000 === 0) return plural(ms / 60_000, "minute");
  if (ms % 1000 === 0) return plural(ms / 1000, "second");
  return plural(Math.round(ms), "millisecond");
}

/**
 * The voice settings out of the settings route's list, or undefined when one
 * is missing or does not read as its type. The composer then draws no
 * microphone: the values live in the registry, never here.
 */
export function readVoiceSettings(list: readonly { key: string; value: string }[]): VoiceSettings | undefined {
  const value = (key: string) => list.find((s) => s.key === key)?.value;
  const bool = (key: string) => {
    const v = value(key);
    return v === "true" ? true : v === "false" ? false : undefined;
  };
  const dictation = bool("voice.dictation");
  const interim = bool("voice.interim");
  const language = value("voice.language");
  const maxListenMs = durationMs(value("voice.max_listen") ?? "");
  const finishWaitMs = durationMs(value("voice.finish_wait") ?? "");
  if (dictation === undefined || interim === undefined || language === undefined || maxListenMs === undefined || finishWaitMs === undefined) return undefined;
  return { dictation, language: language.trim(), interim, maxListenMs, finishWaitMs };
}

/**
 * The box's text with words put in at a point, and the point just after
 * them. Nothing of the text is removed or replaced: a space goes between the
 * words and the text before them, and after them when text follows.
 */
export function insertAt(text: string, at: number, words: string): { text: string; at: number } {
  const w = words.trim();
  const p = Math.max(0, Math.min(Number.isFinite(at) ? at : text.length, text.length));
  if (w === "") return { text, at: p };
  const before = text.slice(0, p);
  const after = text.slice(p);
  const lead = before !== "" && !/\s$/.test(before) ? " " : "";
  const tail = after !== "" && !/^\s/.test(after) ? " " : "";
  return { text: before + lead + w + tail + after, at: p + lead.length + w.length };
}

/** Every sentence dictation says. */
export const words = {
  idleLabel: "Dictate a message",
  listeningLabel: "Stop dictation",
  finishingLabel: "Finishing the transcript",
  unavailableLabel: "Dictation is not available",
  where: "In Chrome and Edge, what you say goes to the browser maker's speech service; Keystone receives only the words.",
  waiting: "Waiting for the microphone…",
  listening: "Listening. Enter or the microphone finishes, Escape stops.",
  finishing: "Finishing the transcript…",
  review: "Review the words before sending.",
  stopped: "Stopped listening.",
  typed: "Stopped listening because you typed in the box.",
  didNotFinish: "Speech did not finish. Try dictation again or type your message.",
  couldNotStart: "Could not start listening.",
  unsupported: "This browser cannot turn speech into text. Try your keyboard's own dictation, or type your message.",
  insecure: "Dictation needs a secure address: https, or this computer's own.",
  maxed: (ms: number) => `Stopped after ${durationWords(ms)}, the longest dictation listens at one go.`,
};

/** The sentence for one of the browser's error codes. */
export function errorWords(code: string, language: string): string {
  switch (code) {
    case "not-allowed":
      return "Microphone permission was refused. Allow the microphone for this site in the browser, then try again.";
    case "audio-capture":
      return "No microphone is available.";
    case "network":
      return "The speech service could not be reached.";
    case "service-not-allowed":
      return "This browser blocked speech recognition.";
    case "language-not-supported":
      return `This browser cannot transcribe ${language ? `the language “${language}”` : "that language"}. Change voice.language on Settings → General.`;
    case "no-speech":
      return "No speech heard. Try the microphone again.";
  }
  return "Listening stopped.";
}

export type DictationOptions = {
  /** Makes the browser's recognizer, or null when this browser has none. */
  make: (() => Recognizer) | null;
  /** The page is a secure context, which the browser asks for the microphone. */
  secure: boolean;
  settings: VoiceSettings;
  /** The browser's language, used when voice.language is empty. */
  browserLanguage: string;
  /** Puts final words into the box at the insertion point. The only thing dictation does to the composer. */
  insert: (words: string) => void;
  /** Told after the view changes. */
  changed: (view: DictationView) => void;
  clock: Clock;
};

export type Dictation = {
  view(): DictationView;
  /** The microphone button: start when idle, finish when listening. */
  toggle(): void;
  start(): void;
  /** Stop listening and wait for the last words, up to voice.finish_wait. */
  finish(): void;
  /** Escape: stop at once, keeping what was heard. Reports whether there was anything to stop. */
  escape(): boolean;
  /** The person typed in the box: stop at once and leave the box as they left it. */
  edited(): void;
  /**
   * The composer's send action asks this first. While dictation runs it
   * finishes the transcript for review and answers true, and the composer
   * sends nothing. Idle, it answers false and the composer sends.
   */
  holdsSend(): boolean;
  /** Clears the last message once the person has moved on. */
  quiet(): void;
  /** The composer is going: stop without a word. */
  dispose(): void;
};

type Session = {
  rec: Recognizer;
  /** Results already put in the box. */
  done: number;
  /** Words heard and not final yet. */
  pending: string;
  /** Words were put in the box. */
  heard: boolean;
  cancels: (() => void)[];
  /** Said before the review sentence when it ends: the time limit. */
  ending: string;
};

/** Makes the dictation machine for one composer. */
export function createDictation(o: DictationOptions): Dictation {
  const reason = !o.make ? words.unsupported : !o.secure ? words.insecure : "";
  let view: DictationView = { available: reason === "", reason, phase: "idle", started: false, interim: "", message: "", problem: false };
  let session: Session | null = null;
  const language = o.settings.language || o.browserLanguage;

  function show(next: Partial<DictationView>) {
    view = { ...view, ...next };
    o.changed(view);
  }

  /** Ends a session once: the words still pending go in the box when keep is true. */
  function end(s: Session, how: { keep: boolean; abort: boolean; message: (heard: boolean) => string; problem?: boolean }) {
    if (session !== s) return;
    session = null;
    for (const cancel of s.cancels) cancel();
    if (how.keep && s.pending) {
      o.insert(s.pending);
      s.heard = true;
    }
    s.pending = "";
    if (how.abort) {
      try {
        s.rec.abort();
      } catch {
        // already stopped
      }
    }
    show({ phase: "idle", started: false, interim: "", message: how.message(s.heard), problem: how.problem ?? false });
  }

  function finish() {
    const s = session;
    if (!s || view.phase === "finishing") return;
    show({ phase: "finishing", interim: view.interim, message: words.finishing, problem: false });
    const fallback = () => end(s, { keep: true, abort: true, message: (heard) => (heard ? s.ending + words.review : words.didNotFinish) });
    s.cancels.push(o.clock.after(o.settings.finishWaitMs, fallback));
    try {
      s.rec.stop();
    } catch {
      fallback();
    }
  }

  function start() {
    if (session) return;
    if (!view.available || !o.make) {
      show({ message: view.reason, problem: true });
      return;
    }
    let rec: Recognizer;
    try {
      rec = o.make();
    } catch {
      show({ message: words.couldNotStart, problem: true });
      return;
    }
    if (language) rec.lang = language;
    rec.continuous = true;
    rec.interimResults = true; // asked for always, so Escape keeps them; shown only with voice.interim
    rec.maxAlternatives = 1;
    const s: Session = { rec, done: 0, pending: "", heard: false, cancels: [], ending: "" };
    session = s;

    rec.onstart = () => {
      if (session !== s || view.phase !== "listening") return;
      show({ started: true, message: words.listening });
    };
    rec.onresult = (e) => {
      if (session !== s) return;
      let interim = "";
      let open = false; // a result before this one is not final yet
      for (let i = s.done; i < e.results.length; i++) {
        const r = e.results[i];
        const said = (r.length > 0 ? r[0].transcript : "").trim();
        if (r.isFinal && !open) {
          s.done = i + 1;
          if (said) {
            o.insert(said);
            s.heard = true;
          }
        } else {
          open = true;
          if (said) interim = interim ? `${interim} ${said}` : said;
        }
      }
      s.pending = interim;
      const next: Partial<DictationView> = { interim: o.settings.interim ? interim : "" };
      if (!view.started && view.phase === "listening") Object.assign(next, { started: true, message: words.listening });
      show(next);
    };
    rec.onerror = (e) => {
      if (session !== s) return;
      const code = e.error;
      const quiet = code === "no-speech" || code === "aborted";
      end(s, {
        keep: true,
        abort: false,
        message: (heard) => (heard ? (quiet ? s.ending + words.review : `${errorWords(code, language)} ${words.review}`) : errorWords(code, language)),
        problem: !quiet || !(s.heard || s.pending !== ""),
      });
    };
    rec.onend = () => {
      if (session !== s) return;
      end(s, { keep: true, abort: false, message: (heard) => (heard ? s.ending + words.review : s.ending ? s.ending.trim() : errorWords("no-speech", language)) });
    };

    show({ phase: "listening", started: false, interim: "", message: words.waiting, problem: false });
    if (o.settings.maxListenMs > 0) {
      s.cancels.push(
        o.clock.after(o.settings.maxListenMs, () => {
          if (session !== s) return;
          s.ending = words.maxed(o.settings.maxListenMs) + " ";
          finish();
        }),
      );
    }
    try {
      rec.start();
    } catch {
      end(s, { keep: false, abort: true, message: () => words.couldNotStart, problem: true });
    }
  }

  return {
    view: () => view,
    toggle() {
      if (!session) start();
      else finish();
    },
    start,
    finish,
    escape() {
      const s = session;
      if (!s) return false;
      end(s, { keep: true, abort: true, message: (heard) => (heard ? `${words.stopped} ${words.review}` : words.stopped) });
      return true;
    },
    edited() {
      const s = session;
      if (!s) return;
      s.pending = ""; // the person is typing: what was still being heard is left out
      end(s, { keep: false, abort: true, message: () => words.typed });
    },
    holdsSend() {
      if (!session) return false;
      finish();
      return true;
    },
    quiet() {
      if (!session && view.message) show({ message: "", problem: false });
    },
    dispose() {
      const s = session;
      if (!s) return;
      session = null;
      for (const cancel of s.cancels) cancel();
      try {
        s.rec.abort();
      } catch {
        // already stopped
      }
    },
  };
}
