import { useEffect, useRef, useState, type FormEvent } from "react";
import { useNavigate } from "react-router";
import { ApiError, deleteChat, renameChat, starChat } from "./api";
import { titleOf, useChats } from "./chats";
import { Icon } from "./Icon";
import { forget } from "./turns";

type Mode = "closed" | "menu" | "rename" | "delete";

/** The chat's title in the header. It opens the chat's menu: rename, star, delete. */
export function ChatTitle({ id }: { id: string }) {
  const { chats, put, drop, lost } = useChats();
  const navigate = useNavigate();
  const chat = chats.find((c) => c.id === id);
  const [mode, setMode] = useState<Mode>("closed");
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState("");
  const wrap = useRef<HTMLDivElement>(null);

  // Another chat was opened: its menu starts closed.
  useEffect(() => {
    setMode("closed");
    setProblem("");
  }, [id]);

  // A click outside, or Escape, closes whatever is open.
  useEffect(() => {
    if (mode === "closed") return;
    function down(e: MouseEvent) {
      if (wrap.current && !wrap.current.contains(e.target as Node)) setMode("closed");
    }
    function key(e: KeyboardEvent) {
      if (e.key === "Escape") setMode("closed");
    }
    document.addEventListener("mousedown", down);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("mousedown", down);
      document.removeEventListener("keydown", key);
    };
  }, [mode]);

  if (!chat) return <span className="header-title">Chat</span>;

  async function act(work: () => Promise<void>, failed: string) {
    setBusy(true);
    setProblem("");
    try {
      await work();
      setMode("closed");
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") lost();
      else if (e instanceof ApiError && e.status === 400 && e.said) setProblem(`${failed}: ${e.said}.`);
      else setProblem(`${failed}.`);
    } finally {
      setBusy(false);
    }
  }

  function rename(e: FormEvent) {
    e.preventDefault();
    const title = draft.trim();
    if (title === "" || title === chat!.title) {
      setMode("closed");
      return;
    }
    void act(async () => put(await renameChat(id, title)), "The chat was not renamed");
  }

  function star() {
    void act(async () => put(await starChat(id, !chat!.starred)), "The star was not changed");
  }

  function remove() {
    void act(async () => {
      await deleteChat(id);
      forget(id);
      drop(id);
      navigate("/");
    }, "The chat was not deleted");
  }

  if (mode === "rename") {
    return (
      <div className="title-wrap" ref={wrap}>
        <form className="rename-form" onSubmit={rename}>
          <label className="offscreen" htmlFor="chat-title">
            The chat's title
          </label>
          <input id="chat-title" className="input" value={draft} autoFocus disabled={busy} onChange={(e) => setDraft(e.target.value)} onFocus={(e) => e.target.select()} />
          <button type="submit" className="button button-primary" disabled={busy}>
            Save
          </button>
          <button type="button" className="button" disabled={busy} onClick={() => setMode("closed")}>
            Cancel
          </button>
        </form>
        {problem && (
          <span className="header-note" role="alert">
            {problem}
          </span>
        )}
      </div>
    );
  }

  return (
    <div className="title-wrap" ref={wrap}>
      <button type="button" className="title-button" aria-haspopup="menu" aria-expanded={mode !== "closed"} onClick={() => setMode((m) => (m === "closed" ? "menu" : "closed"))}>
        {chat.starred && (
          <span className="star-mark" title="Starred">
            <Icon name="star" size={14} />
          </span>
        )}
        <span className="title-text">{titleOf(chat)}</span>
        <Icon name="chevron" size={16} />
      </button>
      {problem && mode === "closed" && (
        <span className="header-note" role="alert">
          {problem}
        </span>
      )}
      {mode === "menu" && (
        <div className="menu" role="menu">
          <button
            type="button"
            className="menu-item"
            role="menuitem"
            onClick={() => {
              setDraft(chat.title);
              setProblem("");
              setMode("rename");
            }}
          >
            <Icon name="pencil" size={16} />
            Rename
          </button>
          <button type="button" className="menu-item" role="menuitem" disabled={busy} onClick={star}>
            <Icon name="star" size={16} />
            {chat.starred ? "Remove the star" : "Star"}
          </button>
          <button type="button" className="menu-item menu-item-danger" role="menuitem" onClick={() => setMode("delete")}>
            <Icon name="trash" size={16} />
            Delete
          </button>
        </div>
      )}
      {mode === "delete" && (
        <div className="menu" role="alertdialog" aria-label="Delete this chat">
          <p className="menu-note">Delete this chat, with its messages and its files? It cannot be brought back.</p>
          {problem && (
            <p className="menu-note header-note" role="alert">
              {problem}
            </p>
          )}
          <div className="menu-row">
            <button type="button" className="button button-danger" disabled={busy} onClick={remove}>
              Delete
            </button>
            <button type="button" className="button" disabled={busy} onClick={() => setMode("closed")}>
              Keep
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
