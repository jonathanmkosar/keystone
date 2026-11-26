// The calls of styles (P9-05): the style picker in the composer and the
// Styles settings screen. The routes are in docs/architecture/styles.md, and
// their shapes in docs/api/openapi.json. Kept apart from api.ts so the two
// can change without touching each other.

import { ApiError, call } from "./api";

export type Style = {
  id: string;
  name: string;
  /** How replies should be written. A built-in style may have none, and then sends nothing. */
  instructions: string;
  /** Every person has it, and none can change or delete it. */
  built_in: boolean;
  created?: string;
  changed?: string;
};

export type StyleLimits = { max_styles: number; name_max_chars: number; instructions_max_chars: number };

export type StyleList = {
  /** The built-in styles in their order, then the person's own by name. */
  styles: Style[];
  /** The id of the style a new chat uses. It may name no style. */
  default: string;
  limits: StyleLimits;
};

export type ChatStyle = {
  chat_id: string;
  /** The style the chat's next call carries. Absent when it uses none. */
  style?: Style;
  /** chat: the chat's own; default: the person's default, until its first turn keeps it; none. */
  source: "chat" | "default" | "none";
  default: string;
  styles: Style[];
};

const stylesPath = "/api/v1/styles";
const stylePath = (id: string) => `${stylesPath}/${encodeURIComponent(id)}`;
const chatStylePath = (chatId: string) => `/api/v1/chats/${encodeURIComponent(chatId)}/style`;

function isStyle(v: unknown): v is Style {
  if (typeof v !== "object" || v === null) return false;
  const s = v as Record<string, unknown>;
  return typeof s.id === "string" && typeof s.name === "string" && typeof s.instructions === "string" && typeof s.built_in === "boolean";
}

function styleFrom(data: unknown): Style {
  if (!isStyle(data)) throw new ApiError("not-ready", 200);
  return data;
}

function listFrom(data: unknown): StyleList {
  const d = (data ?? {}) as Partial<StyleList>;
  if (!Array.isArray(d.styles) || !d.styles.every(isStyle) || typeof d.default !== "string" || typeof d.limits !== "object" || d.limits === null) {
    throw new ApiError("not-ready", 200);
  }
  return { styles: d.styles, default: d.default, limits: d.limits };
}

function chatStyleFrom(data: unknown): ChatStyle {
  const d = (data ?? {}) as Partial<ChatStyle>;
  if (typeof d.chat_id !== "string" || !Array.isArray(d.styles) || !d.styles.every(isStyle) || typeof d.source !== "string") {
    throw new ApiError("not-ready", 200);
  }
  return { chat_id: d.chat_id, style: isStyle(d.style) ? d.style : undefined, source: d.source, default: d.default ?? "", styles: d.styles };
}

/** Every style the person may choose, the default for new chats, and the limits. */
export async function listStyles(): Promise<StyleList> {
  return listFrom(await call("GET", stylesPath));
}

/** Makes a style of the person's own. */
export async function createStyle(name: string, instructions: string): Promise<Style> {
  return styleFrom(await call("POST", stylesPath, { name, instructions }));
}

/** Renames one of the person's own styles, or changes its instructions. */
export async function changeStyle(id: string, change: { name?: string; instructions?: string }): Promise<Style> {
  return styleFrom(await call("PATCH", stylePath(id), change));
}

/** Deletes one of the person's own styles. */
export async function deleteStyle(id: string): Promise<void> {
  await call("DELETE", stylePath(id));
}

/** Makes a style the default for new chats. */
export async function setDefaultStyle(id: string): Promise<StyleList> {
  return listFrom(await call("PUT", `${stylePath(id)}/default`));
}

/** The style a chat uses, and every style it may use. */
export async function chatStyle(chatId: string): Promise<ChatStyle> {
  return chatStyleFrom(await call("GET", chatStylePath(chatId)));
}

/** Chooses a chat's style. Its next call carries it. */
export async function chooseChatStyle(chatId: string, styleId: string): Promise<ChatStyle> {
  return chatStyleFrom(await call("PUT", chatStylePath(chatId), { style: styleId }));
}
