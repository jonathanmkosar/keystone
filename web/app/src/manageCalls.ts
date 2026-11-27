// The calls of the Chats screen, the table of every chat (P4-06). The routes
// are in docs/architecture/manage-chats.md, and their shapes in
// docs/api/openapi.json. Kept apart from api.ts so the two can change
// without touching each other.

import { ApiError, call, type Imported } from "./api";

/** A piece of a title or a snippet. `match` marks the words the search found. */
export type Part = { text: string; match?: boolean };

/** What the search found in one chat. */
export type RowMatch = {
  /** The chat's title holds the words. */
  title: boolean;
  /** The title cut into parts, when it matched. */
  title_parts?: Part[];
  /** How many of its messages hold the words, on every branch. */
  messages: number;
  /** The best of them, and who wrote it. */
  message_id?: number;
  role?: "user" | "assistant";
  /** The words around the match in that message. */
  snippet?: Part[];
};

/** One chat as the table shows it. */
export type TableRow = {
  id: string;
  title: string;
  /** The summary of its recap, once it has one. */
  summary?: string;
  created: string;
  /** When its last message was written. */
  active: string;
  /** Every message of the chat, on every branch. */
  messages: number;
  /** A turn is queued or running in it. */
  busy: boolean;
  /** A background task or a sub-agent that has not ended works in it or for it. */
  working: boolean;
  starred: boolean;
  imported?: Imported;
  project?: { id: string; name: string };
  /** A background task's or a scheduled run's own chat. */
  task?: boolean;
  /** A sub-agent's own chat. */
  subagent?: boolean;
  /** It was sent content from a sensitive space, so only a local or trusted model answers in it. */
  held?: boolean;
  /** How many share links to it are open. */
  shared?: number;
  incognito?: boolean;
  /** Present while searching. */
  match?: RowMatch;
};

export const sorts = ["active", "title", "messages", "match"] as const;
export type Sort = (typeof sorts)[number];
export type Order = "asc" | "desc";

export const filters = ["starred", "imported", "project", "task", "subagent"] as const;
export type Filter = (typeof filters)[number];

/** What the table is asked for. */
export type TableAsk = {
  query: string;
  sort: Sort;
  order: Order;
  filters: Filter[];
};

export type TablePage = {
  rows: TableRow[];
  /** How many chats match the search and the filters. */
  total: number;
  more: boolean;
  /** The order the server used: `match` only stands while searching. */
  sort: Sort;
  order: Order;
  /** Rows a page: the chats.table_page_rows setting. */
  page_rows: number;
  /** The most chats one request deletes: chats.delete_batch_max. */
  delete_max: number;
};

const tablePath = "/api/v1/chats/table";
const deletePath = "/api/v1/chats/delete";

function queryOf(ask: TableAsk): URLSearchParams {
  const q = new URLSearchParams();
  if (ask.query.trim()) q.set("q", ask.query.trim());
  q.set("sort", ask.sort);
  q.set("order", ask.order);
  if (ask.filters.length > 0) q.set("filter", ask.filters.join(","));
  return q;
}

function isRow(v: unknown): v is TableRow {
  if (typeof v !== "object" || v === null) return false;
  const r = v as Record<string, unknown>;
  return typeof r.id === "string" && typeof r.title === "string" && typeof r.active === "string" && typeof r.messages === "number";
}

/** One page of the table, from `offset`. */
export async function chatTable(ask: TableAsk, offset: number): Promise<TablePage> {
  const q = queryOf(ask);
  if (offset > 0) q.set("offset", String(offset));
  const d = (await call("GET", `${tablePath}?${q}`)) as Partial<TablePage> | undefined;
  if (!d || !Array.isArray(d.rows) || !d.rows.every(isRow) || typeof d.total !== "number") throw new ApiError("not-ready", 200);
  return {
    rows: d.rows,
    total: d.total,
    more: d.more === true,
    sort: sorts.includes(d.sort as Sort) ? (d.sort as Sort) : ask.sort,
    order: d.order === "asc" ? "asc" : "desc",
    page_rows: typeof d.page_rows === "number" ? d.page_rows : d.rows.length,
    delete_max: typeof d.delete_max === "number" && d.delete_max > 0 ? d.delete_max : 1,
  };
}

/** The ids of every chat that matches, in the table's order, for "select all that match". `cut` says there were more than the server gives at once. */
export async function matchingIds(ask: TableAsk): Promise<{ ids: string[]; total: number; cut: boolean }> {
  const q = queryOf(ask);
  q.set("ids", "true");
  q.set("limit", "1");
  const d = (await call("GET", `${tablePath}?${q}`)) as { ids?: unknown; total?: unknown; ids_cut?: unknown } | undefined;
  if (!d || !Array.isArray(d.ids) || !d.ids.every((x) => typeof x === "string")) throw new ApiError("not-ready", 200);
  return { ids: d.ids as string[], total: typeof d.total === "number" ? d.total : d.ids.length, cut: d.ids_cut === true };
}

/** A chat one delete left, and why. */
export type Skipped = {
  id: string;
  title?: string;
  /** turn_running, task_running, not_found or failed. */
  reason: string;
  /** The same, in words for a person. */
  why: string;
};

export type Deleted = { deleted: string[]; skipped: Skipped[] };

/** Deletes up to `delete_max` chats in one request, each with its messages, files, artifacts and share links. */
export async function deleteChats(ids: string[]): Promise<Deleted> {
  const d = (await call("POST", deletePath, { ids })) as Partial<Deleted> | undefined;
  if (!d || !Array.isArray(d.deleted) || !Array.isArray(d.skipped)) throw new ApiError("not-ready", 200);
  return { deleted: d.deleted, skipped: d.skipped };
}
