// The admin account's settings calls (P15-03): a person's tenant-admin
// settings, and the install's own values (install settings, and the default
// each person's own setting starts from). docs/architecture/admin-settings.md
// has the design and docs/api/openapi.json every shape.

import { ApiError, call } from "./api";

const admin = "/api/v1/admin";

/** One setting as the admin sees it. */
export type AdminSetting = {
  key: string;
  title?: string;
  summary?: string;
  lane: string;
  type: string;
  options: string[];
  scope: "install" | "tenant-admin" | "tenant";
  description: string;
  /** For a person's setting, the registry's default; for the install, the shipped default. */
  default: string;
  /** What is set now: the person's value, or the install's. */
  value: string;
  /** Value is set, for the person or for the install, and is not the default. */
  overridden: boolean;
  updated?: string;
  /** The setting takes effect only after Keystone restarts. */
  restart?: boolean;
  /** For an install setting: what Keystone runs with now. */
  in_force?: string;
  /** A restart setting changed since the start, waiting for the next. */
  pending?: boolean;
};

/** One change, as both audit logs keep it. */
export type AdminChange = { by: string; person?: string; key: string; old: string; new: string; restart?: boolean };

export type Changed = { setting: AdminSetting; change: AdminChange };

export type Person = { id: string; name: string };

function isSetting(v: unknown): v is AdminSetting {
  if (typeof v !== "object" || v === null) return false;
  const s = v as Record<string, unknown>;
  return typeof s.key === "string" && typeof s.type === "string" && typeof s.value === "string" && typeof s.default === "string" && Array.isArray(s.options);
}

function settingsFrom(data: unknown): AdminSetting[] {
  const list = (data as { settings?: unknown } | undefined)?.settings;
  if (!Array.isArray(list) || !list.every(isSetting)) throw new ApiError("not-ready", 200);
  return list;
}

function changedFrom(data: unknown): Changed {
  const c = data as Partial<Changed> | undefined;
  if (!c || !isSetting(c.setting) || !c.change) throw new ApiError("not-ready", 200);
  return c as Changed;
}

/** The body of both PUT routes: a value, or null to go back to the default. */
function body(key: string, value: string | null) {
  return value === null ? { key, reset: true } : { key, value };
}

/** Every person on the install: id and name. Each person's log records the read. */
export async function listPeople(): Promise<Person[]> {
  const data = (await call("GET", `${admin}/people`)) as { people?: unknown } | undefined;
  if (!data || !Array.isArray(data.people)) throw new ApiError("not-ready", 200);
  return data.people.filter((p): p is Person => typeof p?.id === "string" && typeof p?.name === "string");
}

/** A person's tenant-admin settings. Their log records the read. */
export async function listPersonSettings(id: string): Promise<AdminSetting[]> {
  return settingsFrom(await call("GET", `${admin}/people/${encodeURIComponent(id)}/settings`));
}

/** Sets one of a person's tenant-admin settings, or with null takes it back to its default. */
export async function changePersonSetting(id: string, key: string, value: string | null): Promise<Changed> {
  return changedFrom(await call("PUT", `${admin}/people/${encodeURIComponent(id)}/settings`, body(key, value)));
}

/** Every install setting, and every person's own setting as the default a person starts from. */
export async function listInstallSettings(): Promise<AdminSetting[]> {
  return settingsFrom(await call("GET", `${admin}/install/settings`));
}

/** Sets the install's value for a setting, or with null takes it back to its shipped default. */
export async function changeInstallSetting(key: string, value: string | null): Promise<Changed> {
  return changedFrom(await call("PUT", `${admin}/install/settings`, body(key, value)));
}
