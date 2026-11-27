// Incognito chats (P6-07), provisional on the memory mockups (P1-06): the
// switch on the new-chat screen, and the mark an incognito chat wears. An
// incognito chat runs no memory pass, gets no recap, is not found by
// past-chat search, and is deleted when the person leaves it: when its
// screen goes, or when the page goes. The server deletes one that was not
// left once it has been idle for knowledge.incognito_keep.

import { useEffect, useState, useSyncExternalStore } from "react";
import { useChats } from "./chats";
import { Icon } from "./Icon";
import { closeIncognito, closeIncognitoAsPageGoes, isIncognito } from "./memoryCalls";
import "./memory.css";

// Whether the new chat will be incognito. On a phone its switch is a round
// button in the header (P10-07), outside the new chat screen, so the choice
// is held here; the screen turns it off when it goes.
let newChatIncognito = false;
const listeners = new Set<() => void>();

export function setNewChatIncognito(on: boolean) {
  if (on === newChatIncognito) return;
  newChatIncognito = on;
  listeners.forEach((fn) => fn());
}

export function useNewChatIncognito(): boolean {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    () => newChatIncognito,
  );
}

const incognitoWhy = "An incognito chat keeps no record: nothing is remembered from it, and it is deleted when you leave it.";

/** The phone's switch, a round button at the right of the new chat's header (P10-07). */
export function IncognitoButton() {
  const on = useNewChatIncognito();
  return (
    <button type="button" className={on ? "float-button incognito-button is-on" : "float-button incognito-button"} aria-pressed={on} aria-label="Incognito" title={on ? `Incognito is on. ${incognitoWhy}` : incognitoWhy} onClick={() => setNewChatIncognito(!on)}>
      <Icon name="incognito" size={20} />
    </button>
  );
}

/** The switch on the new-chat screen. */
export function IncognitoSwitch({ on, onChange }: { on: boolean; onChange: (on: boolean) => void }) {
  return (
    <label className={on ? "incognito-switch is-on" : "incognito-switch"} title={incognitoWhy}>
      <input type="checkbox" checked={on} onChange={(e) => onChange(e.target.checked)} />
      <Icon name="incognito" size={16} />
      <span>Incognito</span>
    </label>
  );
}

// Leaving a chat's screen schedules its close a moment later, and coming
// back to it before then cancels the close. A screen that is drawn twice in
// a row, as React's strict mode does while building, is not a leaving.
const closing = new Map<string, number>();
const closeAfter = 400;

function closeSoon(chatId: string, then: () => void) {
  window.clearTimeout(closing.get(chatId));
  closing.set(
    chatId,
    window.setTimeout(() => {
      closing.delete(chatId);
      closeIncognito(chatId).then(then, () => {
        // The server deletes it once it has been idle long enough.
      });
    }, closeAfter),
  );
}

function stayed(chatId: string) {
  window.clearTimeout(closing.get(chatId));
  closing.delete(chatId);
}

/** The mark an incognito chat wears, at the top of its screen. Leaving the screen closes the chat. */
export function IncognitoMark({ chatId }: { chatId: string }) {
  const { drop } = useChats();
  const [on, setOn] = useState(false);

  useEffect(() => {
    let live = true;
    isIncognito(chatId).then(
      (yes) => {
        if (live) setOn(yes);
      },
      () => {
        // A chat whose flag cannot be read wears no mark; the server keeps
        // what the flag says all the same.
      },
    );
    return () => {
      live = false;
    };
  }, [chatId]);

  useEffect(() => {
    if (!on) return;
    stayed(chatId);
    const pageGoes = () => {
      closeIncognitoAsPageGoes(chatId);
    };
    window.addEventListener("pagehide", pageGoes);
    return () => {
      window.removeEventListener("pagehide", pageGoes);
      closeSoon(chatId, () => drop(chatId));
    };
  }, [on, chatId, drop]);

  if (!on) return null;
  return (
    <div className="incognito-mark" role="status">
      <Icon name="incognito" size={16} />
      <span>
        <strong>Incognito.</strong> Nothing from this chat is remembered, and it is deleted when you leave it.
      </span>
    </div>
  );
}
