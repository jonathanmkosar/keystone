// Dictation on the screen (P10-01): the composer's microphone button, the
// line under the text that says what dictation is doing, and the hook that
// joins the browser's speech recognition to the machine in dictation.ts.
//
// The browser's own recognizer is used (SpeechRecognition, or
// webkitSpeechRecognition in Chrome, Edge and Safari). In Chrome and Edge it
// sends the audio to the browser maker's speech service; Keystone receives
// only the words (decision 0500). Final words are put in the box at the
// cursor, the caret is put after them and the focus left in the box, and
// nothing is sent: the composer's send action asks holdsSend first.
//
// There is no mockup of the button (the chat mockups, P1-04, do not draw
// one), so its place, just before the send button, and its look are built
// from the tokens and are provisional.

import { useEffect, useRef, useState, type RefObject } from "react";
import { listSettings } from "./api";
import { createDictation, insertAt, readVoiceSettings, words, type Dictation, type DictationView, type Recognizer, type VoiceSettings } from "./dictation";
import { Icon } from "./Icon";
import "./dictation.css";

/** What a composer holds of dictation. */
export type DictationControl = {
  /** voice.dictation is on and the settings were read: draw the button. */
  shown: boolean;
  view?: DictationView;
  /** The microphone button. */
  toggle: () => void;
  /** Escape: reports whether dictation was stopped, so the key does nothing else. */
  escape: () => boolean;
  /** Asked first by the composer's send action; true means send nothing (dictation.ts). */
  holdsSend: () => boolean;
};

type Speech = { SpeechRecognition?: new () => Recognizer; webkitSpeechRecognition?: new () => Recognizer };

/** The browser's recognizer, or null when it has none. */
function browserRecognizer(): (() => Recognizer) | null {
  const w = window as unknown as Speech;
  const Made = w.SpeechRecognition ?? w.webkitSpeechRecognition;
  return Made ? () => new Made() : null;
}

/**
 * Dictation for one composer: area is its box, text and setText its text.
 * Dictation writes to the box only through setText, at the insertion point.
 */
export function useDictation(area: RefObject<HTMLTextAreaElement | null>, text: string, setText: (text: string) => void): DictationControl {
  const [settings, setSettings] = useState<VoiceSettings>();
  const [view, setView] = useState<DictationView>();
  const machine = useRef<Dictation | undefined>(undefined);
  const now = useRef(text); // the box's text, as drawn or as dictation just wrote it
  const written = useRef(text); // the box's text as dictation, or the last render, left it
  const at = useRef(0); // where the next words go
  const caret = useRef(false); // put the caret after the words once they are drawn
  const phase = useRef<DictationView["phase"]>("idle");
  now.current = text;

  // The settings, read through the settings route each time a composer
  // opens, so a change on Settings → General is used by the next one.
  useEffect(() => {
    let live = true;
    listSettings().then(
      (r) => live && setSettings(readVoiceSettings(r.settings)),
      () => {}, // no settings, no microphone
    );
    return () => {
      live = false;
    };
  }, []);

  useEffect(() => {
    if (!settings?.dictation) return;
    const d = createDictation({
      make: browserRecognizer(),
      secure: window.isSecureContext,
      settings,
      browserLanguage: navigator.language ?? "",
      insert: (said) => {
        const r = insertAt(now.current, at.current, said);
        at.current = r.at;
        now.current = r.text;
        written.current = r.text;
        caret.current = true;
        setText(r.text);
      },
      changed: setView,
      clock: {
        after: (ms, fn) => {
          const id = window.setTimeout(fn, ms);
          return () => window.clearTimeout(id);
        },
      },
    });
    machine.current = d;
    setView(d.view());
    // A page put away stops listening, as New Keystone's did.
    const hidden = () => {
      if (document.hidden) d.finish();
    };
    document.addEventListener("visibilitychange", hidden);
    return () => {
      document.removeEventListener("visibilitychange", hidden);
      d.dispose();
      machine.current = undefined;
    };
  }, [settings, setText]);

  // A change of the text that dictation did not make is the person's: it
  // stops listening, or clears the last message.
  useEffect(() => {
    const d = machine.current;
    const el = area.current;
    if (text === written.current) {
      if (caret.current && el) {
        caret.current = false;
        el.focus();
        el.setSelectionRange(at.current, at.current);
      }
      return;
    }
    written.current = text;
    if (!d) return;
    if (d.view().phase !== "idle") d.edited();
    else d.quiet();
  }, [text, area]);

  // When dictation ends, the focus is left in the box with the caret after
  // the words, for the person to review.
  useEffect(() => {
    const was = phase.current;
    phase.current = view?.phase ?? "idle";
    if (was !== "idle" && phase.current === "idle") {
      const el = area.current;
      if (el) {
        el.focus();
        el.setSelectionRange(at.current, at.current);
      }
    }
  }, [view, area]);

  return {
    shown: !!settings?.dictation && !!view,
    view,
    toggle: () => {
      const d = machine.current;
      if (!d) return;
      if (d.view().phase === "idle") {
        const el = area.current;
        at.current = el ? el.selectionEnd : now.current.length;
        written.current = now.current;
        el?.focus();
      }
      d.toggle();
    },
    escape: () => machine.current?.escape() ?? false,
    holdsSend: () => machine.current?.holdsSend() ?? false,
  };
}

/** The microphone button. Dimmed with its reason, and still focusable, when the browser cannot dictate. */
export function DictationButton({ dictation }: { dictation: DictationControl }) {
  const v = dictation.view;
  if (!dictation.shown || !v) return null;
  const label = !v.available ? words.unavailableLabel : v.phase === "listening" ? words.listeningLabel : v.phase === "finishing" ? words.finishingLabel : words.idleLabel;
  const title = !v.available ? v.reason : v.phase === "idle" ? `${words.idleLabel}. ${words.where}` : label;
  const blocked = !v.available || v.phase === "finishing";
  return (
    <button
      type="button"
      className={"icon-button dictation-button" + (v.phase !== "idle" ? ` is-${v.phase}` : "") + (v.available ? "" : " is-unavailable")}
      aria-label={label}
      title={title}
      aria-pressed={v.phase === "listening"}
      aria-disabled={blocked || undefined}
      onClick={() => {
        if (v.phase !== "finishing") dictation.toggle();
      }}
      onKeyDown={(e) => {
        if (e.key === "Escape" && dictation.escape()) e.preventDefault();
      }}
    >
      <Icon name="mic" size={18} />
    </button>
  );
}

/** The line under the text: a dot and what dictation is doing, the words still being heard, and why it stopped. */
export function DictationStatus({ dictation }: { dictation: DictationControl }) {
  const v = dictation.view;
  if (!dictation.shown || !v) return null;
  const active = v.phase !== "idle";
  const empty = !active && !v.message;
  return (
    <div className={empty ? "offscreen" : "dictation-status"}>
      <p className={v.problem ? "dictation-message is-problem" : "dictation-message"} role="status">
        {active && <span className={"dictation-dot" + (v.started && v.phase === "listening" ? " is-live" : "")} aria-hidden="true" />}
        {v.message}
      </p>
      {v.interim && <p className="dictation-interim">{v.interim}</p>}
    </div>
  );
}
