// The right of a chat's header (P5-06): "Continued from" with a link to the
// chat this one was handed off from, a link to the newest chat handed off
// from this one, and the Hand off button, drawn where the mockups put Share
// (MainDark.html). A handoff makes a fresh chat that carries a summary of
// this one, and opens it with the same model. There is no mockup for the
// links, so they are provisional. docs/architecture/attach-and-handoff.md.
//
// On a phone the same things, with Share and the project, are rows of a
// sheet behind one ... button (ChatMenu, decision 0590), so the title has
// the header's room.

import { useCallback, useRef, useState } from "react";
import { Link, useNavigate } from "react-router";
import { ApiError, handOff } from "./api";
import { useAttached } from "./attached";
import { useChats } from "./chats";
import { rowLabel } from "./composerSheet";
import { Icon } from "./Icon";
import { pickModel } from "./modelPicks";
import { Sheet, SheetRow } from "./PhoneSheet";
import { ChatProjectChip } from "./Projects";
import { ShareControl } from "./ShareControl";
import { isActive, useLive } from "./turns";
import "./attach.css";

/** Hand off: makes the new chat, picks for it the model this one would use next, and opens it. */
function useHandoff(id: string) {
  const { put, lost } = useChats();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState("");

  async function handoff() {
    setBusy(true);
    setProblem("");
    try {
      const made = await handOff(id);
      put(made.chat.chat, true);
      // The new chat goes on with the model this one would use next: it is
      // sent with its first message, as a pick is.
      if (made.model) pickModel(made.chat.chat.id, made.model);
      navigate(`/chats/${made.chat.chat.id}`);
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") lost();
      else if (e instanceof ApiError && e.code === "incognito") setProblem("An incognito chat is not handed off.");
      else if (e instanceof ApiError && e.status === 400) setProblem("There is nothing in this chat to carry into a new one yet.");
      else setProblem("The chat could not be handed off. Check that the server is running.");
    } finally {
      setBusy(false);
    }
  }

  return { busy, problem, handoff };
}

const handoffWhy = "Open a fresh chat that carries a summary of this one, linked back to it";

export function ChatLinks({ id }: { id: string }) {
  const { lost } = useChats();
  const a = useAttached(id, lost);
  const live = useLive(id);
  const { busy, problem, handoff } = useHandoff(id);

  const newest = a?.handoffs.at(-1);
  const more = (a?.handoffs.length ?? 0) - 1;
  return (
    <div className="chat-links">
      {a?.parent && (
        <Link to={`/chats/${a.parent.id}`} className="chat-link" title={`This chat continues from ${a.parent.title || "an untitled chat"}`}>
          <Icon name="handoff" size={14} />
          <span className="chat-link-words">Continued from</span>
          <span className="chat-link-title">{a.parent.title || "an untitled chat"}</span>
        </Link>
      )}
      {newest && (
        <Link to={`/chats/${newest.id}`} className="chat-link" title={more > 0 ? `Handed off ${more + 1} times. This is the newest.` : "This chat was handed off to it"}>
          <span className="chat-link-words">Continued in</span>
          <span className="chat-link-title">{newest.title || "a new chat"}</span>
          {more > 0 && <span className="chat-link-more">+{more}</span>}
          <Icon name="caret" size={14} />
        </Link>
      )}
      {problem && (
        <span className="header-note" role="alert">
          {problem}
        </span>
      )}
      {a && !a.incognito && (
        <button type="button" className="button handoff-button" disabled={busy || isActive(live)} title={handoffWhy} onClick={() => void handoff()}>
          <Icon name="handoff" size={16} />
          <span className="handoff-label">{busy ? "Handing off…" : "Hand off"}</span>
        </button>
      )}
      {a && !a.incognito && <ShareControl chatId={id} />}
    </div>
  );
}

/**
 * The phone's header action (decision 0590): one ... button that opens a
 * sheet with Share, Hand off, the chat's project, and the links to the chat
 * it continues from and the newest it was handed off to, each a row.
 */
export function ChatMenu({ id }: { id: string }) {
  const { lost } = useChats();
  const a = useAttached(id, lost);
  const live = useLive(id);
  const { busy, problem, handoff } = useHandoff(id);
  const [open, setOpen] = useState(false);
  const button = useRef<HTMLButtonElement>(null);
  const close = useCallback(() => setOpen(false), []);

  const newest = a?.handoffs.at(-1);
  const more = (a?.handoffs.length ?? 0) - 1;
  const parentTitle = a?.parent ? a.parent.title || "an untitled chat" : "";
  const newestTitle = newest ? `${newest.title || "a new chat"}${more > 0 ? ` +${more}` : ""}` : "";
  return (
    <div className="chat-menu">
      <button
        ref={button}
        type="button"
        className="icon-button chat-menu-button"
        aria-label="This chat: share, hand off, its project and its links"
        title="Share, hand off, project and links"
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
      >
        <Icon name="more" size={22} />
      </button>
      <Sheet open={open} label="This chat" onClose={close} opener={button}>
        {a && !a.incognito && <ShareControl chatId={id} />}
        {a && !a.incognito && (
          <div className="sheet-item">
            <SheetRow icon="handoff" name="Hand off" state={busy ? "Handing off…" : ""} disabled={busy || isActive(live)} title={handoffWhy} onClick={() => void handoff()} />
          </div>
        )}
        {problem && (
          <p className="sheet-note" role="alert">
            {problem}
          </p>
        )}
        <ChatProjectChip chatId={id} />
        {a?.parent && (
          <div className="sheet-item">
            <Link to={`/chats/${a.parent.id}`} className="sheet-row" aria-label={rowLabel("Continued from", parentTitle)} onClick={close}>
              <Icon name="handoff" size={20} />
              <span className="sheet-row-name">Continued from</span>
              <span className="sheet-row-state is-link">{parentTitle}</span>
            </Link>
          </div>
        )}
        {newest && (
          <div className="sheet-item">
            <Link to={`/chats/${newest.id}`} className="sheet-row" aria-label={rowLabel("Continued in", newestTitle)} title={more > 0 ? `Handed off ${more + 1} times. This is the newest.` : undefined} onClick={close}>
              <Icon name="caret" size={20} />
              <span className="sheet-row-name">Continued in</span>
              <span className="sheet-row-state is-link">{newestTitle}</span>
            </Link>
          </div>
        )}
      </Sheet>
    </div>
  );
}
