// Every call the web interface makes to the Keystone server is in this file.
//
// The routes are under /api/v1/. The shell's (P2-05) are described in
// docs/architecture/api.md, and the chat and turn routes (P4-03) in
// docs/architecture/turns.md. docs/api/openapi.json has every shape.
//
// The session is a cookie the server sets and the page cannot read, so no
// token is ever held here.

import { readEvents } from "./sse";

const paths = {
  people: "/api/v1/signin/people",
  session: "/api/v1/session",
  chats: "/api/v1/chats",
};

const chatPath = (id: string) => `${paths.chats}/${encodeURIComponent(id)}`;
const turnsPath = (chat: string) => `${chatPath(chat)}/turns`;
const turnPath = (chat: string, turn: string) => `${turnsPath(chat)}/${encodeURIComponent(turn)}`;

export type Person = { id: string; name: string };

export type ApiErrorKind =
  | "not-ready" // the API is not there: a 404 or 405 with no route behind it, a non-JSON answer, or no connection
  | "refused" // wrong password
  | "wait" // too many wrong passwords, or too many turns waiting; try again later
  | "signed-out" // no session
  | "missing" // the chat, message or turn is not in this person's record
  | "failed"; // anything else

export class ApiError extends Error {
  kind: ApiErrorKind;
  status: number;
  /** The server's short fixed name for the failure, such as not_found. Empty when it gave none. */
  code: string;
  /** The server's own words for a person. Empty when it gave none. */
  said: string;
  constructor(kind: ApiErrorKind, status: number, code = "", said = "") {
    super(`api: ${kind} (${status}${code ? " " + code : ""})`);
    this.name = "ApiError";
    this.kind = kind;
    this.status = status;
    this.code = code;
    this.said = said;
  }
}

function kindOf(status: number, code: string): ApiErrorKind {
  if (status === 404) return code !== "" && code !== "no_route" ? "missing" : "not-ready";
  if (status === 405) return "not-ready";
  if (status === 401) return "signed-out";
  if (status === 429) return "wait";
  return "failed";
}

/** Reads the API's error body, {"error","code","request_id"}, from an answer that is not a success. */
async function errorFrom(res: Response): Promise<ApiError> {
  let code = "";
  let said = "";
  try {
    if ((res.headers.get("Content-Type") ?? "").includes("application/json")) {
      const body = (await res.json()) as { error?: unknown; code?: unknown };
      if (typeof body.code === "string") code = body.code;
      if (typeof body.error === "string") said = body.error;
    }
  } catch {
    // An error with no readable body is still an error.
  }
  return new ApiError(kindOf(res.status, code), res.status, code, said);
}

type Method = "GET" | "POST" | "PATCH" | "DELETE";

function headersFor(body: unknown, accept = "application/json"): Record<string, string> {
  return body === undefined ? { Accept: accept } : { Accept: accept, "Content-Type": "application/json" };
}

async function call(method: Method, path: string, body?: unknown): Promise<unknown> {
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      credentials: "same-origin",
      headers: headersFor(body),
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError("not-ready", 0);
  }
  if (res.status === 204) return undefined;
  if (!res.ok) throw await errorFrom(res);
  const type = res.headers.get("Content-Type") ?? "";
  if (!type.includes("application/json")) throw new ApiError("not-ready", res.status);
  try {
    return await res.json();
  } catch {
    throw new ApiError("not-ready", res.status);
  }
}

function isPerson(v: unknown): v is Person {
  if (typeof v !== "object" || v === null) return false;
  const p = v as Record<string, unknown>;
  return typeof p.id === "string" && typeof p.name === "string";
}

function personFrom(data: unknown): Person {
  const person = (data as { person?: unknown } | undefined)?.person;
  if (!isPerson(person)) throw new ApiError("not-ready", 200);
  return person;
}

/** The people on this install who can sign in. Needs no session. */
export async function listPeople(): Promise<Person[]> {
  const data = (await call("GET", paths.people)) as { people?: unknown } | undefined;
  const people = data?.people;
  if (!Array.isArray(people) || !people.every(isPerson)) throw new ApiError("not-ready", 200);
  return people;
}

