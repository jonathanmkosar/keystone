// The right of a chat's header (P5-06): "Continued from" with a link to the
// chat this one was handed off from, a link to the newest chat handed off
// from this one, and the Hand off button, drawn where the mockups put Share
// (MainDark.html). A handoff makes a fresh chat that carries a summary of
// this one, and opens it with the same model. There is no mockup for the
// links, so they are provisional. docs/architecture/attach-and-handoff.md.

import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { ApiError, handOff } from "./api";
import { useAttached } from "./attached";
import { useChats } from "./chats";
import { Icon } from "./Icon";
import { pickModel } from "./modelPicks";
import { ShareControl } from "./ShareControl";
import { isActive, useLive } from "./turns";
import "./attach.css";

export function ChatLinks({ id }: { id: string }) {
  const { put, lost } = useChats();
  const navigate = useNavigate();
  const a = useAttached(id, lost);
  const live = useLive(id);
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
        <button type="button" className="button handoff-button" disabled={busy || isActive(live)} title="Open a fresh chat that carries a summary of this one, linked back to it" onClick={() => void handoff()}>
          <Icon name="handoff" size={16} />
          <span className="handoff-label">{busy ? "Handing off…" : "Hand off"}</span>
        </button>
      )}
      {a && !a.incognito && <ShareControl chatId={id} />}
    </div>
  );
}
