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

type Method = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

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
      if (e.name === "approval") return tellApproval(e.data); // P7-02: for the approval card, Approval.tsx
      if (!turnEventNames.has(e.name)) return true; // an event a later server adds
      let data: unknown;
      try {
        data = JSON.parse(e.data);
      } catch {
        return true;
      }
      if (e.name === "end") ended = true;
      reader.onEvent({ name: e.name, data } as TurnEvent, e.id);
      // `end` is always the last event, so the reading stops there.
      return !ended;
    });
  } catch {
    // The connection broke in the middle. The caller decides what is next.
    return ended;
  }
  return ended;
}

/** Sends a message to a chat and reads the reply's stream. `model` names the model to answer; absent means the chat's own. See readTurn for what it returns. */
export function sendMessage(chatId: string, text: string, reader: StreamReader, model?: string): Promise<boolean> {
  return readTurn("POST", turnsPath(chatId), model ? { text, model } : { text }, "", reader);
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

// The settings screens (P11-01). The routes are described in
// docs/architecture/settings-screens.md.

const settingsPaths = {
  settings: "/api/v1/settings",
  secrets: "/api/v1/secrets",
  models: "/api/v1/models",
  profile: "/api/v1/profile",
};

/** A call whose answer must be a JSON object. */
async function object(method: Method, path: string, body?: unknown): Promise<Record<string, unknown>> {
  const data = await call(method, path, body);
  if (typeof data !== "object" || data === null || Array.isArray(data)) throw new ApiError("not-ready", 200);
  return data as Record<string, unknown>;
}

export type SettingType = "bool" | "int" | "float" | "string" | "duration";

/** One setting as it stands for the signed-in person. Values are text, read as the type says. */
export type Setting = {
  key: string;
  lane: string;
  type: SettingType;
  default: string;
  /** The values a string setting may take. Empty means any. */
  options: string[];
  scope: "install" | "tenant-admin" | "tenant";
  description: string;
  value: string;
  /** The value is the person's own and not the default. */
  overridden: boolean;
  /** The signed-in person may change it. */
  changeable: boolean;
};

/** One of the four fixed rules. It is not a setting and has no value. */
export type FixedRule = { key: string; rule: string };

function isSetting(v: unknown): v is Setting {
  if (typeof v !== "object" || v === null) return false;
  const s = v as Record<string, unknown>;
  return (
    typeof s.key === "string" &&
    typeof s.lane === "string" &&
    typeof s.type === "string" &&
    typeof s.default === "string" &&
    Array.isArray(s.options) &&
    typeof s.description === "string" &&
    typeof s.value === "string" &&
    typeof s.overridden === "boolean" &&
    typeof s.changeable === "boolean"
  );
}

function settingFrom(data: unknown): Setting {
  if (!isSetting(data)) throw new ApiError("not-ready", 200);
  return data;
}

/** Every setting, with the person's own values, and the fixed rules. */
export async function listSettings(): Promise<{ settings: Setting[]; rules: FixedRule[] }> {
  const data = await object("GET", settingsPaths.settings);
  if (!Array.isArray(data.settings) || !data.settings.every(isSetting)) throw new ApiError("not-ready", 200);
  const rules = Array.isArray(data.rules) ? (data.rules as FixedRule[]) : [];
  return { settings: data.settings, rules };
}

/** Stores the person's own value for a setting. */
export async function setSetting(key: string, value: string): Promise<Setting> {
  return settingFrom(await call("PUT", `${settingsPaths.settings}/${encodeURIComponent(key)}`, { value }));
}

/** Removes the person's own value, so the default is back. */
export async function resetSetting(key: string): Promise<Setting> {
  return settingFrom(await call("DELETE", `${settingsPaths.settings}/${encodeURIComponent(key)}`));
}

/** The name of a stored secret. The server never sends a value, so there is no field for one. */
export type SecretName = { name: string; used_by: string[] };

/** The names of the person's stored secrets. */
export async function listSecrets(): Promise<SecretName[]> {
  const data = await object("GET", settingsPaths.secrets);
  if (!Array.isArray(data.secrets)) throw new ApiError("not-ready", 200);
  return data.secrets as SecretName[];
}

/** Stores a secret under a name. The value is sent and never read back. */
export async function setSecret(name: string, value: string): Promise<void> {
  await call("PUT", `${settingsPaths.secrets}/${encodeURIComponent(name)}`, { value });
}

export async function deleteSecret(name: string): Promise<void> {
  await call("DELETE", `${settingsPaths.secrets}/${encodeURIComponent(name)}`);
}

/** A model registry entry, as docs/architecture/models.md describes it. key is a secret's name, never the secret. */
export type ModelEntry = {
  name: string;
  label: string;
  adapter: string;
  endpoint?: string;
  provider_model?: string;
  key?: string;
  context_window: number;
  output_limit: number;
  capabilities?: string[];
  price: { input: number; output: number; cache_read?: number; cache_write?: number };
  class: string;
  ca_cert?: string;
};

export type RegisteredModel = {
  entry: ModelEntry;
  /** A secret with the entry's key name is stored. */
  key_present: boolean;
  may_read_sensitive: boolean;
  /** Why a stored entry is refused today. Such a row has only its name. */
  problem?: string;
};

export type ModelRegistry = {
  models: RegisteredModel[];
  adapters: { name: string; speaks: string; in_process: boolean }[];
  classes: string[];
  capabilities: string[];
  max: number;
};

export async function listModels(): Promise<ModelRegistry> {
  const data = await object("GET", settingsPaths.models);
  if (!Array.isArray(data.models) || !Array.isArray(data.adapters) || !Array.isArray(data.classes) || !Array.isArray(data.capabilities)) {
    throw new ApiError("not-ready", 200);
  }
  return data as unknown as ModelRegistry;
}

export async function addModel(entry: ModelEntry): Promise<void> {
  await call("POST", settingsPaths.models, entry);
}

export async function replaceModel(entry: ModelEntry): Promise<void> {
  await call("PUT", `${settingsPaths.models}/${encodeURIComponent(entry.name)}`, entry);
}

export async function removeModel(name: string): Promise<void> {
  await call("DELETE", `${settingsPaths.models}/${encodeURIComponent(name)}`);
}

/** The short text about the person that starts every call to a model. */
export type Profile = { text: string; updated?: string; max_chars: number };

function profileFrom(data: Record<string, unknown>): Profile {
  if (typeof data.text !== "string" || typeof data.max_chars !== "number") throw new ApiError("not-ready", 200);
  return data as unknown as Profile;
}

export async function readProfile(): Promise<Profile> {
  return profileFrom(await object("GET", settingsPaths.profile));
}

export async function writeProfile(text: string): Promise<Profile> {
  return profileFrom(await object("PUT", settingsPaths.profile, { text }));
}

// The model picker in the composer (P4-04). The routes are the chat
// contract's, docs/architecture/turns.md.

function isModelInfo(v: unknown): v is ModelInfo {
  if (typeof v !== "object" || v === null) return false;
  const m = v as Record<string, unknown>;
  return typeof m.name === "string" && typeof m.label === "string" && typeof m.class === "string";
}

/** Which model a chat's next turn uses when the turn names none, and the models a turn in it may name. */
export async function chatModel(chatId: string): Promise<ModelChoice> {
  const data = await object("GET", `${chatPath(chatId)}/model`);
  if (!Array.isArray(data.models) || !data.models.every(isModelInfo) || typeof data.source !== "string") throw new ApiError("not-ready", 200);
  if (data.model !== undefined && !isModelInfo(data.model)) throw new ApiError("not-ready", 200);
  return {
    model: data.model as ModelInfo | undefined,
    source: data.source,
    reason: typeof data.reason === "string" ? data.reason : undefined,
    models: data.models,
  };
}

/** One turn as it stands, with the reason it failed if it did. */
export async function getTurn(chatId: string, turnId: string): Promise<Turn> {
  const data = await object("GET", turnPath(chatId, turnId));
  if (typeof data.id !== "string" || typeof data.status !== "string") throw new ApiError("not-ready", 200);
  return data as unknown as Turn;
}

// ---------------------------------------------------------------------------
// Approvals (P7-02). A tool call that needs the person's answer arrives as an
// `approval` event in a turn's stream; readTurn hands it to tellApproval, and
// Approval.tsx keeps and draws it. docs/architecture/approvals.md.
// ---------------------------------------------------------------------------

/** One tool call that needs, or had, the person's answer. */
export type Approval = {
  id: string;
  turn_id: string;
  chat_id: string;
  /** The tool call's id, as its tool_call event gave it. */
  call_id: string;
  tool: string;
  /** What the call will do, in the server's words. */
  summary: string;
  input: unknown;
  /** policy: the person's policy says to ask. outside: the chat read outside content. */
  reason: string;
  /** Why it asks, as a sentence. */
  why: string;
  status: "pending" | "approved" | "declined" | "expired" | "stopped";
  asked: string;
  decided?: string;
};

function isApproval(v: unknown): v is Approval {
  if (typeof v !== "object" || v === null) return false;
  const a = v as Record<string, unknown>;
  return typeof a.id === "string" && typeof a.turn_id === "string" && typeof a.chat_id === "string" && typeof a.tool === "string" && typeof a.status === "string";
}

const approvalListeners = new Set<(a: Approval) => void>();

/** Called with each approval event of any turn this page reads. */
export function onApproval(fn: (a: Approval) => void): () => void {
  approvalListeners.add(fn);
  return () => approvalListeners.delete(fn);
}

/** Passes an approval event on. It returns true, so the stream reads on. */
function tellApproval(data: string): boolean {
  try {
    const a: unknown = JSON.parse(data);
    if (isApproval(a)) for (const l of approvalListeners) l(a);
  } catch {
    // An event that does not read is passed over.
  }
  return true;
}

const approvalPath = (a: Pick<Approval, "chat_id" | "turn_id" | "id">) => `${turnPath(a.chat_id, a.turn_id)}/approvals/${encodeURIComponent(a.id)}`;

/** The person's answer to one call. The stream brings the change too. */
export async function answerApproval(a: Approval, answer: "approve" | "decline"): Promise<Approval> {
  const data = await call("POST", `${approvalPath(a)}/${answer}`);
  if (!isApproval(data)) throw new ApiError("not-ready", 200);
  return data;
}

/** One tool, with the person's policy for it. */
export type ToolPolicy = {
  name: string;
  description: string;
  read_only: boolean;
  /** Its result is Keystone's own, not outside content. */
  inside: boolean;
  default: string;
  policy: string;
  overridden: boolean;
};

function toolPolicyFrom(data: unknown): ToolPolicy {
  const t = data as Partial<ToolPolicy> | undefined;
  if (!t || typeof t.name !== "string" || typeof t.policy !== "string") throw new ApiError("not-ready", 200);
  return t as ToolPolicy;
}

/** Every tool a turn may use, with the person's policy for each, and the policies there are. */
export async function listToolPolicies(): Promise<{ tools: ToolPolicy[]; policies: string[] }> {
  const data = await object("GET", "/api/v1/tools");
  if (!Array.isArray(data.tools) || !Array.isArray(data.policies)) throw new ApiError("not-ready", 200);
  return { tools: data.tools.map(toolPolicyFrom), policies: data.policies as string[] };
}

export async function setToolPolicy(name: string, policy: string): Promise<ToolPolicy> {
  return toolPolicyFrom(await call("PUT", `/api/v1/tools/${encodeURIComponent(name)}/policy`, { policy }));
}

export async function resetToolPolicy(name: string): Promise<ToolPolicy> {
  return toolPolicyFrom(await call("DELETE", `/api/v1/tools/${encodeURIComponent(name)}/policy`));
}

// ---------------------------------------------------------------------------
// The turn inspector (P5-04), and the pins of P5-02. What went into each
// model call of a turn, what the chat's next turn leaves out, and the chat's
// pinned messages. docs/architecture/inspector.md has the server's side.
// ---------------------------------------------------------------------------

/** One piece of a part of a call: a recalled item, a pinned message. */
export type ItemRecord = {
  label?: string;
  /** The inspector's name for it: page:7, source:3, message:42. */
  ref?: string;
  tokens: number;
  kept: boolean;
  pinned?: boolean;
  /** A pinned message whose turn was sent, so it was not sent on its own. */
  in_turn?: boolean;
  /** The person took it out of this call. */
  removed?: boolean;
};

/** One part of a call, as the context builder recorded it. */
export type PartRecord = {
  part: string;
  place: string;
  /** empty, kept, partly or dropped. */
  state: string;
  tokens: number;
  kept: number;
  dropped: number;
  dropped_tokens: number;
  covers?: number;
  removed?: number;
  /** On the summary: summary:12. */
  ref?: string;
  items?: ItemRecord[];
};

/** One turn of the chat's branch, and whether the call sent it. */
export type TurnRecord = { first: number; messages: number; tokens: number; part: string; kept: boolean; summarised?: boolean };

/** What one call was built from. */
export type ContextRecord = {
  model: string;
  window: number;
  output: number;
  room: number;
  limit: number;
  tokens: number;
  margin: number;
  exact: boolean;
  over: number;
  parts: PartRecord[];
  turns: TurnRecord[];
};

/** One model call's usage record. */
export type UsageRecord = {
  id: number;
  at: string;
  kind: string;
  model: string;
  class: string;
  /** ok, stopped, refused or error. */
  outcome: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
  thinking_tokens?: number;
  estimated?: boolean;
  cost_micros: number;
  duration_ms: number;
};

export type Totals = {
  calls: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  thinking_tokens: number;
  cost_micros: number;
  estimated: boolean;
};

/** One model call of a turn. step is set on a chat call; kind says what the call was for. */
export type InspectedCall = {
  step?: number;
  kind: string;
  model?: string;
  context?: ContextRecord;
  /** The first words of each turn of context.turns, in the same order. */
  previews?: string[];
  usage: UsageRecord[];
  total: Totals;
};

/** One hit of the turn's search of the knowledge store. */
export type RecallHit = { type: string; id: string; space: string; title: string; score: number; sensitive: boolean; kept: boolean; reason?: string };

export type RecallRecord = {
  adapter?: string;
  note?: string;
  query?: string;
  found: number;
  kept: number;
  dropped: number;
  withheld: number;
  sensitive: boolean;
  hits?: RecallHit[];
};

/** A summary of older turns, as stored with the chat. */
export type ChatSummary = { id: number; turns: number; text: string; model: string; sensitive: boolean; tokens: number; created: string };

/** One item the chat's next turn leaves out. */
export type Skip = { kind: string; ref: string; label: string; asked: string; turn_id?: string };

export type InspectedTurn = Turn & {
  stop_reason?: string;
  usage?: { input_tokens?: number; output_tokens?: number };
  cost_micros?: number;
  created?: string;
  ended?: string;
};

export type Inspection = {
  turn: InspectedTurn;
  calls: InspectedCall[];
  recall?: RecallRecord;
  summaries: ChatSummary[];
  totals: Totals;
  pinned: number[];
  skips: Skip[];
};

/** What went into each model call of one turn. */
export async function inspectTurn(chatId: string, turnId: string): Promise<Inspection> {
  const data = await object("GET", `${turnPath(chatId, turnId)}/inspect`);
  if (typeof data.turn !== "object" || !Array.isArray(data.calls) || typeof data.totals !== "object") throw new ApiError("not-ready", 200);
  return {
    turn: data.turn as InspectedTurn,
    calls: data.calls as InspectedCall[],
    recall: (data.recall ?? undefined) as RecallRecord | undefined,
    summaries: Array.isArray(data.summaries) ? (data.summaries as ChatSummary[]) : [],
    totals: data.totals as Totals,
    pinned: Array.isArray(data.pinned) ? (data.pinned as number[]) : [],
    skips: Array.isArray(data.skips) ? (data.skips as Skip[]) : [],
  };
}

function idsFrom(data: Record<string, unknown>): number[] {
  if (!Array.isArray(data.pinned)) throw new ApiError("not-ready", 200);
  return data.pinned.filter((n): n is number => typeof n === "number");
}

/** The ids of a chat's pinned messages, oldest first. */
export async function listPins(chatId: string): Promise<number[]> {
  return idsFrom(await object("GET", `${chatPath(chatId)}/pins`));
}

/** Pins a message, or takes its pin off, and returns the chat's pins. */
export async function setPin(chatId: string, messageId: number, pinned: boolean): Promise<number[]> {
  return idsFrom(await object(pinned ? "PUT" : "DELETE", `${chatPath(chatId)}/pins/${messageId}`));
}

function skipsFrom(data: Record<string, unknown>): Skip[] {
  if (!Array.isArray(data.skips)) throw new ApiError("not-ready", 200);
  return data.skips as Skip[];
}

/** What the chat's next turn leaves out. */
export async function listSkips(chatId: string): Promise<Skip[]> {
  return skipsFrom(await object("GET", `${chatPath(chatId)}/skips`));
}

/** Takes an item out of the chat's next turn, or puts it back, and returns what the next turn leaves out. */
export async function setSkip(chatId: string, kind: string, ref: string, skipped: boolean): Promise<Skip[]> {
  return skipsFrom(await object(skipped ? "PUT" : "DELETE", `${chatPath(chatId)}/skips/${encodeURIComponent(kind)}/${encodeURIComponent(ref)}`));
}

// Background tasks (P8-01). A task works in a chat of its own and writes its
// result in the chat it was started from; Tasks.tsx draws it there.
// docs/architecture/jobs.md.
// ---------------------------------------------------------------------------

export type TaskError = { code: string; message: string };

/** One background task, as the server sends it. */
export type Task = {
  id: string;
  /** The chat it was started from. Its result is written there. */
  chat_id: string;
  /** The chat of its own its turns run in. */
  work_chat_id: string;
  title: string;
  task: string;
  started_by: "person" | "tool";
  status: "queued" | "running" | "done" | "stopped" | "failed";
  /** A call of its round waits for the person's approval, in its own chat. */
  waiting: boolean;
  /** Where it is, in the server's few words. */
  progress: string;
  rounds: number;
  max_rounds: number;
  steps: number;
  turn_id?: string;
  budget_micros: number;
  spent_micros: number;
  result?: string;
  error?: TaskError;
  /** The message in chat_id that holds its result, once written. */
  note_message_id?: number;
  created: string;
  started?: string;
  ended?: string;
  last_event_id: number;
};

const tasksPath = (chat: string) => `${chatPath(chat)}/tasks`;

function taskFrom(data: unknown): Task {
  const t = data as Partial<Task> | undefined;
  if (!t || typeof t.id !== "string" || typeof t.chat_id !== "string" || typeof t.status !== "string") throw new ApiError("not-ready", 200);
  return t as Task;
}

/** The tasks started from a chat or working in it, oldest first. */
export async function listTasks(chatId: string): Promise<Task[]> {
  const data = await object("GET", tasksPath(chatId));
  if (!Array.isArray(data.tasks)) throw new ApiError("not-ready", 200);
  return data.tasks.map(taskFrom);
}

/** Starts a task from a chat. */
export async function startTask(chatId: string, text: string, title?: string): Promise<Task> {
  return taskFrom(await call("POST", tasksPath(chatId), title ? { text, title } : { text }));
}

/** Stops a task. Its round is stopped and the chat it was started from is told. */
export async function stopTask(chatId: string, taskId: string): Promise<Task> {
  return taskFrom(await call("POST", `${tasksPath(chatId)}/${encodeURIComponent(taskId)}/stop`));
}

/**
 * Reads a task's stream from after the event `lastEventId`, or from its
 * first event when that is empty, calling onTask with the task each time it
 * changes. It returns true when the `end` event came, and false when the
 * stream closed without one: the connection dropped, and the caller comes
 * back with the last id it had.
 */
export async function followTask(chatId: string, taskId: string, lastEventId: string, onTask: (task: Task, id: string, end: boolean) => void, signal?: AbortSignal): Promise<boolean> {
  const headers = headersFor(undefined, "text/event-stream");
  if (lastEventId !== "") headers["Last-Event-ID"] = lastEventId;
  let res: Response;
  try {
    res = await fetch(`${tasksPath(chatId)}/${encodeURIComponent(taskId)}/events`, { method: "GET", credentials: "same-origin", headers, signal });
  } catch {
    throw new ApiError("not-ready", 0);
  }
  if (!res.ok) throw await errorFrom(res);
  if (!(res.headers.get("Content-Type") ?? "").includes("text/event-stream") || !res.body) throw new ApiError("not-ready", res.status);
  let ended = false;
  try {
    await readEvents(res.body, (e) => {
      if (e.name !== "task" && e.name !== "end") return true;
      try {
        const task = taskFrom(JSON.parse(e.data));
        ended = e.name === "end";
        onTask(task, e.id, ended);
      } catch {
        return true;
      }
      return !ended;
    });
  } catch {
    return ended;
  }
  return ended;
}

// ---------------------------------------------------------------------------
// Scheduled tasks (P8-03). A schedule's every run is a background task that
// works in a chat of its own; Scheduled.tsx draws them.
// docs/architecture/schedules.md.
// ---------------------------------------------------------------------------

export type Recurrence = {
  kind: "hourly" | "daily" | "weekdays" | "weekly" | "once";
  /** HH:MM, for every kind but hourly. */
  time?: string;
  /** Minutes past the hour, for hourly. */
  minute?: number;
  /** mon … sun, for weekly. */
  days?: string[];
  /** YYYY-MM-DD, for once. */
  date?: string;
};

/** One run of a schedule. Its times carry the person's zone's offset. */
export type ScheduleRun = {
  id: string;
  schedule_id: string;
  trigger: "schedule" | "now";
  due: string;
  started: string;
  late: boolean;
  late_reason?: "stopped" | "busy";
  missed: number;
  status: "starting" | "queued" | "running" | "done" | "stopped" | "failed" | "gone";
  waiting: boolean;
  progress?: string;
  chat_id?: string;
  task_id?: string;
  error?: string;
  steps: number;
  spent_micros: number;
  began?: string;
  ended?: string;
};

export type Schedule = {
  id: string;
  name: string;
  prompt: string;
  recurrence: Recurrence;
  /** The recurrence in words. */
  says: string;
  /** A registry name. Absent means a new chat's own model. */
  model?: string;
  incognito: boolean;
  enabled: boolean;
  /** The person's switch that holds every schedule is on. */
  held: boolean;
  /** The next due time, in the person's zone. Absent while paused or held, or once it has run. */
  next?: string;
  last_run?: ScheduleRun;
  created: string;
  updated: string;
};

export type ScheduleZone = { name?: string; label: string; problem?: string };

export type SchedulesReply = { schedules: Schedule[]; paused: boolean; zone: ScheduleZone; max: number };

export type ScheduleReply = { schedule: Schedule; runs: ScheduleRun[]; zone: ScheduleZone };

export type ScheduleBody = {
  name: string;
  prompt: string;
  recurrence: Recurrence;
  model?: string;
  incognito?: boolean;
  enabled?: boolean;
};

const schedulesPath = "/api/v1/schedules";
const schedulePath = (id: string) => `${schedulesPath}/${encodeURIComponent(id)}`;

function schedulesFrom(data: unknown): SchedulesReply {
  const d = data as Partial<SchedulesReply> | undefined;
  if (!d || !Array.isArray(d.schedules) || typeof d.paused !== "boolean" || !d.zone) throw new ApiError("not-ready", 200);
  return d as SchedulesReply;
}

function scheduleFrom(data: unknown): Schedule {
  const s = data as Partial<Schedule> | undefined;
  if (!s || typeof s.id !== "string" || typeof s.name !== "string" || !s.recurrence) throw new ApiError("not-ready", 200);
  return s as Schedule;
}

/** The person's schedules, oldest first, with the switch and the zone. */
export async function listSchedules(): Promise<SchedulesReply> {
  return schedulesFrom(await call("GET", schedulesPath));
}

/** One schedule and its newest runs, newest first. */
export async function getSchedule(id: string): Promise<ScheduleReply> {
  const data = (await object("GET", schedulePath(id))) as Partial<ScheduleReply>;
  if (!Array.isArray(data.runs) || !data.zone) throw new ApiError("not-ready", 200);
  return { schedule: scheduleFrom(data.schedule), runs: data.runs, zone: data.zone };
}

export async function createSchedule(body: ScheduleBody): Promise<Schedule> {
  return scheduleFrom(await call("POST", schedulesPath, body));
}

/** Replaces every field of a schedule. */
export async function updateSchedule(id: string, body: ScheduleBody): Promise<Schedule> {
  return scheduleFrom(await call("PUT", schedulePath(id), body));
}

/** Deletes a schedule and the record of its runs. Each run's chat stays. */
export async function deleteSchedule(id: string): Promise<void> {
  await call("DELETE", schedulePath(id));
}

/** Pauses or resumes one schedule. Resumed, nothing paused is made up. */
export async function setScheduleEnabled(id: string, on: boolean): Promise<Schedule> {
  return scheduleFrom(await call("POST", `${schedulePath(id)}/${on ? "resume" : "pause"}`));
}

/** Runs a schedule now, in a chat of its own. */
export async function runScheduleNow(id: string): Promise<ScheduleRun> {
  const run = (await call("POST", `${schedulePath(id)}/run`)) as Partial<ScheduleRun> | undefined;
  if (!run || typeof run.id !== "string") throw new ApiError("not-ready", 200);
  return run as ScheduleRun;
}

/** Turns the switch that holds every schedule on or off. */
export async function holdSchedules(on: boolean): Promise<SchedulesReply> {
  return schedulesFrom(await call("POST", `${schedulesPath}/${on ? "pause-all" : "resume-all"}`));
}
