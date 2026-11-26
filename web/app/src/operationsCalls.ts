// The calls of Operations and Monitor (P11-03): the report, and the crossed
// watch levels the home screen reads. The routes are in
// docs/architecture/operations.md, and their shapes in docs/api/openapi.json.
// Kept apart from api.ts so the two can change without touching each other.

import { ApiError, call } from "./api";

export type OpsPoint = { at: string; used: number; of: number };

export type OpsTrend = {
  points: OpsPoint[];
  known: boolean;
  /** Growth a day, in the limit's unit; negative when it shrank. */
  per_day: number;
  full_at?: string;
  resets?: string;
  at_reset?: number;
};

export type OpsLimit = {
  /** cap, memory_reserve, disk, database, files, artifacts, or run:<request id>. */
  key: string;
  label: string;
  note?: string;
  /** tenant or install. */
  scope: string;
  /** usd_micros or bytes. */
  unit: string;
  used: number;
  of: number;
  percent: number;
  watch: number;
  /** ok, watch, full or unknown. */
  state: string;
  crossed: boolean;
  problem?: string;
  trend: OpsTrend;
  chat_id?: string;
  run_id?: string;
};

export type OpsTask = {
  id: string;
  title: string;
  scheduled: boolean;
  status: string;
  waiting: boolean;
  progress?: string;
  chat_id: string;
  work_chat_id: string;
  turn_id?: string;
  rounds: number;
  steps: number;
  budget_micros: number;
  spent_micros: number;
  error?: string;
  created: string;
  ended?: string;
  round_started?: string;
};

export type OpsJobs = {
  paused: boolean;
  counts: Record<string, number>;
  open: number;
  newest: OpsTask[];
  stuck: OpsTask[];
  stuck_after: string;
  failed: OpsTask[];
  failed_count: number;
};

export type OpsRun = {
  started: string;
  status: string;
  waiting?: boolean;
  late?: boolean;
  late_reason?: string;
  missed: number;
  error?: string;
  chat_id?: string;
  spent_micros: number;
};

export type OpsSchedule = {
  id: string;
  name: string;
  says: string;
  enabled: boolean;
  held: boolean;
  next?: string;
  last_run?: OpsRun;
};

export type OpsSchedules = {
  paused: boolean;
  zone: string;
  schedules: OpsSchedule[];
  enabled: number;
  failing: number;
};

export type OpsCallError = { at: string; kind: string; message: string };

export type OpsModel = {
  name: string;
  label: string;
  class: string;
  where?: string;
  in_registry: boolean;
  problem?: string;
  calls: number;
  ok: number;
  errors: number;
  refusals: number;
  stopped: number;
  error_percent: number;
  median_ms: number;
  cost_micros: number;
  /** good, errors, failing, refused or idle. */
  state: string;
  last_error?: OpsCallError;
};

export type OpsModels = { since: string; window: string; models: OpsModel[] };

export type OpsToolStep = { name: string; outcome: string; duration_ms: number };

export type OpsUsage = {
  calls: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  cost_micros: number;
};

export type OpsTrace = {
  request_id: string;
  /** chat, task, schedule or subagent. */
  kind: string;
  title: string;
  chat_id?: string;
  turn_id?: string;
  run_id?: string;
  turns: number;
  status: string;
  waiting?: boolean;
  error?: string;
  models: string[];
  calls: number;
  errors: number;
  refusals: number;
  tools: OpsToolStep[];
  usage: OpsUsage;
  started: string;
  ended?: string;
};

export type OpsReport = {
  at: string;
  jobs: OpsJobs;
  schedules: OpsSchedules;
  models: OpsModels;
  traces: OpsTrace[];
  limits: OpsLimit[];
  crossed: number;
};

export type OpsWatch = { at: string; crossed: OpsLimit[] };

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null;
}

function isReport(v: unknown): v is OpsReport {
  if (!isObject(v)) return false;
  return (
    Array.isArray(v.limits) &&
    Array.isArray(v.traces) &&
    isObject(v.jobs) &&
    isObject(v.schedules) &&
    isObject(v.models) &&
    Array.isArray((v.models as Record<string, unknown>).models)
  );
}

/** Reads the person's Operations report. */
export async function readOperations(): Promise<OpsReport> {
  const data = await call("GET", "/api/v1/operations");
  if (!isReport(data)) throw new ApiError("not-ready", 200);
  return data;
}

/** Reads the limits at or past their watch level. */
export async function readWatch(): Promise<OpsWatch> {
  const data = await call("GET", "/api/v1/operations/watch");
  if (!isObject(data) || !Array.isArray(data.crossed)) throw new ApiError("not-ready", 200);
  return data as OpsWatch;
}

// Words and numbers, shared by the page and the notice.

/** An amount in millionths of a dollar, to the cent. */
export function dollars(micros: number): string {
  if (micros > 0 && micros < 5000) return "under $0.01";
  return "$" + (micros / 1e6).toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

/** A size in bytes, in the largest unit that keeps it above one. */
export function bytes(n: number): string {
  const units = ["bytes", "KB", "MB", "GB", "TB"];
  let v = n;
  let i = 0;
  while (Math.abs(v) >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  if (i === 0) return `${Math.round(v)} bytes`;
  return `${v.toLocaleString("en-US", { maximumFractionDigits: v < 10 ? 1 : 0 })} ${units[i]}`;
}

/** A limit's amount in its unit. */
export function amount(l: { unit: string }, n: number): string {
  return l.unit === "usd_micros" ? dollars(n) : bytes(n);
}
