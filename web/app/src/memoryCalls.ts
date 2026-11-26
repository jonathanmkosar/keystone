// The calls of the Memory screen and of incognito chats (P6-07). The routes
// are in docs/architecture/memory-screens.md, and their shapes in
// docs/api/openapi.json. Kept apart from api.ts so the two can change
// without touching each other.

import { ApiError, call, type OpenChat } from "./api";

export type MemoryStore = {
  /** none, built-in or waymarks. */
  adapter: string;
  /** Forgetting takes a source out of the store. When false, Keystone withdraws it. */
  can_remove: boolean;
  /** The store takes new sources, so kept and corrected facts can be put back. */
  can_add: boolean;
  /** What forgetting does in this store, for a person. */
  note: string;
  unavailable?: string;
};

export type Remembered = {
  source_id: string;
  chat_id: string;
  chat_title: string;
  chat_gone: boolean;
  turn_id: string;
  facts: string[];
  added: string;
};

export type MemoryPage = { id: string; path: string; title: string; kind?: string };

export type MemorySpace = {
  name: string;
  sensitive: boolean;
  memories: Remembered[];
  pages: MemoryPage[];
  more_pages: number;
};

export type MemoryOverview = {
  store: MemoryStore;
  profile: string;
  map: string;
  spaces: MemorySpace[];
  more: number;
};

export type MemoryChange = {
  change: { source_id: string; removed: boolean; new_source_id?: string; facts: string[]; space: string; sensitive: boolean };
  profile_changed: boolean;
  note: string;
};

const memoryPath = "/api/v1/memory";
const chatPath = (id: string) => `/api/v1/chats/${encodeURIComponent(id)}`;

function overviewFrom(data: unknown): MemoryOverview {
  const d = (data ?? {}) as Partial<MemoryOverview>;
  if (typeof d.store !== "object" || d.store === null || !Array.isArray(d.spaces)) throw new ApiError("not-ready", 200);
  return {
    store: d.store,
    profile: typeof d.profile === "string" ? d.profile : "",
    map: typeof d.map === "string" ? d.map : "",
    spaces: d.spaces.map((s) => ({ ...s, memories: Array.isArray(s.memories) ? s.memories : [], pages: Array.isArray(s.pages) ? s.pages : [] })),
    more: typeof d.more === "number" ? d.more : 0,
  };
}

/** What the person's knowledge store remembers, by space, with the profile and the map. */
export async function memoryOverview(): Promise<MemoryOverview> {
  return overviewFrom(await call("GET", memoryPath));
}

/** Forgets one fact the memory pass added. */
export async function forgetFact(sourceId: string, fact: string): Promise<MemoryChange> {
  return (await call("POST", `${memoryPath}/forget`, { source_id: sourceId, fact })) as MemoryChange;
}

/** Corrects one fact the memory pass added. */
export async function correctFact(sourceId: string, fact: string, text: string): Promise<MemoryChange> {
  return (await call("POST", `${memoryPath}/correct`, { source_id: sourceId, fact, text })) as MemoryChange;
}

/** Makes an incognito chat: no memory, no recap, not found by past-chat search, deleted when left. */
export async function createIncognitoChat(): Promise<OpenChat> {
  const d = (await call("POST", "/api/v1/chats/incognito")) as Partial<OpenChat> | undefined;
  if (!d?.chat) throw new ApiError("not-ready", 200);
  return { chat: d.chat, messages: d.messages ?? [], model: d.model ?? { source: "none", models: [] }, turns: d.turns ?? [] };
}

/** Whether a chat is incognito. */
export async function isIncognito(chatId: string): Promise<boolean> {
  const d = (await call("GET", `${chatPath(chatId)}/memory`)) as { incognito?: unknown } | undefined;
  return d?.incognito === true;
}

/** Tells the server the person left an incognito chat, so it is deleted. */
export async function closeIncognito(chatId: string): Promise<void> {
  await call("POST", `${chatPath(chatId)}/close`);
}

/** The same, for a page that is going away: the browser sends it after the page has gone. */
export function closeIncognitoAsPageGoes(chatId: string): boolean {
  return typeof navigator.sendBeacon === "function" && navigator.sendBeacon(`${chatPath(chatId)}/close`);
}
