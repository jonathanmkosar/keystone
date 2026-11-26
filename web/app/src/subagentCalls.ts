// The calls of sub-agents (P8-02): the lanes under a run_subagents step in
// the chat. The routes are in docs/architecture/sub-agents.md, and their
// shapes in docs/api/openapi.json. Kept apart from api.ts so the two can
// change without touching each other. A sub-agent's steps are its own
// chat's (openChat), and its approval cards its turn's (answerApproval).

import { ApiError, call, type Approval } from "./api";

export type SubagentError = { code: string; message: string };

/** One sub-agent, as GET chats/{id}/subagents gives it. */
export type Subagent = {
  id: string;
  /** The chat, turn and run_subagents call that started it, and its place among the call's, from 1. */
  chat_id: string;
  turn_id: string;
  call_id: string;
  position: number;
  /** Its own chat, which holds its whole transcript, and its turn there. */
  work_chat_id: string;
  work_turn_id?: string;
  title: string;
  task: string;
  model?: string;
  status: "queued" | "running" | "done" | "stopped" | "failed" | "paused";
  /** Where it is, in the server's words. */
  progress: string;
  /** A call of its turn waits for the person's answer. */
  waiting: boolean;
  steps: number;
  max_steps: number;
  budget_micros: number;
  spent_micros: number;
  result?: string;
  error?: SubagentError;
  /** Which model wrote its work when the cap or its budget refused its own. */
  notice?: string;
  outside: boolean;
  sensitive: boolean;
  /** Its result went back to the turn that started it. */
  passed: boolean;
  created: string;
  started?: string;
  ended?: string;
};

const chatPath = (chatId: string) => `/api/v1/chats/${encodeURIComponent(chatId)}`;

function isSubagent(v: unknown): v is Subagent {
  if (typeof v !== "object" || v === null) return false;
  const s = v as Record<string, unknown>;
  return typeof s.id === "string" && typeof s.work_chat_id === "string" && typeof s.status === "string" && typeof s.position === "number";
}

function subagentFrom(data: unknown): Subagent {
  if (!isSubagent(data)) throw new ApiError("not-ready", 200);
  return data;
}

/** The sub-agents one run_subagents call of the chat started, by place. */
export async function listSubagents(chatId: string, callId: string): Promise<Subagent[]> {
  const data = (await call("GET", `${chatPath(chatId)}/subagents?call=${encodeURIComponent(callId)}`)) as { subagents?: unknown } | undefined;
  const list = data?.subagents;
  if (!Array.isArray(list) || !list.every(isSubagent)) throw new ApiError("not-ready", 200);
  return list;
}

/** Stops one sub-agent. The call that started it goes on with the others. */
export async function stopSubagent(chatId: string, id: string): Promise<Subagent> {
  return subagentFrom(await call("POST", `${chatPath(chatId)}/subagents/${encodeURIComponent(id)}/stop`));
}

/** The approvals a sub-agent's turn asked for, oldest first. */
export async function subagentApprovals(sub: Subagent): Promise<Approval[]> {
  if (!sub.work_turn_id) return [];
  const data = (await call("GET", `${chatPath(sub.work_chat_id)}/turns/${encodeURIComponent(sub.work_turn_id)}/approvals`)) as { approvals?: unknown } | undefined;
  return Array.isArray(data?.approvals) ? (data.approvals as Approval[]) : [];
}

/** A sub-agent has not ended. */
export function underWay(s: Subagent): boolean {
  return s.status === "queued" || s.status === "running";
}

/** An amount in millionths of a US dollar, as the usage screens write it. */
export function dollars(micros: number): string {
  const usd = micros / 1_000_000;
  if (usd > 0 && usd < 0.01) return "under $0.01";
  return `$${usd.toFixed(2)}`;
}
