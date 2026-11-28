// The turns this page is following: for each chat, the reply that is
// arriving. It is kept outside the screens, so a reply goes on arriving
// while the person looks at another chat, and is there when they come back.
//
// One turn's events are folded into a Live value here. The stream itself is
// read by api.ts. Decision 0151 says why this is a small store of its own.

import { useSyncExternalStore } from "react";
import { ApiError, followTurn, openChat, sendMessage, stopTurn, strongerTurn, type Block, type Message, type StreamReader, type TurnEvent, type TurnRoute } from "./api";
import { strongerProblem } from "./autoWords";
import { policyProblem } from "./policyWords";

/** A block of the reply that is still being written. */
export type DraftBlock =
  | { type: "text"; text: string }
  | { type: "thinking"; text: string; open: boolean; started: number; seconds?: number }
  | { type: "tool_call"; id: string; name: string; input: string };

export type LiveState =
  | "sending" // the message is on its way
  | "waiting" // the turn is queued behind an earlier one
  | "streaming" // the reply is arriving
  | "reconnecting" // the connection dropped and is being opened again
  | "done"
  | "stopped"
  | "failed";

export type Live = {
  chatId: string;
  /** Empty until the server names the turn. */
  turnId: string;
  state: LiveState;
  /** The person asked for the turn to stop, and it has not ended yet. */
  stopping: boolean;
  /** The person's message, shown until the record has it. */
  said: string;
  /** That message went with the person's location (P10-06). */
  located: boolean;
  /** Messages the turn has written to the record, in order. */
  written: Message[];
  /** The reply being written now, by block. A stored message replaces it. */
  draft: DraftBlock[];
  /** What each tool gave back, by the call's id. */
  results: Record<string, { text: string; failed: boolean }>;
  /** Why the turn failed, in words for a person. Empty otherwise. */
  problem: string;
  /** The turn ended and the screen has taken its messages. Only a failure is kept this long. */
  settled: boolean;
  /** How the turn was routed, when the chat is on Auto (P14-04). */
  route?: TurnRoute;
};

const active: LiveState[] = ["sending", "waiting", "streaming", "reconnecting"];

/** True while the turn has not ended. */
export function isActive(live: Live | undefined): boolean {
  return live !== undefined && active.includes(live.state);
}

type Entry = { live: Live; lastEventId: string; abort: AbortController; wantStop: boolean };

const entries = new Map<string, Entry>();
const listeners = new Set<() => void>();
const titleListeners = new Set<(chatId: string, title: string) => void>();
const endListeners = new Set<(chatId: string) => void>();
let signedOut: () => void = () => {};

// How long each thinking block took, by message id and block index, for the
// replies this page saw arrive. The record does not keep it.
const thoughtSeconds = new Map<string, number>();

/** The seconds a thinking block took, if this page watched it arrive. */
export function thoughtFor(messageId: number, index: number): number | undefined {
  return thoughtSeconds.get(`${messageId}:${index}`);
}

// Text arrives in many small pieces. The screens are told at most once every
// few milliseconds, so a fast reply does not redraw for every piece.
let pending: number | undefined;
function tell() {
  if (pending !== undefined) return;
  pending = window.setTimeout(() => {
    pending = undefined;
    for (const l of listeners) l();
  }, 24);
}

function change(entry: Entry, patch: Partial<Live>) {
  entry.live = { ...entry.live, ...patch };
  tell();
}

function textOf(blocks: { type: string; text?: string }[] | undefined): string {
  return (blocks ?? [])
    .filter((b) => b.type === "text" && b.text)
    .map((b) => b.text)
    .join("\n\n");
}

function closeThinking(draft: DraftBlock[]): DraftBlock[] {
  return draft.map((b) => (b.type === "thinking" && b.open ? { ...b, open: false, seconds: (Date.now() - b.started) / 1000 } : b));
}

