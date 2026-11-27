import { useCallback, useEffect, useRef, useState, type FormEvent, type KeyboardEvent, type MouseEvent, type ReactNode } from "react";
import { AttachButton, AttachChips, useAttachments } from "./Attachments";
import { composerExpanded, phonePlaceholder } from "./composerSheet";
import { DictationButton, DictationStatus, useDictation } from "./DictationControls";
import { Icon } from "./Icon";
import { mayTakeFocus, Sheet, useNarrow } from "./PhoneSheet";

// How long after the focus leaves the phone's composer it folds back into
// its idle pill (P10-07, decision 0610): long enough for a tap's click to land
// on the button it began on, and for the focus to come back from a sheet.
const foldAfter = 120;

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
  /** The message, and the files to upload with it (P9-02). */
  onSend: (text: string, files: File[]) => void;
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

  // A phone: one pill row while idle, the card once focused or holding
  // anything (P10-07, decision 0610). The focus counts while it is anywhere
  // in the composer's React tree, so in a sheet it opened too: React's focus
  // events come up through the sheet's portal. Leaving folds it a moment later.
  const [focusIn, setFocusIn] = useState(false);
  const fold = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(fold.current), []);
  const phase = dictation.view?.phase;
  const expanded = !narrow || composerExpanded({ focused: focusIn, text, files: attach.files.length, dictating: phase !== undefined && phase !== "idle" });

  function focused() {
    window.clearTimeout(fold.current);
    setFocusIn(true);
  }

  function left() {
    window.clearTimeout(fold.current);
    fold.current = window.setTimeout(() => setFocusIn(false), foldAfter);
  }

  // A press on the row's buttons leaves the focus in the box, so the
  // phone's keyboard stays up for the next message. Only a press on the row
  // itself: a sheet's boxes, drawn elsewhere in the page, take the focus.
  function keepFocus(e: MouseEvent<HTMLDivElement>) {
    if (document.activeElement === area.current && e.currentTarget.contains(e.target as Node)) e.preventDefault();
  }

  // The box grows with what is typed, up to the limit the stylesheet sets.
  // The height is set through the element, which the content security policy
  // allows; it refuses only a style written into the markup.
  useEffect(() => {
    const el = area.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${el.scrollHeight}px`;
  }, [text, expanded]);

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
    onSend(text.trim(), attach.files);
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

  const formClass = [large ? "composer composer-large" : "composer", attach.dragging ? "is-dropping" : "", narrow ? "is-phone" : "", expanded ? "" : "is-idle"].filter(Boolean).join(" ");

  return (
    <form className={formClass} onSubmit={submit} onFocus={narrow ? focused : undefined} onBlur={narrow ? left : undefined} {...attach.dropProps}>
      <AttachChips attach={attach} />
      <label className="offscreen" htmlFor="composer-message">
        Message
      </label>
      <textarea
        id="composer-message"
        ref={area}
        className="composer-text"
        rows={narrow ? 1 : large ? 3 : 2}
        placeholder={narrow ? phonePlaceholder : placeholder}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={keyDown}
      />
      <DictationStatus dictation={dictation} />
      {narrow ? (
        // A phone: a round +, the model's chip, the microphone and a round
        // send button; everything else is a row of the sheet the + opens.
        // Idle, the row and the box are one pill and the chip waits
        // (phone-sheet.css); the box is the same element in both, so the
        // focus stays in it as the composer changes shape.
        <div className="composer-row is-phone" onMouseDown={keepFocus}>
          <button ref={plus} type="button" className="plus-button" aria-label="Add files, chats and tools" aria-haspopup="dialog" aria-expanded={sheet} title="Add files, chats and tools" onClick={() => setSheet((s) => !s)}>
            <Icon name="plus" size={20} />
          </button>
          {model}
          <DictationButton dictation={dictation} />
          {sendButton}
          <Sheet open={sheet} label="Add to your message" onClose={closeSheet} opener={plus}>
            <AttachButton attach={attach} />
            {beside}
          </Sheet>
        </div>
      ) : (
        <div className="composer-row">
          <AttachButton attach={attach} />
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
