// The small actions beside Copy on a message (P5-04): Pin on every message,
// and Inspect on a reply, which opens the turn inspector for the turn that
// wrote it. Built from the tokens; there is no mockup for them yet.

import { useState } from "react";
import { ApiError } from "./api";
import { Icon } from "./Icon";
import { openInspector } from "./Inspector";
import { CopyButton } from "./Markdown";
import { pinMessage, usePins } from "./pins";

/** Pins a message so it is in every call of the chat, or takes the pin off. */
export function PinButton({ chatId, messageId }: { chatId: string; messageId: number }) {
  const pinned = usePins(chatId).has(messageId);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);

  async function toggle() {
    setBusy(true);
    setFailed(false);
    try {
      await pinMessage(chatId, messageId, !pinned);
    } catch (e) {
      setFailed(!(e instanceof ApiError && e.kind === "signed-out"));
    } finally {
      setBusy(false);
    }
  }

  const label = failed ? "Could not pin" : pinned ? "Pinned" : "Pin";
  return (
    <button
      type="button"
      className={pinned ? "copy-button action-on" : "copy-button"}
      aria-pressed={pinned}
      disabled={busy}
      title={pinned ? "Pinned: sent with every reply in this chat. Click to take the pin off." : "Pin: send this message with every reply in this chat, even when the turns around it are left out."}
      onClick={() => void toggle()}
    >
      <Icon name="pin" size={14} />
      {label}
    </button>
  );
}

/** Copy and Pin under a person's message. They show on hover and focus, and always once it is pinned. */
export function SaidActions({ chatId, messageId, text }: { chatId: string; messageId: number; text: string }) {
  const pinned = usePins(chatId).has(messageId);
  return (
    <div className={pinned ? "said-actions is-pinned" : "said-actions"}>
      {text !== "" && <CopyButton text={text} />}
      <PinButton chatId={chatId} messageId={messageId} />
    </div>
  );
}

/** Opens the turn inspector for the turn that wrote a reply. */
export function InspectButton({ chatId, turnId }: { chatId: string; turnId: string }) {
  return (
    <button type="button" className="copy-button" title="What went into this reply, with tokens and cost" onClick={() => openInspector(chatId, turnId)}>
      <Icon name="layers" size={14} />
      Inspect
    </button>
  );
}
