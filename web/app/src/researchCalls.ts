// The calls of research mode (P8-04): what a task would run with, a chat's
// tasks, starting one, reading one and stopping one. The routes are in
// docs/architecture/research-mode.md, and their shapes in
// docs/api/openapi.json. Kept apart from api.ts so the two can change
// without touching each other.
//
// This file also keeps the tasks a page has seen, by chat, so the note a
// task wrote can be drawn with its sources and its report, and the choice
// the Research control holds until the next message is sent.

import { useSyncExternalStore } from "react";
import { ApiError, call } from "./api";
import type { Subagent } from "./subagentCalls";

/** One source a part of a research task read. */
export type ResearchSource = {
  n: number;
  title: string;
  url: string;
  /** The first part that read it, from 1. */
  part: number;
  /** Its page was read; false means a search result's extract. */
  fetched: boolean;
};

export type ResearchError = { code: string; message: string };

/** One research task, as GET chats/{id}/research/{research} gives it. */
export type Research = {
  id: string;
  chat_id: string;
  work_chat_id: string;
  title: string;
  question: string;
  status: "queued" | "running" | "paused" | "done" | "stopped" | "failed";
  stage?: "plan" | "parts" | "write";
  /** Where it is, in the server's words. */
  progress: string;
  /** A call of one of its turns waits for the person's answer. */
  waiting: boolean;
  budget_micros: number;
  spent_micros: number;
  at_budget: boolean;
  plan: { title: string; task: string }[];
  parts: Subagent[];
  sources: ResearchSource[];
  sources_read: number;
  dropped_citations: number;
  summary?: string;
  notice?: string;
  artifact_id?: string;
  artifact_chat_id?: string;
  error?: ResearchError;
  note_message_id?: number;
  created: string;
  started?: string;
  ended?: string;
};

/** What a task started now would run with, as GET research gives it. */
export type ResearchTerms = {
  budget_micros: number;
  parts: number;
  sources_per_part: number;
  report_words: number;
  open: number;
  open_max: number;
  paused: boolean;
};

const chatPath = (chatId: string) => `/api/v1/chats/${encodeURIComponent(chatId)}`;

function isResearch(v: unknown): v is Research {
  if (typeof v !== "object" || v === null) return false;
  const r = v as Record<string, unknown>;
  return typeof r.id === "string" && typeof r.work_chat_id === "string" && typeof r.status === "string" && Array.isArray(r.parts) && Array.isArray(r.sources) && Array.isArray(r.plan);
}

function researchFrom(data: unknown): Research {
  if (!isResearch(data)) throw new ApiError("not-ready", 200);
  return data;
}

export async function researchTerms(): Promise<ResearchTerms> {
  const data = (await call("GET", "/api/v1/research")) as Partial<ResearchTerms> | undefined;
  if (!data || typeof data.budget_micros !== "number" || typeof data.parts !== "number") throw new ApiError("not-ready", 200);
  return data as ResearchTerms;
}

/** The research tasks started from the chat, or working in it, oldest first. */
export async function listResearch(chatId: string): Promise<Research[]> {
  const data = (await call("GET", `${chatPath(chatId)}/research`)) as { research?: unknown } | undefined;
  const list = data?.research;
  if (!Array.isArray(list) || !list.every(isResearch)) throw new ApiError("not-ready", 200);
  return list;
}

export async function startResearch(chatId: string, question: string): Promise<Research> {
  return researchFrom(await call("POST", `${chatPath(chatId)}/research`, { question }));
}

export async function getResearch(chatId: string, id: string): Promise<Research> {
  return researchFrom(await call("GET", `${chatPath(chatId)}/research/${encodeURIComponent(id)}`));
}

export async function stopResearch(chatId: string, id: string): Promise<Research> {
  return researchFrom(await call("POST", `${chatPath(chatId)}/research/${encodeURIComponent(id)}/stop`));
}

/** A task has not ended, or has and its note or its report is not yet written. */
export function researchUnderWay(r: Research): boolean {
  if (r.status === "queued" || r.status === "running" || r.status === "paused") return true;
  if (!r.note_message_id) return true;
  // The report is saved just after the note. One that is still not saved two
  // minutes on is left for the next time the chat is opened.
  const justEnded = r.ended !== undefined && Date.now() - Date.parse(r.ended) < 120_000;
  return r.status === "done" && !r.artifact_id && justEnded;
}

// The tasks a page has seen, by chat.

const byChat = new Map<string, Research[]>();
const listeners = new Set<() => void>();
const none: Research[] = [];

function tell() {
  for (const l of listeners) l();
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

export function keepResearch(chatId: string, tasks: Research[]) {
  byChat.set(chatId, tasks);
  tell();
}

/** Puts one task in the chat's list, in place of the one with its id, or at the end. */
export function keepOneResearch(chatId: string, task: Research) {
  const have = byChat.get(chatId) ?? [];
  keepResearch(chatId, have.some((x) => x.id === task.id) ? have.map((x) => (x.id === task.id ? task : x)) : [...have, task]);
}

export function keptResearch(chatId: string): Research[] {
  return byChat.get(chatId) ?? none;
}

export function useResearch(chatId: string): Research[] {
  return useSyncExternalStore(subscribe, () => byChat.get(chatId) ?? none);
}

// The Research control's choice: on means the next message sent from that
// composer is a research question. It is kept for a chat, or for the new
// chat screen under the person's id, until that message is sent.

const armed = new Set<string>();

export function researchArmed(key: string): boolean {
  return armed.has(key);
}

export function armResearch(key: string, on: boolean) {
  if (on) armed.add(key);
  else armed.delete(key);
  tell();
}

export function useResearchArmed(key: string): boolean {
  return useSyncExternalStore(subscribe, () => armed.has(key));
}

/** The key the new chat screen keeps its choice under. */
export const newChatKey = (personId: string) => `new:${personId}`;

// A start that failed, by chat, for the cards to say.

const problems = new Map<string, string>();

export function researchProblem(chatId: string): string {
  return problems.get(chatId) ?? "";
}

export function useResearchProblem(chatId: string): string {
  return useSyncExternalStore(subscribe, () => problems.get(chatId) ?? "");
}

function sayProblem(chatId: string, text: string) {
  if (text === "") problems.delete(chatId);
  else problems.set(chatId, text);
  tell();
}

/** Why a start was refused, in words for the person. */
function whyNot(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.kind === "signed-out") return "You are signed out. Sign in, then ask again.";
    if (e.code === "too_many_research") return "The research was not started: as many research tasks as you may have at once are under way. Wait for one to end, or stop one.";
    if (e.status === 400 && e.said) return `The research was not started: ${e.said}.`;
  }
  return "The research could not be started. Check that the server is running, then ask again.";
}

/**
 * Starts a research task with the question, when the Research control is
 * on for this composer, and reports whether it took the message. The
 * composer's own send does nothing more then. Files are not part of a
 * research question, and are left out.
 */
export function sendAsResearch(key: string, chatId: string, question: string): boolean {
  if (!armed.has(key) || question.trim() === "") return false;
  armResearch(key, false);
  sayProblem(chatId, "");
  startResearch(chatId, question.trim()).then(
    (task) => keepOneResearch(chatId, task),
    (e) => sayProblem(chatId, whyNot(e)),
  );
  return true;
}

/** An amount in millionths of a US dollar, as the usage screens write it. */
export function researchDollars(micros: number): string {
  const usd = micros / 1_000_000;
  if (usd > 0 && usd < 0.01) return "under $0.01";
  return `$${usd.toFixed(2)}`;
}
