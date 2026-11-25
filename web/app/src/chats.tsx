// The person's list of chats, shared by the sidebar, the header and the
// chats screen, so a new title or a star shows in all three at once.

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { ApiError, listChats, type Chat } from "./api";
import { onEnd, onSignedOut, onTitle, reset } from "./turns";

type Chats = {
  status: "loading" | "ready" | "failed";
  /** Newest message first, as the server lists them. */
  chats: Chat[];
  /** The server has more chats than this list holds. */
  more: boolean;
  /** Reads the list from the server again. */
  refresh: () => void;
  /** Adds a chat, or replaces the one with its id. `front` moves it to the top, as a new message does. */
  put: (chat: Chat, front?: boolean) => void;
  /** Takes a chat out of the list. */
  drop: (id: string) => void;
  /** A call found that the session has ended. */
  lost: () => void;
};

const Context = createContext<Chats | undefined>(undefined);

export function ChatsProvider({ onSignedOut: lost, children }: { onSignedOut: () => void; children: ReactNode }) {
  const [status, setStatus] = useState<Chats["status"]>("loading");
  const [chats, setChats] = useState<Chat[]>([]);
  const [more, setMore] = useState(false);

  const refresh = useCallback(() => {
    listChats().then(
      (list) => {
        setChats(list.chats);
        setMore(list.more);
        setStatus("ready");
      },
      (e) => {
        if (e instanceof ApiError && e.kind === "signed-out") lost();
        else setStatus((s) => (s === "ready" ? s : "failed"));
      },
    );
  }, [lost]);

  const put = useCallback((chat: Chat, front = false) => {
    setChats((list) => {
      const at = list.findIndex((c) => c.id === chat.id);
      if (at < 0) return [chat, ...list];
      if (front) return [chat, ...list.slice(0, at), ...list.slice(at + 1)];
      const next = list.slice();
      next[at] = chat;
      return next;
    });
  }, []);

  const drop = useCallback((id: string) => setChats((list) => list.filter((c) => c.id !== id)), []);

  useEffect(() => {
    refresh();
    const offTitle = onTitle((id, title) => setChats((list) => list.map((c) => (c.id === id ? { ...c, title } : c))));
    // A turn that ended moved its chat to the top, and may have named it.
    const offEnd = onEnd(() => refresh());
    const offLost = onSignedOut(lost);
    return () => {
      offTitle();
      offEnd();
      offLost();
      // Leaving the signed-in screens: stop following every reply.
      reset();
    };
  }, [refresh, lost]);

  const value = useMemo(() => ({ status, chats, more, refresh, put, drop, lost }), [status, chats, more, refresh, put, drop, lost]);
  return <Context.Provider value={value}>{children}</Context.Provider>;
}

export function useChats(): Chats {
  const value = useContext(Context);
  if (!value) throw new Error("useChats is used outside ChatsProvider");
  return value;
}

/** What a chat is called on the screen before it has a title. */
export function titleOf(chat: Chat | undefined): string {
  return chat?.title || "New chat";
}
