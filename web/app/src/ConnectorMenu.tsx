// The tools menu (P7-04): a switch for each of the person's connectors in
// this chat, beside the model picker in the composer. For a chat it reads
// and writes the chat's connector routes. The new chat screen has no chat
// yet, so its switches are kept here and applied to the chat its first
// message makes, before the message is sent (applyNewChatConnectors).
// docs/architecture/connectors.md describes it. Every call is in api.ts.

import { useCallback, useEffect, useRef, useState, useSyncExternalStore, type KeyboardEvent } from "react";
import { Link } from "react-router";
import { ApiError, chatConnectors, listConnectors, switchChatConnector, type ChatConnector } from "./api";
import { useChats } from "./chats";
import { Icon } from "./Icon";
import "./model-picker.css";
import "./connectors.css";

// The new chat screen's switches: the connectors switched off, for each
// person, until the first message makes the chat.
const offForNew = new Map<string, Set<string>>();
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
 * Applies the new chat screen's switches to the chat its first message
 * made, and forgets them. A switch that cannot be applied leaves the
 * connector on, as every chat starts.
 */
export async function applyNewChatConnectors(personId: string, chatId: string): Promise<void> {
  const off = offForNew.get(personId);
  if (!off || off.size === 0) return;
  offForNew.delete(personId);
  tell();
  await Promise.all([...off].map((name) => switchChatConnector(chatId, name, false).catch(() => undefined)));
}

/** The tools menu, for a chat or for the new chat screen. It shows nothing when the person has no connectors. */
export function ConnectorMenu(props: { chatId: string } | { newChatFor: string }) {
  const isNew = "newChatFor" in props;
  const chatId = isNew ? "" : props.chatId;
  const personId = isNew ? props.newChatFor : "";
  const { lost } = useChats();
  useVersion();
  const [rows, setRows] = useState<ChatConnector[]>();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState("");
  const [problem, setProblem] = useState("");
  const wrap = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);

  const read = useCallback(async () => {
    try {
      if (isNew) {
        const got = await listConnectors();
        setRows(got.connectors.map((c) => ({ name: c.name, label: c.label || c.name, enabled: c.enabled, on: c.enabled, switched: false, tools: c.tools.length, problem: c.problem })));
      } else {
        setRows(await chatConnectors(chatId));
      }
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") lost();
      // What is on the screen stays; a menu that cannot be read is not drawn.
    }
  }, [isNew, chatId, lost]);

  useEffect(() => {
    void read();
  }, [read]);

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

  useEffect(() => {
    if (open) menu.current?.querySelector<HTMLButtonElement>('[role="menuitemcheckbox"]:not(:disabled)')?.focus();
  }, [open]);

  if (!rows || rows.length === 0) return null;
  const off = offForNew.get(personId) ?? new Set<string>();
  const shown = rows.map((r) => (isNew ? { ...r, on: r.enabled && !off.has(r.name), switched: off.has(r.name) } : r));
  const on = shown.filter((r) => r.on).length;

  async function flip(r: ChatConnector) {
    if (!r.enabled) return;
    setProblem("");
    if (isNew) {
      const next = new Set(off);
      if (r.on) next.add(r.name);
      else next.delete(r.name);
      offForNew.set(personId, next);
      tell();
      return;
    }
    setBusy(r.name);
    try {
      setRows(await switchChatConnector(chatId, r.name, !r.on));
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") lost();
      else setProblem("The switch could not be changed. Check that the server is running.");
    } finally {
      setBusy("");
    }
  }

  function toggle() {
    if (!open) void read();
    setOpen((o) => !o);
  }

  function menuKey(e: KeyboardEvent<HTMLDivElement>) {
    const items = Array.from(menu.current?.querySelectorAll<HTMLButtonElement>('[role="menuitemcheckbox"]:not(:disabled)') ?? []);
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

  const label = `Tools: ${on} of ${shown.length} connectors on${isNew ? " for the new chat" : " in this chat"}`;
  return (
    <div className="tools-menu" ref={wrap}>
      <button type="button" ref={trigger} className="model-button tools-button" aria-haspopup="menu" aria-expanded={open} aria-label={`${label}. Change them`} title={label} onClick={toggle}>
        <Icon name="plug" size={16} />
        <span className="tools-count">{on}</span>
      </button>
      {open && (
        <div className="model-menu tools-list" role="menu" aria-label={isNew ? "Connectors for the new chat" : "Connectors in this chat"} ref={menu} onKeyDown={menuKey}>
          <p className="model-menu-head">{isNew ? "Connectors for the new chat" : "Connectors in this chat"}</p>
          {shown.map((r) => (
            <button
              key={r.name}
              type="button"
              role="menuitemcheckbox"
              aria-checked={r.on}
              className="model-item tools-item"
              disabled={!r.enabled || busy === r.name}
              onClick={() => void flip(r)}
            >
              <span className={r.on ? "tools-switch is-on" : "tools-switch"} aria-hidden="true">
                <span className="tools-switch-knob" />
              </span>
              <span className="model-item-text">
                <span className="model-item-label">{r.label}</span>
                <span className="model-item-why">
                  {!r.enabled ? "Off in Settings" : r.problem ? "Could not be reached when last tried" : `${r.tools} ${r.tools === 1 ? "tool" : "tools"}`}
                </span>
              </span>
            </button>
          ))}
          {problem && (
            <p className="model-menu-note tools-problem" role="alert">
              {problem}
            </p>
          )}
          <p className="model-menu-note">
            {isNew ? "Kept for the chat your first message starts." : "A connector switched off here offers this chat no tools."} A tool that changes something still asks you first.
          </p>
          <Link className="model-menu-link" to="/settings/connectors" onClick={() => setOpen(false)}>
            Connectors
          </Link>
        </div>
      )}
    </div>
  );
}
