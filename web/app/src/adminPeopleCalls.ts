// The admin account's calls for people, the install and monitoring
// (P15-02), and the person's setup door. docs/architecture/admin.md has
// the design and docs/api/openapi.json every shape. Every figure is a
// number: an admin never sees a person's chats, files, memory or logs.

import { ApiError, call } from "./api";

const admin = "/api/v1/admin";

export type Cap = { month: string; spent_micros: number; limit_micros: number; warn_percent: number; state: string; problem?: string };

export type AdminPerson = {
  id: string;
  name: string;
  added: string;
  last_sign_in?: string;
  enabled: boolean;
  disabled_at?: string;
  has_password: boolean;
  setup_until?: string;
  spend: Cap;
  storage: { database: number; files: number; artifacts: number };
  jobs: { failed: number; stuck: number };
  schedules: number;
  models: Record<string, number>;
  crossed: string[];
};

export type SetupCode = { code: string; expires: string };

export type Total = {
  calls: number;
  input_tokens: number;
  output_tokens: number;
  cost_micros: number;
  capped_micros: number;
  errors: number;
  refusals: number;
};

export type DayUse = Total & { day: string };
export type ModelUse = Total & { model: string; class: string };

export type Use = { total: Total; days: DayUse[]; models: ModelUse[] };

export type PersonUsage = Use & {
  id: string;
  name: string;
  period: { from: string; to: string };
  failures: Record<string, number>;
  storage: { at: string; database: number; files: number; artifacts: number }[];
};

export type Install = Use & {
  at: string;
  period: { from: string; to: string };
  failures: Record<string, number>;
  cap_micros: number;
  disk: { used: number; of: number; percent: number; watch: number; state: string; crossed: boolean; problem?: string };
  people: { total: number; enabled: number; disabled: number; waiting: number };
  version: string;
  sampler: { every: string; newest?: string };
};

function check<T>(data: unknown, ok: (d: T) => boolean): T {
  const d = data as T;
  if (!d || !ok(d)) throw new ApiError("not-ready", 200);
  return d;
}

/** Every person on the install, with their numbers. Each read is written in each person's own log. */
export async function listAdminPeople(): Promise<AdminPerson[]> {
  return check<{ people: AdminPerson[] }>(await call("GET", `${admin}/people`), (d) => Array.isArray(d.people)).people;
}

/** One person, with their numbers. */
export async function readAdminPerson(id: string): Promise<AdminPerson> {
  return check<{ person: AdminPerson }>(await call("GET", `${admin}/people/${encodeURIComponent(id)}`), (d) => !!d.person).person;
}

/** Adds a person and returns them with their setup code, shown this once. */
export async function addPerson(name: string): Promise<{ person: AdminPerson; setup_code: SetupCode }> {
  return check<{ person: AdminPerson; setup_code: SetupCode }>(await call("POST", `${admin}/people`, { name }), (d) => !!d.person && !!d.setup_code);
}

/** A new setup code for a person, ending any earlier one. */
export async function newSetupCode(id: string): Promise<SetupCode> {
  return check<{ setup_code: SetupCode }>(await call("POST", `${admin}/people/${encodeURIComponent(id)}/setup-code`), (d) => !!d.setup_code).setup_code;
}

/** Disables or enables a person, and returns them as they are after it. */
export async function setEnabled(id: string, enabled: boolean): Promise<AdminPerson> {
  const path = `${admin}/people/${encodeURIComponent(id)}/${enabled ? "enable" : "disable"}`;
  return check<{ person: AdminPerson }>(await call("POST", path), (d) => !!d.person).person;
}

/** One person's usage over a period, this month to today when the dates are empty. */
export async function readPersonUsage(id: string, from = "", to = ""): Promise<PersonUsage> {
  const q = new URLSearchParams();
  if (from) q.set("from", from);
  if (to) q.set("to", to);
  const tail = q.toString() ? `?${q}` : "";
  return check<PersonUsage>(await call("GET", `${admin}/people/${encodeURIComponent(id)}/usage${tail}`), (d) => !!d.total && Array.isArray(d.days));
}

/** The whole install this month. */
export async function readInstall(): Promise<Install> {
  return check<Install>(await call("GET", `${admin}/install`), (d) => !!d.total && !!d.disk && !!d.people);
}

/** Sets up a person with the code an admin gave them and the password they choose. */
export async function setUp(code: string, password: string): Promise<{ id: string; name: string }> {
  try {
    const d = check<{ person: { id: string; name: string } }>(await call("POST", "/api/v1/setup", { code, password }), (x) => !!x.person);
    return d.person;
  } catch (e) {
    // At the door a 401 means the code was refused, not that a session ended.
    if (e instanceof ApiError && e.status === 401) throw new ApiError("refused", 401, e.code, e.said);
    throw e;
  }
}

/** Millionths of a dollar as dollars and cents. */
export function dollars(micros: number): string {
  return `$${(micros / 1e6).toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

/** Bytes in words. */
export function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toLocaleString("en-US", { maximumFractionDigits: v < 10 ? 1 : 0 })} ${units[i]}`;
}

/** The kinds of failure in words. */
export const failureWords: Record<string, string> = {
  model_error: "Model calls that failed",
  model_refused: "Model calls Keystone did not send",
  task_failed: "Tasks that failed",
  schedule_start_failed: "Scheduled runs that could not start",
};

/** A limit's key in words. */
export function limitWords(key: string): string {
  if (key.startsWith("run:")) return "A run's budget";
  return (
    {
      cap: "Monthly cap",
      memory_reserve: "Memory reserve",
      database: "Database",
      files: "Files",
      artifacts: "Artifacts",
    } as Record<string, string>
  )[key] ?? key;
}
