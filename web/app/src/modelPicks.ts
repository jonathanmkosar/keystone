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
// Auto (P14-04) is a pick like a model: its name is autoPick, which no
// registry name can be, and it goes with the next message as `auto`. A model
// picked in a chat on Auto takes the chat off Auto.
//
// Whether a chat is held to local or trusted models comes from the model
// route (`held`) since P14-04; the browser's own record of held chats,
// learned from a refused turn, is gone (decision 0696).

import { useSyncExternalStore } from "react";
import type { ModelChoice, ModelInfo } from "./api";
import { autoPick } from "./autoWords";
import { allowedByHand, autoRequired } from "./policyWords";

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

/** Whether Auto is what will answer: Auto picked, or no pick in a chat on Auto (P14-04). */
export function showsAuto(choice: ModelChoice | undefined, pick: Pick | undefined): boolean {
  if (pick) return pick.name === autoPick && (choice?.ladder?.length ?? 0) > 0;
  return choice?.auto === true;
}

/**
 * A pick the chat may make: Auto while there is a ladder, or one of its
 * models the admin lets the person pick by hand, while they may leave Auto
 * (P14-05, policyWords.ts). A pick the server would refuse is let go.
 */
function mayPick(choice: ModelChoice, name: string): boolean {
  if (name === autoPick) return (choice.ladder?.length ?? 0) > 0;
  return choice.models.some((m) => m.name === name) && allowedByHand(choice, name) && !autoRequired(choice);
}

/** A pick that is already the server's choice: Auto on a chat on Auto, or the chat's model off Auto. */
function isServers(choice: ModelChoice | undefined, name: string): boolean {
  return name === autoPick ? choice?.auto === true : choice?.auto !== true && name === choice?.model?.name;
}

/**
 * Keeps an answer of the model route. A pick the answer makes needless goes:
 * one the chat may no longer name, one that is already the server's choice,
 * and one that was sent, when the answer was asked for after its turn ended.
 */
export function rememberChoice(key: string, choice: ModelChoice, idle: boolean) {
  let pick = entries.get(key)?.pick;
  if (pick && (!mayPick(choice, pick.name) || isServers(choice, pick.name) || (pick.sent && idle))) {
    pick = undefined;
  }
  put(key, { choice, pick });
}

/** The person picked a model, or Auto. Picking the server's own choice takes the pick back. */
export function pickModel(key: string, name: string) {
  const entry = entries.get(key) ?? {};
  put(key, { ...entry, pick: isServers(entry.choice, name) ? undefined : { name, sent: false } });
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
  const auto = showsAuto(choice, entry?.pick);
  return model === choice.model && auto === (choice.auto === true) ? choice : { ...choice, model, auto };
}
