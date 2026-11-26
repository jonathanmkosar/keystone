// Share links (P10-04): the person's calls, through api.ts's call, with the
// session. The shared page itself (Shared.tsx) reads a link without one.
// The routes are in docs/architecture/share.md.

import { call } from "./api";

export type ShareView = "live" | "snapshot";

export type ShareLink = {
  id: string;
  chat_id: string;
  chat_title: string;
  view: ShareView;
  created: string;
  expires?: string;
  revoked?: string;
  views: number;
  last_viewed?: string;
  /** open, revoked, expired, or refused when its chat is now held or incognito. */
  state: "open" | "revoked" | "expired" | "refused";
  reason?: string;
};

/** A new link. The token is in it this once, and is never kept. */
export type MadeLink = { link: ShareLink; token: string; path: string };

const chatShares = (chatId: string) => `/api/v1/chats/${encodeURIComponent(chatId)}/shares`;

export async function makeLink(chatId: string, view: ShareView): Promise<MadeLink> {
  return (await call("POST", chatShares(chatId), { view })) as MadeLink;
}

export async function chatLinks(chatId: string): Promise<ShareLink[]> {
  return ((await call("GET", chatShares(chatId))) as { links: ShareLink[] }).links;
}

export async function allLinks(): Promise<ShareLink[]> {
  return ((await call("GET", "/api/v1/shares")) as { links: ShareLink[] }).links;
}

export async function revokeLink(id: string): Promise<ShareLink> {
  return ((await call("DELETE", `/api/v1/shares/${encodeURIComponent(id)}`)) as { link: ShareLink }).link;
}

/** The whole address of a link's page, on this Keystone. */
export function addressOf(path: string): string {
  return window.location.origin + path;
}

const day = new Intl.DateTimeFormat(undefined, { day: "numeric", month: "short", year: "numeric" });

export function dayOf(iso: string): string {
  const t = new Date(iso);
  return Number.isNaN(t.getTime()) ? iso : day.format(t);
}

/** One line on a link: what it shows, when it was made, its opens and its end. */
export function linkLine(l: ShareLink): string {
  const parts = [l.view === "snapshot" ? "As it was when made" : "As it is now", `made ${dayOf(l.created)}`];
  parts.push(l.views === 0 ? "not opened yet" : l.views === 1 ? "opened once" : `opened ${l.views} times`);
  if (l.state === "open") parts.push(l.expires ? `opens until ${dayOf(l.expires)}` : "opens until you turn it off");
  return parts.join(" · ");
}

export const stateWords: Record<ShareLink["state"], string> = {
  open: "Open",
  revoked: "Turned off",
  expired: "Expired",
  refused: "Shows nothing",
};