/** Signs one person in with their password. The server sets the session cookie. */
export async function signIn(person: string, password: string): Promise<Person> {
  try {
    return personFrom(await call("POST", paths.session, { tenant: person, password }));
  } catch (e) {
    // At the door a 401 means the password was refused, not that a session ended.
    if (e instanceof ApiError && e.status === 401) throw new ApiError("refused", 401);
    throw e;
  }
}

/** Ends the session. */
export async function signOut(): Promise<void> {
  await call("DELETE", paths.session);
}

/** The signed-in person. Throws ApiError "signed-out" when there is no session. */
export async function me(): Promise<Person> {
  return personFrom(await call("GET", paths.session));
}

// ---------------------------------------------------------------------------
// Chats and turns. The shapes are the contract's, docs/architecture/turns.md.
// ---------------------------------------------------------------------------

export type Chat = {
  id: string;
  /** Empty until the chat has one. The first reply gives it one. */
  title: string;
  starred: boolean;
  created: string;
  /** When its last message was written. */
  active: string;
  /** A turn is queued or running in the chat. */
  busy: boolean;
};

/** One piece of a message's content, in the model seam's format. */
export type Block = {
  type: "text" | "thinking" | "tool_call" | "tool_result" | "image" | "file";
  text?: string;
  /** A tool call's id, and on a tool result the id of the call it answers. */
  id?: string;
  name?: string;
  input?: unknown;
  content?: Block[];
  is_error?: boolean;
  media_type?: string;
};

export type Message = {
  /** A number, never used twice. A later message has a larger one. */
  id: number;
  chat_id: string;
  parent_id?: number;
  role: "user" | "assistant";
  blocks: Block[];
  created: string;
  /** Every message with the same parent, this one included, oldest first. */
  siblings: number[];
  turn_id?: string;
  model?: string;
  /** end, tool_call, max_tokens, refused, stopped or failed. */
  stop_reason?: string;
};

export type ModelInfo = {
  name: string;
  label: string;
  class: string;
  /** True for the fake provider playing recorded turns. */
  test: boolean;
};

export type ModelChoice = {
  /** The model the next turn uses. Absent when there is none. */
  model?: ModelInfo;
  source: string;
  /** Why there is no model, for a person. */
  reason?: string;
  models: ModelInfo[];
};

export type TurnError = { code: string; message: string; retryable: boolean };

export type Turn = {
  id: string;
  chat_id: string;
  status: "queued" | "running" | "done" | "stopped" | "failed";
  model?: string;
  user_message_id?: number;
  reply_message_id?: number;
  error?: TurnError;
  last_event_id: number;
};

/** One chat as the screen opens it: its current branch, its model, and its turns that have not ended. */
export type OpenChat = { chat: Chat; messages: Message[]; model: ModelChoice; turns: Turn[] };

function isChat(v: unknown): v is Chat {
  if (typeof v !== "object" || v === null) return false;
  const c = v as Record<string, unknown>;
  return typeof c.id === "string" && typeof c.title === "string" && typeof c.starred === "boolean";
}

function chatFrom(data: unknown): Chat {
  if (!isChat(data)) throw new ApiError("not-ready", 200);
  return data;
}

function openChatFrom(data: unknown): OpenChat {
  const d = (data ?? {}) as Partial<OpenChat>;
  if (!isChat(d.chat)) throw new ApiError("not-ready", 200);
  return {
    chat: d.chat,
    messages: Array.isArray(d.messages) ? d.messages : [],
    model: d.model ?? { source: "none", models: [] },
    turns: Array.isArray(d.turns) ? d.turns : [],
  };
}

/** The person's chats, the one with the newest message first. `more` says the list goes on. */
export async function listChats(): Promise<{ chats: Chat[]; more: boolean }> {
  const data = (await call("GET", paths.chats)) as { chats?: unknown; more?: unknown } | undefined;
  if (!Array.isArray(data?.chats) || !data.chats.every(isChat)) throw new ApiError("not-ready", 200);
  return { chats: data.chats, more: data.more === true };
}

/** Makes a chat with no title and no messages. */
export async function createChat(): Promise<OpenChat> {
  return openChatFrom(await call("POST", paths.chats));
}

/** One chat with its current branch of messages. */
export async function openChat(id: string): Promise<OpenChat> {
  return openChatFrom(await call("GET", chatPath(id)));
}