const plain: Record<string, string> = {
  no_model: "There is no model to answer. Add one in the model settings, then send the message again.",
  model_unavailable: "The model could not be opened. Its key may be missing, or this build may not include its adapter.",
  too_long: "This chat no longer fits in the model's context window. Start a new chat, or pick a model with a larger window.",
  auth: "The model's provider refused Keystone's key.",
  rate_limited: "The model's provider is limiting requests. Wait a moment, then send the message again.",
  overloaded: "The model's provider is overloaded. Wait a moment, then send the message again.",
  unavailable: "The model could not be reached. Send the message again.",
  step_limit: "The model asked for tools more times than one turn allows, so the turn was ended.",
  interrupted: "Keystone stopped while this reply was being written. Send the message again.",
  failed: "The turn failed inside Keystone. The reason is in the server's log.",
};

function problemFrom(code: string, message: string): string {
  // The server's message is written for a person. The fixed text is for an
  // error that came with none.
  return message || plain[code] || "The turn failed.";
}

function apply(entry: Entry, event: TurnEvent) {
  const live = entry.live;
  switch (event.name) {
    case "turn": {
      const patch: Partial<Live> = { turnId: event.data.id };
      if (event.data.status === "queued") patch.state = "waiting";
      else if (event.data.status === "running") patch.state = "streaming";
      change(entry, patch);
      return;
    }
    case "message": {
      const m = event.data;
      const written = live.written.filter((w) => w.id !== m.id).concat(m);
      const patch: Partial<Live> = { written };
      if (m.role === "user") {
        if (m.blocks.some((b) => b.type !== "tool_result")) patch.said = "";
        const results = { ...live.results };
        for (const b of m.blocks) {
          if (b.type === "tool_result" && b.id) results[b.id] = { text: textOf(b.content), failed: b.is_error === true };
        }
        patch.results = results;
      } else {
        // The stored message replaces the draft. Keep how long it thought.
        closeThinking(live.draft).forEach((b, i) => {
          if (b.type === "thinking" && b.seconds !== undefined) thoughtSeconds.set(`${m.id}:${i}`, b.seconds);
        });
        patch.draft = [];
      }
      change(entry, patch);
      return;
    }
    case "start":
      change(entry, { draft: [], state: live.state === "reconnecting" ? live.state : "streaming" });
      return;
    case "thinking":
    case "text": {
      const { index, text } = event.data;
      let draft = live.draft;
      const at = draft[index];
      if (at && at.type === event.name) {
        draft = draft.slice();
        draft[index] = { ...at, text: at.text + text };
      } else {
        draft = closeThinking(draft);
        draft[index] = event.name === "text" ? { type: "text", text } : { type: "thinking", text, open: true, started: Date.now() };
      }
      change(entry, { draft });
      return;
    }
    case "tool_call": {
      const draft = closeThinking(live.draft);
      let input = "";
      try {
        input = JSON.stringify(event.data.input ?? {});
      } catch {
        input = "";
      }
      draft[event.data.index] = { type: "tool_call", id: event.data.id, name: event.data.name, input };
      change(entry, { draft });
      return;
    }
    case "tool_result":
      change(entry, {
        results: { ...live.results, [event.data.id]: { text: textOf(event.data.content), failed: event.data.is_error === true } },
      });
      return;
    case "usage":
      return;
    case "route": // P14-04: the reply's footnote while it arrives
      change(entry, { route: event.data });
      return;
    case "title":
      for (const l of titleListeners) l(event.data.chat_id, event.data.title);
      return;
    case "error":
      change(entry, { problem: problemFrom(event.data.code, event.data.message) });
      return;
    case "end": {
      const status = event.data.status;
      const state: LiveState = status === "stopped" ? "stopped" : status === "failed" ? "failed" : "done";
      let problem = live.problem;
      if (state === "failed" && !problem) problem = problemFrom(event.data.error?.code ?? "", event.data.error?.message ?? "");
      change(entry, { state, stopping: false, draft: closeThinking(live.draft), problem });
      return;
    }
  }
}

function fail(entry: Entry, problem: string) {
  change(entry, { state: "failed", stopping: false, draft: closeThinking(entry.live.draft), problem });
}

function wait(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const timer = window.setTimeout(resolve, ms);
    signal.addEventListener("abort", () => {
      window.clearTimeout(timer);
      resolve();
    });
  });
}

