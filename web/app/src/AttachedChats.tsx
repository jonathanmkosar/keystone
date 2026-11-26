// The chats attached to a chat (P5-06), over its composer: each as a chip
// that opens it, with a button that takes it off, and the control that
// attaches another of the person's chats, picked from their list and
// filtered by title and summary as they type. The chat it continues from,
// after a handoff, is a chip too, and stays. There is no mockup for any of
// it, so it is provisional: the chips follow the composer's own lines and
// the picker is drawn like the model picker's menu (model-picker.css).
// docs/architecture/attach-and-handoff.md describes the routes.

import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { Link } from "react-router";
import { ApiError, setAttached, type Chat } from "./api";
import { keepAttached, useAttached } from "./attached";
import { titleOf, useChats } from "./chats";
import { Icon } from "./Icon";
import "./model-picker.css";
import "./attach.css";

/** The chat list carries each chat's recap summary when it has one (P5-05). */
type Listed = Chat & { summary?: string };

// The picker shows at most this many chats; typing narrows them.
const shownMost = 30;

function matches(c: Listed, words: string[]): boolean {
  const text = `${c.title} ${c.summary ?? ""}`.toLowerCase();
  return words.every((w) => text.includes(w));
}

export function AttachedChats({ chatId }: { chatId: string }) {
  const { chats, lost } = useChats();
  const a = useAttached(chatId, lost);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState("");
  const [problem, setProblem] = useState("");
  const wrap = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLDivElement>(null);

  // A click outside, or Escape, closes the picker.
  useEffect(() => {
    if (!open) return;
    function down(e: MouseEvent) {
      if (wrap.current && !wrap.current.contains(e.target as Node)) setOpen(false);
    }
    function escape(e: globalThis.KeyboardEvent) {
      if (e.key === "Escape") {
        setOpen(false);
        trigger.current?.focus();
      }
    }
    document.addEventListener("mousedown", down);
    document.addEventListener("keydown", escape);
    return () => {
      document.removeEventListener("mousedown", down);
      document.removeEventListener("keydown", escape);
    };
  }, [open]);

  // On a phone the picker is a sheet at the top of what can be seen
  // (attach.css). The on-screen keyboard shrinks and moves that, and CSS
  // cannot tell, so the sheet is told where the visible part starts and how
  // tall it is, and its list scrolls inside it.
  useEffect(() => {
    const vv = window.visualViewport;
    const el = list.current;
    if (!open || !vv || !el) return;
    const place = () => {
      el.style.setProperty("--seen-top", `${vv.offsetTop}px`);
      el.style.setProperty("--seen-height", `${vv.height}px`);
    };
    place();
    vv.addEventListener("resize", place);
    vv.addEventListener("scroll", place);
    return () => {
      vv.removeEventListener("resize", place);
      vv.removeEventListener("scroll", place);
    };
  }, [open]);

  // An incognito chat attaches nothing, so nothing is drawn.
  if (!a || a.incognito) return null;

  const taken = new Set(a.attached.map((x) => x.id));
  const full = a.attached.length >= a.max;
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  const choices = (chats as Listed[]).filter((c) => c.id !== chatId && !taken.has(c.id) && matches(c, words)).slice(0, shownMost);

  async function change(otherId: string, attach: boolean) {
    setBusy(otherId);
    setProblem("");
    try {
      keepAttached(await setAttached(chatId, otherId, attach));
      if (attach) {
        setOpen(false);
        setQuery("");
      }
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") lost();
      else if (e instanceof ApiError && e.code === "incognito") setProblem("An incognito chat is not attached to anything.");
      else if (e instanceof ApiError && e.code === "too_many") setProblem(`This chat has as many attached chats as it may (${a?.max}). Take one off first.`);
      else setProblem(attach ? "The chat could not be attached. Check that the server is running." : "The chat could not be taken off. Check that the server is running.");
    } finally {
      setBusy("");
    }
  }

  function listKey(e: KeyboardEvent<HTMLDivElement>) {
    const items = Array.from(list.current?.querySelectorAll<HTMLElement>(".attached-search, .model-item:not(:disabled)") ?? []);
    if (items.length === 0) return;
    const at = items.indexOf(document.activeElement as HTMLElement);
    let next = -1;
    if (e.key === "ArrowDown") next = (at + 1) % items.length;
    else if (e.key === "ArrowUp") next = (at - 1 + items.length) % items.length;
    if (next >= 0) {
      e.preventDefault();
      items[next].focus();
    }
  }

  // A touch screen raises its keyboard when a box takes the focus, which
  // covers half the list: there the person sees their chats first and taps
  // the box if they want to narrow them.
  const touch = window.matchMedia?.("(pointer: coarse)").matches ?? false;
  const addLabel = full ? `This chat has as many attached chats as it may (${a.max})` : "Attach another of your chats: each reply in this chat is given its summary, and can open the rest of it";
  return (
    <div className="attached" ref={wrap}>
      <ul className="attached-chips" aria-label="Chats attached to this one">
        {a.attached.map((x) => {
          const title = x.title || "Untitled chat";
          const why = [x.parent ? "This chat continues from it." : "", x.summary ?? "", x.sensitive ? "It holds content from a sensitive space, so only a local or trusted model is given it." : "", x.incognito ? "It is incognito now, so no reply is given it." : ""]
            .filter(Boolean)
            .join(" ");
          return (
            <li key={x.id} className={["attached-chip", x.parent ? "is-parent" : "", x.incognito ? "is-off" : ""].filter(Boolean).join(" ")}>
              <Icon name={x.parent ? "handoff" : "chatlink"} size={14} />
              <Link to={`/chats/${x.id}`} className="attached-chip-title" title={why || title}>
                {x.parent && <span className="attached-chip-words">Continued from </span>}
                {title}
              </Link>
              {x.sensitive && <span className="attached-mark is-private">sensitive</span>}
              {x.incognito && <span className="attached-mark">incognito</span>}
              {!x.parent && (
                <button type="button" className="attached-remove" aria-label={`Take ${title} off this chat`} title="Take it off" disabled={busy === x.id} onClick={() => void change(x.id, false)}>
                  <Icon name="close" size={14} />
                </button>
              )}
            </li>
          );
        })}
        <li>
          <button
            type="button"
            ref={trigger}
            className="attached-add"
            aria-haspopup="dialog"
            aria-expanded={open}
            title={addLabel}
            disabled={full && !open}
            onClick={() => {
              setProblem("");
              setOpen((o) => !o);
            }}
          >
            <Icon name="chatlink" size={16} />
            {a.attached.length === 0 ? "Attach a chat" : "Attach"}
          </button>
        </li>
      </ul>
      {open && (
        <div className="model-menu attached-menu" role="dialog" aria-label="Attach a chat" ref={list} onKeyDown={listKey}>
          <div className="attached-menu-top">
            <p className="model-menu-head">Attach a chat</p>
            <button type="button" className="attached-remove" aria-label="Close" title="Close" onClick={() => setOpen(false)}>
              <Icon name="close" size={14} />
            </button>
          </div>
          <label className="offscreen" htmlFor="attached-search">
            Find a chat by its title or summary
          </label>
          <input id="attached-search" className="input attached-search" placeholder="Find a chat by its title or summary" value={query} autoFocus={!touch} autoComplete="off" onChange={(e) => setQuery(e.target.value)} />
          {choices.map((c) => (
            <button key={c.id} type="button" className="model-item attached-item" disabled={busy === c.id} onClick={() => void change(c.id, true)}>
              <Icon name="chatlink" size={16} />
              <span className="model-item-text">
                <span className="model-item-label">{titleOf(c)}</span>
                {c.summary && <span className="model-item-why attached-item-why">{c.summary}</span>}
              </span>
            </button>
          ))}
          {choices.length === 0 && <p className="model-menu-note">{words.length > 0 ? "None of your other chats has those words in its title or summary." : "You have no other chat to attach."}</p>}
          <p className="model-menu-note">Each reply in this chat is given an attached chat's summary, and can open the rest of it. A chat that holds sensitive content is given only to a local or trusted model.</p>
        </div>
      )}
      {problem && (
        <p className="composer-problem" role="alert">
          {problem}
        </p>
      )}
    </div>
  );
}
