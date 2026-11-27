// The style picker (P9-05): the style a chat's replies are written in,
// beside the model picker in the composer. For a chat it reads and writes
// the chat's style route, and a pick takes effect from the chat's next
// call. The new chat screen has no chat yet, so its pick is kept here and
// applied to the chat its first message makes, before the message is sent
// (applyNewChatStyle), as the tools menu does with its switches. There is
// no styles mockup: the button and the menu are drawn like the model
// picker's, whose classes they share. docs/architecture/styles.md
// describes it. Every call is in styleCalls.ts.

import { useCallback, useEffect, useRef, useState, useSyncExternalStore, type KeyboardEvent } from "react";
import { Link } from "react-router";
import { ApiError } from "./api";
import { useChats } from "./chats";
import { styleState } from "./composerSheet";
import { Icon } from "./Icon";
import { SheetRow, useSheet } from "./PhoneSheet";
import { chatStyle, chooseChatStyle, listStyles, type Style } from "./styleCalls";
import { isActive, useLive } from "./turns";
import "./model-picker.css";
import "./style-presets.css";

// The new chat screen's pick, for each person, until the first message
// makes the chat.
const pickForNew = new Map<string, string>();
const listeners = new Set<() => void>();
let version = 0;

function tell() {
  version++;
  for (const l of listeners) l();
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

function useVersion(): number {
  return useSyncExternalStore(subscribe, () => version);
}

/**
 * Gives the chat the new chat screen's first message made the style picked
 * there, and forgets the pick. A pick that cannot be applied leaves the
 * chat on the person's default, as every chat starts.
 */
export async function applyNewChatStyle(personId: string, chatId: string): Promise<void> {
  const id = pickForNew.get(personId);
  if (id === undefined) return;
  pickForNew.delete(personId);
  tell();
  await chooseChatStyle(chatId, id).catch(() => undefined);
}

type Shown = {
  styles: Style[];
  /** The id of the style the next call carries, or "" for none. */
  current: string;
  /** The id of the person's default style. */
  def: string;
  /** Where the current style comes from: chat, default or none. */
  source: string;
};

/** The first line of a style's instructions, for the menu, which shows two lines of it at most. */
function gist(st: Style): string {
  const line = st.instructions.split("\n")[0].trim();
  return line === "" ? "No instructions: replies are written as the model writes them." : line;
}

function footOf(shown: Shown, isNew: boolean, picked: boolean): string {
  if (isNew) return picked ? "The new chat starts with the style you picked." : "Your default style for new chats, set under Styles in Settings.";
  if (shown.source === "default") return "Your default style. The chat keeps it from its first reply until you pick another.";
  if (shown.source === "none") return "No style: your default names a style that is not there.";
  return "Used from your next message. The rest of the chat is not changed.";
}

/** The style picker, for a chat or for the new chat screen. It shows nothing until the styles are read. */
export function StylePicker(props: { chatId: string } | { newChatFor: string }) {
  const isNew = "newChatFor" in props;
  const chatId = isNew ? "" : props.chatId;
  const personId = isNew ? props.newChatFor : "";
  const { lost } = useChats();
  useVersion();
  const sheet = useSheet();
  const live = useLive(isNew ? undefined : chatId);
  const active = isActive(live);
  const [shown, setShown] = useState<Shown>();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState("");
  const wrap = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);

  const read = useCallback(async () => {
    try {
      if (isNew) {
        const got = await listStyles();
        setShown({ styles: got.styles, current: got.styles.some((s) => s.id === got.default) ? got.default : "", def: got.default, source: "default" });
      } else {
        const got = await chatStyle(chatId);
        setShown({ styles: got.styles, current: got.style?.id ?? "", def: got.default, source: got.source });
      }
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") lost();
      // What is on the screen stays; a picker that cannot be read is not drawn.
    }
  }, [isNew, chatId, lost]);

  // Read on opening the chat, and again when a turn ends: its first turn
  // keeps the default as the chat's own.
  useEffect(() => {
    if (!active) void read();
  }, [active, read]);

  // A click outside, or Escape, closes the menu.
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

  // The menu opens on the style in use.
  useEffect(() => {
    if (!open) return;
    const items = Array.from(menu.current?.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]:not(:disabled)') ?? []);
    (items.find((b) => b.getAttribute("aria-checked") === "true") ?? items[0])?.focus();
  }, [open]);

  if (!shown || shown.styles.length === 0) return null;
  const picked = isNew ? pickForNew.get(personId) : undefined;
  const currentId = picked ?? shown.current;
  const current = shown.styles.find((s) => s.id === currentId);
  const label = current?.name ?? "No style";
  const foot = footOf(shown, isNew, picked !== undefined);

  async function choose(st: Style) {
    setProblem("");
    if (isNew) {
      if (st.id === shown?.current) pickForNew.delete(personId);
      else pickForNew.set(personId, st.id);
      tell();
      setOpen(false);
      trigger.current?.focus();
      sheet?.done();
      return;
    }
    if (st.id === shown?.current && shown.source === "chat") {
      setOpen(false);
      trigger.current?.focus();
      sheet?.done();
      return;
    }
    setBusy(true);
    try {
      const got = await chooseChatStyle(chatId, st.id);
      setShown({ styles: got.styles, current: got.style?.id ?? "", def: got.default, source: got.source });
      setOpen(false);
      trigger.current?.focus();
      sheet?.done();
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") lost();
      else setProblem("The style could not be changed. Check that the server is running.");
    } finally {
      setBusy(false);
    }
  }

  function toggle() {
    if (!open) void read();
    setOpen((o) => !o);
  }

  function menuKey(e: KeyboardEvent<HTMLDivElement>) {
    const items = Array.from(menu.current?.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]:not(:disabled)') ?? []);
    if (items.length === 0) return;
    const at = items.indexOf(document.activeElement as HTMLButtonElement);
    let next = -1;
    if (e.key === "ArrowDown") next = (at + 1) % items.length;
    else if (e.key === "ArrowUp") next = (at - 1 + items.length) % items.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = items.length - 1;
    else if (e.key === "Tab") setOpen(false);
    if (next >= 0) {
      e.preventDefault();
      items[next].focus();
    }
  }

  const heading = isNew ? "Style for the new chat" : "Style for this chat";
  return (
    <div className={sheet ? "style-picker sheet-item" : "style-picker"} ref={wrap}>
      {sheet ? (
        // In the phone's + sheet it is a row, and its styles open under it (decision 0590).
        <SheetRow ref={trigger} icon="pencil" name="Style" state={styleState(current?.name)} expanded={open} title={`The style replies are written in: ${label}`} onClick={toggle} />
      ) : (
        <button
          type="button"
          ref={trigger}
          className="model-button style-button"
          aria-haspopup="menu"
          aria-expanded={open}
          aria-label={`Style: ${label}. Pick another style`}
          title={`The style replies are written in: ${label}`}
          onClick={toggle}
        >
          <Icon name="pencil" size={16} />
          <span className="style-button-label">{label}</span>
        </button>
      )}
      {open && (
        <div className="model-menu style-menu" role="menu" aria-label={heading} ref={menu} onKeyDown={menuKey}>
          <p className="model-menu-head">{heading}</p>
          {shown.styles.map((st) => {
            const on = st.id === currentId;
            return (
              <button
                key={st.id}
                type="button"
                role="menuitemradio"
                aria-checked={on}
                className={on ? "model-item is-current" : "model-item"}
                disabled={busy || active}
                onClick={() => void choose(st)}
              >
                <span className="model-item-check">{on && <Icon name="check" size={16} />}</span>
                <span className="model-item-text">
                  <span className="model-item-label">{st.name}</span>
                  <span className="model-item-why style-item-gist">{gist(st)}</span>
                </span>
                <span className="model-tags">
                  {st.id === shown.def && <span className="model-tag">default</span>}
                  {!st.built_in && <span className="model-tag is-own">yours</span>}
                </span>
              </button>
            );
          })}
          {active && <p className="model-menu-note">A reply is being written. Pick a style when it ends.</p>}
          {problem && (
            <p className="model-menu-note style-problem" role="alert">
              {problem}
            </p>
          )}
          {foot && <p className="model-menu-note">{foot}</p>}
          <Link className="model-menu-link" to="/settings/styles" onClick={() => setOpen(false)}>
            Manage styles
          </Link>
        </div>
      )}
    </div>
  );
}
