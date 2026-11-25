// A chat's pinned messages (P5-02, shown since P5-04). A pinned message is
// in every call of its chat, even when the turns around it are left out or
// summarised. The pins are kept here, by chat, outside the screens, as
// turns.ts keeps the replies that are arriving, so the Pin action on each
// message and the turn inspector show the same. A chat's pins are read the
// first time a screen asks for them.

import { useSyncExternalStore } from "react";
import { listPins, setPin } from "./api";

const byChat = new Map<string, ReadonlySet<number>>();
const asked = new Set<string>();
const listeners = new Set<() => void>();
const none: ReadonlySet<number> = new Set();

function tell() {
  for (const l of listeners) l();
}

/** Keeps a chat's pins as the server gave them. */
export function putPins(chatId: string, ids: number[]) {
  byChat.set(chatId, new Set(ids));
  tell();
}

async function load(chatId: string) {
  try {
    putPins(chatId, await listPins(chatId));
  } catch {
    // The pins are shown when they can be read. A chat that cannot be read
    // says so on its own screen.
    asked.delete(chatId);
  }
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** The ids of a chat's pinned messages. Empty until they are read. */
export function usePins(chatId: string): ReadonlySet<number> {
  if (chatId !== "" && !asked.has(chatId)) {
    asked.add(chatId);
    void load(chatId);
  }
  return useSyncExternalStore(subscribe, () => byChat.get(chatId) ?? none);
}

/** Pins a message, or takes its pin off. It throws when the server refuses. */
export async function pinMessage(chatId: string, messageId: number, pinned: boolean): Promise<void> {
  putPins(chatId, await setPin(chatId, messageId, pinned));
}
