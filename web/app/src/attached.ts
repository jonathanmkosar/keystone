// A chat's attached chats and its links (P5-06), read from the server and
// kept outside the screens, as modelPicks.ts keeps the model picker's
// state, so the header's links and the composer's chips show the same.
// docs/architecture/attach-and-handoff.md describes the routes.

import { useEffect, useSyncExternalStore } from "react";
import { ApiError, listAttached, type Attached } from "./api";
import { onEnd } from "./turns";

const entries = new Map<string, Attached>();
const listeners = new Set<() => void>();

function tell() {
  for (const l of listeners) l();
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** Keeps the server's newest answer for a chat. */
export function keepAttached(a: Attached) {
  entries.set(a.chat_id, a);
  tell();
}

/**
 * A chat's attached chats and links. They are read when a screen first asks,
 * and again when a turn of the chat ends, since a turn may have given an
 * attached chat its summary.
 */
export function useAttached(chatId: string, lost: () => void): Attached | undefined {
  const value = useSyncExternalStore(subscribe, () => entries.get(chatId));
  useEffect(() => {
    if (!chatId) return;
    let live = true;
    const read = () =>
      listAttached(chatId).then(
        (a) => {
          if (live) keepAttached(a);
        },
        (e) => {
          if (e instanceof ApiError && e.kind === "signed-out") lost();
          // Otherwise what is on the screen stays, or nothing is drawn.
        },
      );
    void read();
    const off = onEnd((id) => {
      if (id === chatId) void read();
    });
    return () => {
      live = false;
      off();
    };
  }, [chatId, lost]);
  return value;
}