/** Gives a chat its title. */
export async function renameChat(id: string, title: string): Promise<Chat> {
  return chatFrom(await call("PATCH", chatPath(id), { title }));
}

/** Sets or clears a chat's star. */
export async function starChat(id: string, starred: boolean): Promise<Chat> {
  return chatFrom(await call("PATCH", chatPath(id), { starred }));
}

/** Deletes a chat, with its messages, its turns and its files. */
export async function deleteChat(id: string): Promise<void> {
  await call("DELETE", chatPath(id));
}

/** What a turn's stream sends. The names and data are the contract's. */
export type TurnEvent =
  | { name: "turn" | "end"; data: Turn }
  | { name: "message"; data: Message }
  | { name: "start"; data: { step: number; model: string } }
  | { name: "thinking" | "text"; data: { step: number; index: number; text: string } }
  | { name: "tool_call"; data: { step: number; index: number; id: string; name: string; input: unknown } }
  | { name: "tool_result"; data: { id: string; is_error: boolean; content: Block[] } }
  | { name: "usage"; data: { step: number; model: string; stop_reason: string } }
  | { name: "title"; data: { chat_id: string; title: string } }
  | { name: "error"; data: TurnError };

const turnEventNames = new Set(["turn", "end", "message", "start", "thinking", "text", "tool_call", "tool_result", "usage", "title", "error"]);

export type StreamReader = {
  /** Ends the reading. It does not stop the turn: only stopTurn does. */
  signal?: AbortSignal;
  /** The turn's id, from the X-Turn-Id header, as soon as the answer opens. */
  onTurn?: (turnId: string) => void;
  /** Each event, with the id to send back as Last-Event-ID after a dropped connection. */
  onEvent: (event: TurnEvent, id: string) => void;
};

/**
 * Opens a turn's stream and reads it until it closes. It returns true when
 * the `end` event came, and false when the stream closed without one: the
 * connection dropped, and the caller comes back with followTurn. An answer
 * that is not a stream throws an ApiError.
 */
async function readTurn(method: "GET" | "POST", path: string, body: unknown, lastEventId: string, reader: StreamReader): Promise<boolean> {
  const headers = headersFor(body, "text/event-stream");
  if (lastEventId !== "") headers["Last-Event-ID"] = lastEventId;
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      credentials: "same-origin",
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: reader.signal,
    });
  } catch {
    throw new ApiError("not-ready", 0);
  }
  if (!res.ok) throw await errorFrom(res);
  if (!(res.headers.get("Content-Type") ?? "").includes("text/event-stream") || !res.body) {
    throw new ApiError("not-ready", res.status);
  }
  const turnId = res.headers.get("X-Turn-Id");
  if (turnId) reader.onTurn?.(turnId);

  let ended = false;
  try {
    await readEvents(res.body, (e) => {
      if (!turnEventNames.has(e.name)) return; // an event a later server adds
      let data: unknown;
      try {
        data = JSON.parse(e.data);
      } catch {
        return;
      }
      if (e.name === "end") ended = true;
      reader.onEvent({ name: e.name, data } as TurnEvent, e.id);
    });
  } catch {
    // The connection broke in the middle. The caller decides what is next.
    return ended;
  }
  return ended;
}

/** Sends a message to a chat and reads the reply's stream. See readTurn for what it returns. */
export function sendMessage(chatId: string, text: string, reader: StreamReader): Promise<boolean> {
  return readTurn("POST", turnsPath(chatId), { text }, "", reader);
}

/**
 * Reads a turn's stream from after the event `lastEventId`, or from its first
 * event when that is empty. It is how the screen joins a turn it did not
 * send and how it comes back after a dropped connection.
 */
export function followTurn(chatId: string, turnId: string, lastEventId: string, reader: StreamReader): Promise<boolean> {
  return readTurn("GET", `${turnPath(chatId, turnId)}/events`, undefined, lastEventId, reader);
}

/** Stops a turn. What had streamed is kept, and the stream ends with `end`. */
export async function stopTurn(chatId: string, turnId: string): Promise<void> {
  await call("POST", `${turnPath(chatId, turnId)}/stop`);
}
