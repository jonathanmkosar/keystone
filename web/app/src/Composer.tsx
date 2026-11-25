import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { Icon } from "./Icon";

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
  onSend: (text: string) => void;
  onStop?: () => void;
}) {
  const [text, setText] = useState(initial);
  const area = useRef<HTMLTextAreaElement>(null);

  // The box grows with what is typed, up to the limit the stylesheet sets.
  // The height is set through the element, which the content security policy
  // allows; it refuses only a style written into the markup.
  useEffect(() => {
    const el = area.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${el.scrollHeight}px`;
  }, [text]);

  useEffect(() => {
    area.current?.focus();
  }, [focusKey]);

  const ready = text.trim() !== "" && !busy;

  function send() {
    if (!ready) return;
    onSend(text.trim());
    setText("");
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    send();
  }

  function keyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    // Enter while a word is being composed, as in Japanese, picks the word.
    if (e.key !== "Enter" || e.shiftKey || e.nativeEvent.isComposing) return;
    e.preventDefault();
    send();
  }

  return (
    <form className={large ? "composer composer-large" : "composer"} onSubmit={submit}>
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
      <div className="composer-row">
        <span className="composer-hint">Enter to send, Shift and Enter for a new line</span>
        {busy ? (
          <button type="button" className="send-button is-stop" aria-label="Stop the reply" title="Stop" disabled={stopping || !onStop} onClick={onStop}>
            <Icon name="stop" />
          </button>
        ) : (
          <button type="submit" className="send-button" aria-label="Send" title="Send" disabled={!ready}>
            <Icon name="send" />
          </button>
        )}
      </div>
    </form>
  );
}