// How long to wait before each try at opening a dropped stream again.
const retryWaits = [400, 1000, 2000, 4000, 8000, 8000];

function sendProblem(e: unknown): string {
  if (!(e instanceof ApiError)) return "The message could not be sent.";
  if (e.code === "no_stronger" || e.code === "not_auto") return strongerProblem(e.code, e.said ?? ""); // P14-04
  if (policyProblem(e.code, e.said ?? "")) return policyProblem(e.code, e.said ?? ""); // P14-05: the admin's router policy
  if (e.code === "queue_full") return "This chat already has messages waiting for a reply. Wait for one to finish, then send again.";
  if (e.code === "not_built") return "The server cannot answer yet: this part of it is not built.";
  if (e.kind === "missing") return "This chat is no longer in your record. It may have been deleted.";
  if (e.status === 400 && e.said) return `The message was refused: ${e.said}.`;
  if (e.status === 0) return "Keystone did not answer, so the message was not sent. Check that the server is running, then send it again.";
  return e.said ? `The message could not be sent: ${e.said}.` : "The message could not be sent.";
}

/** Reads one turn to its end, opening the stream again each time it drops. */
async function run(entry: Entry, begin: (reader: StreamReader) => Promise<boolean>, sending: boolean) {
  const chatId = entry.live.chatId;
  const signal = entry.abort.signal;
  const reader: StreamReader = {
    signal,
    onTurn: (turnId) => {
      change(entry, { turnId });
      if (entry.wantStop) void requestStop(entry);
    },
    onEvent: (event, id) => {
      if (id !== "") entry.lastEventId = id;
      apply(entry, event);
      if (event.name === "turn" && entry.wantStop) void requestStop(entry);
    },
  };

  let ended = false;
  try {
    ended = await begin(reader);
  } catch (e) {
    if (signal.aborted) return;
    if (e instanceof ApiError && e.kind === "signed-out") {
      fail(entry, "You are signed out. Sign in, then send the message again.");
      signedOut();
      return;
    }
    // No answer at all leaves it unknown whether the message arrived. The
    // record knows: if it holds a turn that has not ended, join it.
    let joined = false;
    if (sending && e instanceof ApiError && e.status === 0) {
      try {
        const turn = (await openChat(chatId)).turns.at(-1);
        if (turn) {
          change(entry, { turnId: turn.id, state: "reconnecting" });
          joined = true;
        }
      } catch {
        // Still no answer.
      }
    }
    if (!joined) {
      fail(entry, sending ? sendProblem(e) : "The reply could not be read. Reload the page to see what the record holds.");
      for (const l of endListeners) l(chatId);
      return;
    }
  }

  let tries = 0;
  while (!ended && !signal.aborted) {
    if (entry.live.turnId === "") {
      fail(entry, "The connection dropped before Keystone named the turn. Reload the page to see whether the message arrived.");
      break;
    }
    if (tries >= retryWaits.length) {
      fail(entry, "The connection to Keystone was lost and did not come back. The reply may have gone on without this page: reload to see it.");
      break;
    }
    change(entry, { state: "reconnecting" });
    await wait(retryWaits[tries], signal);
    if (signal.aborted) return;
    const before = entry.lastEventId;
    try {
      ended = await followTurn(chatId, entry.live.turnId, entry.lastEventId, {
        ...reader,
        onEvent: (event, id) => {
          // Events are arriving again.
          if (entry.live.state === "reconnecting") change(entry, { state: "streaming" });
          reader.onEvent(event, id);
        },
      });
    } catch (e) {
      if (signal.aborted) return;
      if (e instanceof ApiError && e.kind === "signed-out") {
        fail(entry, "You are signed out. Sign in to see the rest of the reply.");
        signedOut();
        return;
      }
      if (e instanceof ApiError && e.kind === "missing") {
        fail(entry, "This turn is no longer in your record. The chat may have been deleted.");
        break;
      }
    }
    tries = entry.lastEventId === before ? tries + 1 : 0;
  }
  if (signal.aborted) return;
  for (const l of endListeners) l(chatId);
  tell();
}

