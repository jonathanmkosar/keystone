// The call of the Usage screen (P11-02). The route is in
// docs/architecture/usage-report.md, and its shape in docs/api/openapi.json.
// Kept apart from api.ts so the two can change without touching each other.

import { ApiError, call } from "./api";

/** Calls, tokens and cost, in millionths of a US dollar. */
export type Tally = {
  calls: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  cost_micros: number;
};

/** A tally, and the same split by the class of the model that took each call. */
export type UsageSum = Tally & { local: Tally; hosted: Tally; trusted: Tally };

export type UsageRow = UsageSum & {
  /** A model's registry name, a chat's id, or a kind of work. Empty for the line of calls in no chat, and for the line that adds up the rest. */
  key: string;
  label: string;
  /** In the breakdown by model: local, hosted or trusted. */
  class?: string;
  /** How many lines this one adds up, past the breakdown's limit. */
  rest?: number;
};

export type UsagePeriod = {
  /** this_month, last_month, last_7_days, last_30_days, month or range. */
  name: string;
  from: string;
  to: string;
  /** Set when the period is exactly one calendar month. */
  month?: string;
};

export type UsageCap = {
  month: string;
  spent_micros: number;
  limit_micros: number;
  warn_percent: number;
  warn_at_micros: number;
  left_micros: number;
  /** ok, warning, reached or unread. */
  state: string;
  problem?: string;
};

export type UsageReserve = {
  month: string;
  spent_micros: number;
  limit_micros: number;
  left_micros: number;
  problem?: string;
};

export type UsageReport = {
  period: UsagePeriod;
  total: UsageSum;
  models: UsageRow[];
  chats: UsageRow[];
  kinds: UsageRow[];
  rows: number;
  cap: UsageCap;
  memory_reserve: UsageReserve;
  /** The period's use of Auto (P14-07). Absent from a server built before it. */
  auto?: UsageAuto;
};

/** The ladder's top step as it is set now, and the replies on Auto priced at it (P14-07). */
export type UsageTopStep = {
  name?: string;
  label?: string;
  class?: string;
  cost_micros: number;
  /** Why there is no top step to price at, in words. */
  problem?: string;
};

/** The period's use of Auto (P14-07), from the usage records and the router's verdicts. */
export type UsageAuto = {
  /** Turns routed on Auto whose first verdict is in the period, rewrites included. */
  turns: number;
  /** Of those, the ones a stronger model was asked for. */
  stronger_asked: number;
  /** Of those, the ones that were a stronger model's rewrite. */
  rewrites: number;
  replies: UsageSum;
  decisions: UsageSum;
  /** The replies and the decisions. */
  cost_micros: number;
  top_step: UsageTopStep;
  /** The top step's cost less cost_micros; below zero when Auto cost more. */
  saved_micros: number;
};

/** What a report is asked for: a named period, a month, or a range of days. Nothing means the person's default. */
export type UsageAsk = { period?: string; month?: string; from?: string; to?: string; rows?: number };

function isTally(v: unknown): v is Tally {
  if (typeof v !== "object" || v === null) return false;
  const t = v as Record<string, unknown>;
  return typeof t.calls === "number" && typeof t.cost_micros === "number";
}

function isReport(v: unknown): v is UsageReport {
  if (typeof v !== "object" || v === null) return false;
  const r = v as Record<string, unknown>;
  return (
    isTally(r.total) &&
    Array.isArray(r.models) &&
    Array.isArray(r.chats) &&
    Array.isArray(r.kinds) &&
    typeof r.period === "object" &&
    r.period !== null &&
    typeof r.cap === "object" &&
    r.cap !== null &&
    typeof r.memory_reserve === "object" &&
    r.memory_reserve !== null
  );
}

/** Reads the person's usage report. */
export async function readUsage(ask: UsageAsk): Promise<UsageReport> {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(ask)) {
    if (v !== undefined && v !== "") q.set(k, String(v));
  }
  const text = q.toString();
  const data = await call("GET", "/api/v1/usage" + (text ? "?" + text : ""));
  if (!isReport(data)) throw new ApiError("not-ready", 200);
  return data;
}
