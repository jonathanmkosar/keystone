import { Link } from "react-router";
import type { Chat } from "./api";
import { titleOf, useChats } from "./chats";
import { Icon } from "./Icon";
import { ImportedTag } from "./Imported";

/** When a chat's last message was written: the time if that was today, the date if not. */
function when(chat: Chat): string {
  const at = new Date(chat.active);
  if (Number.isNaN(at.getTime())) return "";
  const now = new Date();
  if (at.toDateString() === now.toDateString()) return at.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  const year = at.getFullYear() === now.getFullYear() ? undefined : "numeric";
  return at.toLocaleDateString(undefined, { month: "short", day: "numeric", year });
}

function Row({ chat }: { chat: Chat }) {
  return (
    <Link to={`/chats/${chat.id}`} className="list-row">
      {chat.starred && (
        <span className="star-mark" title="Starred">
          <Icon name="star" size={14} />
        </span>
      )}
      <span className={chat.title ? "list-title" : "list-title nav-untitled"}>{titleOf(chat)}</span>
      <ImportedTag chat={chat} />
      <span className="list-when">{chat.busy ? "answering" : when(chat)}</span>
    </Link>
  );
}

/** Every chat, starred first, then the one with the newest message first. */
export function Chats() {
  const { status, chats, more, refresh } = useChats();
  const starred = chats.filter((c) => c.starred);
  const rest = chats.filter((c) => !c.starred);
  return (
    <div className="list-page">
      <div className="list-head">
        <h1 className="page-title">Chats</h1>
        <Link to="/" className="button button-primary">
          <Icon name="plus" size={16} />
          New chat
        </Link>
      </div>
      {status === "loading" && (
        <p className="quiet" aria-busy="true">
          Loading.
        </p>
      )}
      {status === "failed" && (
        <>
          <p className="quiet">The list of chats could not be read. Check that the server is running.</p>
          <div>
            <button type="button" className="button" onClick={refresh}>
              <Icon name="retry" size={16} />
              Try again
            </button>
          </div>
        </>
      )}
      {status === "ready" && chats.length === 0 && <p className="quiet">No chats yet. Start one with New chat.</p>}
      {starred.length > 0 && (
        <section className="list-section" aria-label="Starred">
          <div className="nav-heading">STARRED</div>
          {starred.map((c) => (
            <Row key={c.id} chat={c} />
          ))}
        </section>
      )}
      {rest.length > 0 && (
        <section className="list-section" aria-label="Recent">
          {starred.length > 0 && <div className="nav-heading">RECENTS</div>}
          {rest.map((c) => (
            <Row key={c.id} chat={c} />
          ))}
        </section>
      )}
      {more && <p className="quiet">Only the newest chats are listed here. Older ones are still in your record.</p>}
    </div>
  );
}

/** A screen the sidebar links to that a later work item builds. */
export function Soon({ title }: { title: string }) {
  return (
    <div className="home">
      <h1 className="page-title">{title}</h1>
      <p className="quiet">This screen is not built yet.</p>
    </div>
  );
}

export function Missing() {
  return (
    <div className="home">
      <h1 className="page-title">Page not found</h1>
      <p className="quiet">Nothing lives at this address.</p>
      <Link to="/" className="button">
        Back to the start
      </Link>
    </div>
  );
}