async function requestStop(entry: Entry) {
  if (entry.live.turnId === "" || !isActive(entry.live)) return;
  entry.wantStop = false;
  try {
    await stopTurn(entry.live.chatId, entry.live.turnId);
  } catch {
    // The stream says how the turn ended. If the stop did not arrive the
    // button is offered again.
    if (isActive(entry.live)) change(entry, { stopping: false });
  }
}

function begin(chatId: string, live: Partial<Live>): Entry {
  entries.get(chatId)?.abort.abort();
  const entry: Entry = {
    live: { chatId, turnId: "", state: "sending", stopping: false, said: "", located: false, written: [], draft: [], results: {}, problem: "", settled: false, ...live },
    lastEventId: "",
    abort: new AbortController(),
    wantStop: false,
  };
  entries.set(chatId, entry);
  tell();
  return entry;
}

/** Sends a message to a chat and follows the reply. `model` names the model to answer; absent means the chat's own. `blocks`, when given, makes the message's blocks first: its files uploaded (P9-02, uploads.ts). `location` is the rounded position that goes with this message alone (P10-06). */
export function send(chatId: string, text: string, model?: string, blocks?: () => Promise<Block[]>, location?: { latitude: number; longitude: number }): void {
  const entry = begin(chatId, { said: text, located: location !== undefined });
  void run(entry, async (reader) => sendMessage(chatId, text, reader, model, blocks && (await blocks()), location), true);
}

/** Try a stronger model (P14-04): writes a turn on Auto again one step up, and follows it as a send. */
export function stronger(chatId: string, turnId: string): void {
  if (isActive(entries.get(chatId)?.live)) return;
  const entry = begin(chatId, {});
  void run(entry, (reader) => strongerTurn(chatId, turnId, reader), true);
}

/** Joins a turn that is already queued or running, from its first event. */
export function join(chatId: string, turnId: string): void {
  if (isActive(entries.get(chatId)?.live)) return;
  const entry = begin(chatId, { turnId, state: "streaming" });
  void run(entry, (reader) => followTurn(chatId, turnId, "", reader), false);
}

/** Asks the server to stop the chat's turn. The stream says when it has. */
export function stop(chatId: string): void {
  const entry = entries.get(chatId);
  if (!entry || !isActive(entry.live)) return;
  change(entry, { stopping: true });
  if (entry.live.turnId === "") entry.wantStop = true;
  else void requestStop(entry);
}

/**
 * Called once the screen holds the messages of a turn that has ended. The
 * turn is forgotten, except for a failure's words, which stay until the next
 * message is sent.
 */
export function settle(chatId: string): void {
  const entry = entries.get(chatId);
  if (!entry || isActive(entry.live) || entry.live.settled) return;
  if (entry.live.state === "failed" && entry.live.problem) {
    // The failure stays on the screen, with a message that never reached
    // the record and whatever of the reply was not stored.
    change(entry, { written: [], settled: true });
  } else {
    entries.delete(chatId);
    tell();
  }
}

/** Forgets a chat's turn, for a chat that was deleted. */
export function forget(chatId: string): void {
  const entry = entries.get(chatId);
  if (!entry) return;
  entry.abort.abort();
  entries.delete(chatId);
  tell();
}

/** Stops following every turn, when the person signs out. The turns go on. */
export function reset(): void {
  for (const entry of entries.values()) entry.abort.abort();
  entries.clear();
  tell();
}

/** Called with a chat's automatic title when the server makes one. */
export function onTitle(fn: (chatId: string, title: string) => void): () => void {
  titleListeners.add(fn);
  return () => titleListeners.delete(fn);
}

/** Called when a chat's turn ends, however it ended. */
export function onEnd(fn: (chatId: string) => void): () => void {
  endListeners.add(fn);
  return () => endListeners.delete(fn);
}

/** Called when a call finds that the session has ended. */
export function onSignedOut(fn: () => void): () => void {
  signedOut = fn;
  return () => {
    signedOut = () => {};
  };
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** The turn this page is following in a chat, if there is one. */
export function useLive(chatId: string | undefined): Live | undefined {
  return useSyncExternalStore(subscribe, () => (chatId ? entries.get(chatId)?.live : undefined));
}
