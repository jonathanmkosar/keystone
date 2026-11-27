import { useCallback, useEffect, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from "react";
import { AttachButton, AttachChips, useAttachments } from "./Attachments";
import { DictationButton, DictationStatus, useDictation } from "./DictationControls";
import { Icon } from "./Icon";
import { LocationButton, LocationChip, useLocation } from "./LocationControl";
import type { SentPosition } from "./location";
import { mayTakeFocus, Sheet, useNarrow } from "./PhoneSheet";

/**
 * The box a message is written in. Enter sends, and Shift with Enter makes a
 * new line. While a reply is arriving the send button is a stop button.
 */
export function Composer({
  placeholder,
  busy,
  stopping = false,
  large = false,
  focusKey,
  initial = "",
  onSend,
  onStop,
  beside,
  model,
}: {
  placeholder: string;
  /** A reply is arriving, so nothing can be sent and the button stops it. */
  busy: boolean;
  stopping?: boolean;
  /** The taller box of the new chat screen. */
  large?: boolean;
  /** When this changes the box takes the focus: the chat that was opened. */
  focusKey?: string;
  /** What the box holds at first: a message that could not be sent. */
  initial?: string;
  /** The message, the files to upload with it (P9-02), and the rounded location that goes with it alone (P10-06). */
  onSend: (text: string, files: File[], location?: SentPosition) => void;
  onStop?: () => void;
  /** Drawn before the model picker: the research, tools and style controls. On a phone they are rows of the + sheet. */
  beside?: ReactNode;
  /** Drawn just before the microphone: the model picker. On a phone it is the chip beside the +. */
  model?: ReactNode;
}) {
  const [text, setText] = useState(initial);
  const area = useRef<HTMLTextAreaElement>(null);
  const narrow = useNarrow(); // a phone: the + sheet (decision 0590)
  const [sheet, setSheet] = useState(false);
  const plus = useRef<HTMLButtonElement>(null);
  const closeSheet = useCallback(() => setSheet(false), []);
  const attach = useAttachments(); // the files that go with the message (P9-02, Attachments.tsx)
  const dictation = useDictation(area, text, setText); // speech into the box, never sent by itself (P10-01, DictationControls.tsx)
  const location = useLocation(); // the position that goes with the next message only, on a tap (P10-06, LocationControl.tsx)

  // The box grows with what is typed, up to the limit the stylesheet sets.
  // The height is set through the element, which the content security policy
  // allows; it refuses only a style written into the markup.
  useEffect(() => {
    const el = area.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${el.scrollHeight}px`;
  }, [text]);

  // On a phone nothing takes the focus by itself: its keyboard would rise
  // over the chat that was opened.
  useEffect(() => {
    if (mayTakeFocus()) area.current?.focus();
  }, [focusKey]);

  const ready = (text.trim() !== "" || attach.files.length > 0) && !busy;

  function send() {
    // While dictation listens, Send and Enter finish the transcript for the
    // person to review, and nothing is sent (P10-01).
    if (dictation.holdsSend()) return;
    if (!ready) return;
    onSend(text.trim(), attach.files, location.take());
    setText("");
    attach.clear();
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    send();
  }

  function keyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Escape" && dictation.escape()) {
      e.preventDefault(); // Escape stops dictation (P10-01)
      return;
    }
    // Enter while a word is being composed, as in Japanese, picks the word.
    if (e.key !== "Enter" || e.shiftKey || e.nativeEvent.isComposing) return;
    e.preventDefault();
    send();
  }

  const sendButton = busy ? (
    <button type="button" className="send-button is-stop" aria-label="Stop the reply" title="Stop" disabled={stopping || !onStop} onClick={onStop}>
      <Icon name="stop" />
    </button>
  ) : (
    <button type="submit" className="send-button" aria-label="Send" title="Send" disabled={!ready}>
      <Icon name="send" />
    </button>
  );

  return (
    <form className={(large ? "composer composer-large" : "composer") + (attach.dragging ? " is-dropping" : "")} onSubmit={submit} {...attach.dropProps}>
      <AttachChips attach={attach} />
      <LocationChip location={location} />
      <label className="offscreen" htmlFor="composer-message">
        Message
      </label>
      <textarea
        id="composer-message"
        ref={area}
        className="composer-text"
        rows={large ? 3 : 2}
        placeholder={placeholder}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={keyDown}
      />
      <DictationStatus dictation={dictation} />
      {narrow ? (
        // A phone: a round +, the model's chip, the microphone and a round
        // send button; everything else is a row of the sheet the + opens.
        <div className="composer-row is-phone">
          <button ref={plus} type="button" className="plus-button" aria-label="Add files, chats and tools" aria-haspopup="dialog" aria-expanded={sheet} title="Add files, chats and tools" onClick={() => setSheet((s) => !s)}>
            <Icon name="plus" size={20} />
          </button>
          {model}
          <DictationButton dictation={dictation} />
          {sendButton}
          <Sheet open={sheet} label="Add to your message" onClose={closeSheet} opener={plus}>
            <AttachButton attach={attach} />
            <LocationButton location={location} />
            {beside}
          </Sheet>
        </div>
      ) : (
        <div className="composer-row">
          <AttachButton attach={attach} />
          <LocationButton location={location} />
          <span className="composer-hint">Enter to send, Shift and Enter for a new line</span>
          {beside}
          {model}
          <DictationButton dictation={dictation} />
          {sendButton}
        </div>
      )}
    </form>
  );
}
