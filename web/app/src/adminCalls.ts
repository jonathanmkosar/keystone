// The admin account's calls (P15-01), and a person's own view of what admin
// accounts read of theirs. docs/architecture/admin.md has the design and
// docs/api/openapi.json every shape.
//
// An admin is a separate account. Its session is a cookie the server sets,
// keystone_admin, sent with the admin routes alone, so a browser signed in
// as a person and as an admin keeps the two apart. No token is held here.

import { ApiError, call } from "./api";

const admin = "/api/v1/admin";

export type AdminAccount = { id: string; name: string };

export type AdminSession = {
  account: AdminAccount;
  session: { id: string; method: string; created: string; expires: string; idle_until: string };
};

function sessionFrom(data: unknown): AdminSession {
  const s = data as Partial<AdminSession> | undefined;
  if (!s?.account || typeof s.account.id !== "string" || typeof s.account.name !== "string" || !s.session) throw new ApiError("not-ready", 200);
  return s as AdminSession;
}

/** Signs in to an admin account by its name or id. The server sets the admin cookie. */
export async function adminSignIn(account: string, password: string): Promise<AdminSession> {
  try {
    return sessionFrom(await call("POST", `${admin}/session`, { account, password }));
  } catch (e) {
    // At the door a 401 means the password was refused, not that a session ended.
    if (e instanceof ApiError && e.status === 401) throw new ApiError("refused", 401);
    throw e;
  }
}

/** The signed-in admin account. Throws ApiError "signed-out" when there is none. */
export async function adminWho(): Promise<AdminSession> {
  return sessionFrom(await call("GET", `${admin}/session`));
}

/** Ends the admin session. */
export async function adminSignOut(): Promise<void> {
  await call("DELETE", `${admin}/session`);
}

/** One entry of a person's audit log, as the server gives it. */
type Entry = { id: number; at: string; request_id: string; actor: string; action: string; outcome: string; status?: number; sent?: string };

/** One admin call that read or changed something of the person's. */
export type AdminAccess = {
  requestId: string;
  at: string;
  adminId: string;
  /** The admin account's name when it made the call. */
  by: string;
  action: string;
  read: string[];
  key: string;
  /** started when the call never finished; ok, refused or error after. */
  outcome: string;
};

const actorPrefix = "admin:";

/** Folds the two entries of each admin call into one, newest first. */
export function foldAdminEntries(entries: Entry[]): AdminAccess[] {
  const byRequest = new Map<string, AdminAccess>();
  for (const e of entries) {
    if (!e.actor.startsWith(actorPrefix)) continue;
    let touch: { by?: string; read?: string[]; key?: string } = {};
    try {
      touch = e.sent ? JSON.parse(e.sent) : {};
    } catch {
      // An entry whose sent field is not JSON still says who and when.
    }
    const had = byRequest.get(e.request_id);
    if (had) {
      if (e.outcome !== "started") had.outcome = e.outcome;
      continue;
    }
    byRequest.set(e.request_id, {
      requestId: e.request_id,
      at: e.at,
      adminId: e.actor.slice(actorPrefix.length),
      by: touch.by ?? "",
      action: e.action,
      read: Array.isArray(touch.read) ? touch.read : [],
      key: touch.key ?? "",
      outcome: e.outcome,
    });
  }
  return [...byRequest.values()].reverse();
}

/** Every entry admin accounts' calls left in the signed-in person's own log, folded, newest first. */
export async function listAdminAccess(): Promise<AdminAccess[]> {
  const all: Entry[] = [];
  let after = 0;
  // A page at a time, as the server gives them; a few pages at most for a person.
  for (let page = 0; page < 50; page++) {
    const data = (await call("GET", `/api/v1/audit/admin${after ? `?after=${after}` : ""}`)) as { entries?: Entry[]; next_after?: number } | undefined;
    if (!data || !Array.isArray(data.entries)) throw new ApiError("not-ready", 200);
    all.push(...data.entries);
    if (!data.next_after) break;
    after = data.next_after;
  }
  return foldAdminEntries(all);
}
