// What the Scheduled screen says about a schedule and its runs (P8-03).
//
// The server sends every due time in the person's zone, so the JSON text
// carries that zone's offset ("2026-11-27T07:00:00Z", "…-05:00"). Times are
// read from the text itself, never turned into the browser's zone: the
// person sees their zone's clock wherever the browser is.

import type { ScheduleRun } from "./api";

type Wall = { year: number; month: number; day: number; hour: number; minute: number; offset: number };

const stamp = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::\d{2}(?:\.\d+)?)?(Z|[+-]\d{2}:\d{2})$/;

/** The wall-clock parts of a time as the server wrote it, and its offset in minutes. */
export function wall(text: string): Wall | null {
  const m = stamp.exec(text);
  if (!m) return null;
  let offset = 0;
  if (m[6] !== "Z") {
    const sign = m[6][0] === "-" ? -1 : 1;
    offset = sign * (Number(m[6].slice(1, 3)) * 60 + Number(m[6].slice(4, 6)));
  }
  return { year: Number(m[1]), month: Number(m[2]), day: Number(m[3]), hour: Number(m[4]), minute: Number(m[5]), offset };
}

const days = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

function dayNumber(y: number, m: number, d: number): number {
  return Math.floor(Date.UTC(y, m - 1, d) / 86400000);
}

function clock(w: Wall): string {
  return `${String(w.hour).padStart(2, "0")}:${String(w.minute).padStart(2, "0")}`;
}

/**
 * A time as the screen shows it, in the zone it was written in: "today
 * 07:00", "tomorrow 07:00", "yesterday 07:00", "Fri 27 Nov 07:00", with the
 * year when it is not this year's.
 */
export function when(text: string | undefined, now = Date.now()): string {
  if (!text) return "";
  const w = wall(text);
  if (!w) return text;
  const here = new Date(now + w.offset * 60000);
  const today = dayNumber(here.getUTCFullYear(), here.getUTCMonth() + 1, here.getUTCDate());
  const that = dayNumber(w.year, w.month, w.day);
  const t = clock(w);
  if (that === today) return `today ${t}`;
  if (that === today + 1) return `tomorrow ${t}`;
  if (that === today - 1) return `yesterday ${t}`;
  const weekday = days[new Date(Date.UTC(w.year, w.month - 1, w.day)).getUTCDay()];
  const year = w.year === here.getUTCFullYear() ? "" : ` ${w.year}`;
  return `${weekday} ${w.day} ${months[w.month - 1]}${year} ${t}`;
}

/** The time of day of a time as it was written: "07:00". */
export function timeOf(text: string | undefined): string {
  const w = text ? wall(text) : null;
  return w ? clock(w) : "";
}

/** How long between two times: "48 s", "3 min", "1 h 5 min". */
export function took(from?: string, to?: string): string {
  if (!from || !to) return "";
  const ms = Date.parse(to) - Date.parse(from);
  if (!Number.isFinite(ms) || ms < 0) return "";
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s} s`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m} min`;
  return `${Math.floor(m / 60)} h ${m % 60} min`;
}

/** Millionths of a dollar as dollars: "$0.04", "$0.00". */
export function dollars(micros: number): string {
  const d = micros / 1e6;
  if (d > 0 && d < 0.01) return "under $0.01";
  return `$${d.toFixed(2)}`;
}

export type Tone = "ok" | "bad" | "warn" | "idle" | "busy";

/** A run is under way: starting, waiting or running. */
export function open(r: ScheduleRun): boolean {
  return r.status === "starting" || r.status === "queued" || r.status === "running";
}

/** A run's state, as a pill: its words and its tone. */
export function pill(r: ScheduleRun): { label: string; tone: Tone } {
  if (r.waiting) return { label: "Waiting for you", tone: "warn" };
  switch (r.status) {
    case "starting":
      return r.error ? { label: "Waiting to start", tone: "warn" } : { label: "Starting", tone: "busy" };
    case "queued":
      return { label: "Starting", tone: "busy" };
    case "running":
      return { label: "Running", tone: "busy" };
    case "done":
      return r.late ? { label: "Done, late", tone: "warn" } : { label: "Done", tone: "ok" };
    case "stopped":
      return { label: "Stopped", tone: "idle" };
    case "gone":
      return { label: "Chat deleted", tone: "idle" };
    default:
      return { label: "Failed", tone: "bad" };
  }
}

/** The line under a run's pill that says why: late, waiting, or failed. */
export function why(r: ScheduleRun): string {
  const lines: string[] = [];
  if (r.trigger === "schedule" && r.late) {
    const at = timeOf(r.due);
    if (r.late_reason === "stopped") {
      lines.push(r.missed > 1
        ? `Missed ${r.missed} times, the last at ${at}, because Keystone was not running. Ran once when it was back.`
        : `Missed at ${at} because Keystone was not running. Ran once when it was back.`);
    } else {
      lines.push(r.missed > 1
        ? `Due at ${at}, ${r.missed} times while the run before was still going. Ran once when it ended.`
        : `Due at ${at}. It waited for the run before it, or for another background task to end.`);
    }
  }
  if (r.waiting) lines.push("A step waits for your approval, in its chat.");
  else if (r.error && (r.status === "failed" || r.status === "starting" || r.status === "stopped" || r.status === "gone")) lines.push(r.error);
  else if (open(r) && r.progress) lines.push(r.progress);
  return lines.join(" ");
}

/** How a schedule's last run went, for its row in the list. */
export function lastSaid(r: ScheduleRun | undefined): { label: string; tone: Tone } | null {
  if (!r) return null;
  const p = pill(r);
  if (open(r)) return p;
  const day = when(r.started).replace(/ \d{2}:\d{2}$/, "");
  return { label: `${p.label} ${day}`.trim(), tone: p.tone };
}

/** The zone as it reads in the middle of a sentence. */
export function zoneWords(z: { name?: string; label: string } | undefined): string {
  if (!z) return "your time zone";
  return z.name ? z.name : z.label.charAt(0).toLowerCase() + z.label.slice(1);
}

export const dayNames: { key: string; short: string }[] = [
  { key: "mon", short: "Mon" },
  { key: "tue", short: "Tue" },
  { key: "wed", short: "Wed" },
  { key: "thu", short: "Thu" },
  { key: "fri", short: "Fri" },
  { key: "sat", short: "Sat" },
  { key: "sun", short: "Sun" },
];
