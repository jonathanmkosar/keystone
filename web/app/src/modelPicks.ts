// The model picker's state, kept outside the screens like the replies in
// turns.ts, so a pick survives moving between chats and the footnote and
// the picker read the same thing.
//
// For each chat it holds two things:
//
// - the newest answer of the chat's model route this page read, and
// - a model the person picked that the server has not taken yet.
//
// The server remembers a chat's model by itself: a turn that names a model
// and opens it writes the name on the turn, and the chat's next turn and its
// model route use the model of its last turn (internal/turn/model.go). So a
// pick is sent once, with the next message, and is let go as soon as the
// model route is read again after that turn ends. From then on the server's
// answer is what the picker shows. Decision 0240.
//
// It also holds the chats this page has learned are held to local or trusted
// models. The model route does not say so. A turn sent to a model that may
// not read sensitive content, in a chat that was sent some, fails with the
// code `sensitive`, and the picker marks the chat when it sees that. A chat
// stays held for good, so the mark is kept in the browser's local storage.

import { useSyncExternalStore } from "react";
import type { ModelChoice, ModelInfo } from "./api";

export type Pick = {
  /** The registry name of the model picked. */
  name: string;
  /** It went with a message, and waits for the server's next answer. */
  sent: boolean;
};

export type PickEntry = {
  /** The newest answer of the chat's model route this page read. */
  choice?: ModelChoice;
  /** A model the person picked that the server has not taken yet. */
  pick?: Pick;
};

const entries = new Map<string, PickEntry>();
const listeners = new Set<() => void>();

function tell() {
  for (const l of listeners) l();
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

function put(key: string, entry: PickEntry) {
  entries.set(key, entry);
  tell();
}

/** The key the new chat screen keeps its pick under, for one person, since it has no chat yet. */
export function newChatKey(personId: string): string {
  return `new:${personId}`;
}

/** A chat's entry. The object is the same until something in it changes. */
export function usePickEntry(key: string): PickEntry | undefined {
  return useSyncExternalStore(subscribe, () => entries.get(key));
}

/** Whether a model may be sent content from a sensitive space. The server's rule: local or trusted. */
export function mayReadSensitive(m: ModelInfo): boolean {
  return m.class === "local" || m.class === "trusted";
}

/** The model that will answer: the pick, while the chat may name it, or the server's choice. */
export function shownModel(choice: ModelChoice | undefined, pick: Pick | undefined): ModelInfo | undefined {
  if (choice && pick) {
    const picked = choice.models.find((m) => m.name === pick.name);
    if (picked) return picked;
  }
  return choice?.model;
}

/**
 * Keeps an answer of the model route. A pick the answer makes needless goes:
 * one the chat may no longer name, one that is already the server's choice,
 * and one that was sent, when the answer was asked for after its turn ended.
 */
export function rememberChoice(key: string, choice: ModelChoice, idle: boolean) {
  let pick = entries.get(key)?.pick;
  if (pick && (!choice.models.some((m) => m.name === pick!.name) || pick.name === choice.model?.name || (pick.sent && idle))) {
    pick = undefined;
  }
  put(key, { choice, pick });
}

/** The person picked a model. Picking the server's own choice takes the pick back. */
export function pickModel(key: string, name: string) {
  const entry = entries.get(key) ?? {};
  put(key, { ...entry, pick: name === entry.choice?.model?.name ? undefined : { name, sent: false } });
}

/** The model to name in the message being sent, if the person picked one, which is then marked as sent. */
export function takePick(chatId: string): string | undefined {
  const entry = entries.get(chatId);
  if (!entry?.pick) return undefined;
  if (!entry.pick.sent) put(chatId, { ...entry, pick: { ...entry.pick, sent: true } });
  return entry.pick.name;
}

/**
 * The new chat screen's pick, for the chat its first message made: it moves
 * to that chat as sent, and its name is returned to go with the message.
 */
export function adoptNewChatPick(personId: string, chatId: string): string | undefined {
  const key = newChatKey(personId);
  const from = entries.get(key);
  if (!from?.pick) return undefined;
  put(key, { ...from, pick: undefined });
  put(chatId, { ...entries.get(chatId), pick: { name: from.pick.name, sent: true } });
  return from.pick.name;
}

/** The choice the footnote names: the server's, or the newest this page read, with the pick in place of its model. */
export function useShownChoice(chatId: string, fallback: ModelChoice | undefined): ModelChoice | undefined {
  const entry = usePickEntry(chatId);
  const choice = entry?.choice ?? fallback;
  if (!choice) return undefined;
  const model = shownModel(choice, entry?.pick);
  return model === choice.model ? choice : { ...choice, model };
}

// Chats held to local or trusted models, as this browser learned them.

const heldKey = "keystone.chats.held";

function readHeld(): Set<string> {
  try {
    const raw = window.localStorage.getItem(heldKey);
    const ids: unknown = raw ? JSON.parse(raw) : [];
    return new Set(Array.isArray(ids) ? ids.filter((id): id is string => typeof id === "string") : []);
  } catch {
    return new Set();
  }
}

const held = readHeld();

/** True once this browser has seen a turn of the chat refused because it holds sensitive content. */
export function useHeld(chatId: string): boolean {
  return useSyncExternalStore(subscribe, () => held.has(chatId));
}

/** Marks a chat as held to local or trusted models, for good. */
export function markHeld(chatId: string) {
  if (held.has(chatId)) return;
  held.add(chatId);
  try {
    window.localStorage.setItem(heldKey, JSON.stringify([...held]));
  } catch {
    // Without storage the mark lasts until the page is reloaded.
  }
  tell();
}
