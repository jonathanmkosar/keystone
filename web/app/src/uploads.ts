// Uploads (P9-02): the files and images a person adds to a chat. The calls
// to the upload routes are here, beside the small store of each chat's
// uploads and notices that the conversation draws from.
// docs/architecture/uploads.md has the server's side.
//
// Files are held in the composer and uploaded when the message is sent,
// then named in the message by their ids: the server keeps each under its
// hash, and a message's image or file block carries that hash as its ref.

import { useEffect, useSyncExternalStore } from "react";
import { ApiError, type Block } from "./api";
import { onEnd } from "./turns";

export type Upload = {
  /** The file's SHA-256: its name on the server, and the ref a block names it by. */
  id: string;
  chat_id: string;
  name: string;
  /** image: sent to a model that can see; text: its text is read; document: sent whole to a model that takes files. */
  kind: "image" | "text" | "document";
  media_type: string;
  size: number;
  /** Characters of text read from a text file. */
  chars: number;
  created: string;
  /** A message of the chat holds it, so it cannot be removed. */
  in_use: boolean;
};

/** What the person is told about a reply and the uploads of the message it answers. */
export type Notice = {
  turn_id: string;
  message_id: number;
  /** withheld: the reply did not get an image or file; moved: a fallback that can see answered. */
  kind: "withheld" | "moved";
  upload_id?: string;
  name?: string;
  text: string;
};

/** What may be uploaded. */
export type Limits = { max_bytes: number; image_types: string[]; text_extensions: string[]; document_types: string[] };

const uploadsPath = (chat: string) => `/api/v1/chats/${encodeURIComponent(chat)}/uploads`;

/** The address an upload's bytes are served at, from Keystone's own origin, which the content security policy allows. */
export function contentURL(chatId: string, id: string): string {
  return `${uploadsPath(chatId)}/${encodeURIComponent(id)}/content`;
}

async function failure(res: Response): Promise<ApiError> {
  let code = "";
  let said = "";
  try {
    const body = (await res.json()) as { code?: unknown; error?: unknown };
    if (typeof body.code === "string") code = body.code;
    if (typeof body.error === "string") said = body.error;
  } catch {
    // An error with no readable body is still an error.
  }
  const kind = res.status === 401 ? "signed-out" : res.status === 404 && code !== "no_route" && code !== "" ? "missing" : res.status === 404 ? "not-ready" : "failed";
  return new ApiError(kind, res.status, code, said);
}

async function json<T>(method: string, path: string, body?: BodyInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, { method, credentials: "same-origin", headers: { Accept: "application/json" }, body });
  } catch {
    throw new ApiError("not-ready", 0);
  }
  if (!res.ok) throw await failure(res);
  return (await res.json()) as T;
}

/** Uploads one file to a chat. A file the chat already has comes back as it is. */
export async function uploadFile(chatId: string, file: File): Promise<Upload> {
  return json<Upload>("POST", `${uploadsPath(chatId)}?name=${encodeURIComponent(file.name)}`, file);
}

/** Takes an upload off a chat. Refused while a message holds it. */
export async function removeUpload(chatId: string, id: string): Promise<void> {
  await json("DELETE", `${uploadsPath(chatId)}/${encodeURIComponent(id)}`);
}

let limitsAsked: Promise<Limits> | undefined;

/** What may be uploaded, asked once. */
export function uploadLimits(): Promise<Limits> {
  limitsAsked ??= json<Limits>("GET", "/api/v1/uploads/limits").catch((e: unknown) => {
    limitsAsked = undefined;
    throw e;
  });
  return limitsAsked;
}

/** The block a message names an upload with, as the server writes it. */
export function blockOf(u: Upload): Block {
  return u.kind === "image" ? { type: "image", media_type: u.media_type, ref: u.id } : { type: "file", media_type: u.media_type, name: u.name, ref: u.id };
}

/**
 * Uploads a message's files, then returns the message as blocks: the files
 * first, as a model reads best, then the words. A file the server refuses
 * throws, and the message is not sent.
 */
export async function messageWith(chatId: string, text: string, files: File[]): Promise<Block[]> {
  const blocks: Block[] = [];
  for (const f of files) blocks.push(blockOf(await uploadFile(chatId, f)));
  if (text !== "") blocks.push({ type: "text", text });
  void refreshUploads(chatId);
  return blocks;
}

/** Why a file cannot be uploaded, in words for a person, or "" when it may be. The server checks again. */
export function refusal(file: File, limits: Limits | undefined): string {
  if (!limits) return "";
  if (file.size === 0) return `${file.name} is empty.`;
  if (file.size > limits.max_bytes) return `${file.name} is ${size(file.size)}, and the most one file may be is ${size(limits.max_bytes)}.`;
  const dot = file.name.lastIndexOf(".");
  const ending = dot >= 0 ? file.name.slice(dot).toLowerCase() : "";
  const type = file.type.toLowerCase();
  if (limits.image_types.includes(type) || limits.document_types.includes(type) || limits.text_extensions.includes(ending)) return "";
  return `Keystone does not take ${file.name}. It takes images, PDF documents and text files.`;
}

/** The accept list of the file picker. */
export function acceptOf(limits: Limits | undefined): string | undefined {
  return limits ? [...limits.image_types, ...limits.document_types, ...limits.text_extensions].join(",") : undefined;
}

/** A number of bytes for a person. */
export function size(n: number): string {
  if (n < 1000) return `${n} bytes`;
  if (n < 1_000_000) return `${Math.round(n / 1000)} kB`;
  return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, "")} MB`;
}

// ---------------------------------------------------------------------------
// Each chat's uploads and notices, read when the chat is drawn and again
// when one of its turns ends.
// ---------------------------------------------------------------------------

export type ChatUploads = { uploads: Map<string, Upload>; notices: Notice[] };

const empty: ChatUploads = { uploads: new Map(), notices: [] };
const known = new Map<string, ChatUploads>();
const listeners = new Set<() => void>();

function tell() {
  for (const l of listeners) l();
}

async function refreshUploads(chatId: string): Promise<void> {
  try {
    const data = await json<{ uploads?: Upload[]; notices?: Notice[] }>("GET", uploadsPath(chatId));
    known.set(chatId, { uploads: new Map((data.uploads ?? []).map((u) => [u.id, u])), notices: data.notices ?? [] });
    tell();
  } catch {
    // What is on the screen stays. A server without uploads has none.
  }
}

onEnd((chatId) => {
  if (known.has(chatId)) void refreshUploads(chatId);
});

/** A chat's uploads and notices, read when the chat is first drawn. */
export function useChatUploads(chatId: string): ChatUploads {
  useEffect(() => {
    if (chatId !== "") void refreshUploads(chatId);
  }, [chatId]);
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    () => known.get(chatId) ?? empty,
  );
}
