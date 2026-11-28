// The sums behind the usage ring (UsageRing.tsx), apart so they can be
// tested without a page: how full the month's cap is, its tone, and the day
// it starts again.

import type { UsageCap, UsageReserve } from "./usageCalls";

export type Share = { share: number; tone: "ok" | "warn" | "bad" };

/** A share out of 1, kept between 0 and 1. */
export function clamp(n: number): number {
  return Math.max(0, Math.min(1, Number.isFinite(n) ? n : 0));
}

/** How full the month's cap is, and its tone. A cap that does not read is full. */
export function capShare(cap: UsageCap): Share {
  if (cap.state === "unread") return { share: 1, tone: "bad" };
  const share = cap.limit_micros > 0 ? clamp(cap.spent_micros / cap.limit_micros) : cap.spent_micros > 0 ? 1 : 0;
  return { share, tone: cap.state === "reached" ? "bad" : cap.state === "warning" ? "warn" : "ok" };
}

/** How much of the memory pass's reserve is spent, or null when it does not read. */
export function reserveShare(reserve: UsageReserve): Share | null {
  if (reserve.problem) return null;
  return { share: reserve.limit_micros > 0 ? clamp(reserve.spent_micros / reserve.limit_micros) : 0, tone: reserve.left_micros > 0 ? "ok" : "bad" };
}

/** The day a month's cap starts again, the first of the next month in UTC, as "Tue, Dec 1". Empty for a month that does not parse. */
export function resets(month: string): string {
  const m = /^(\d{4})-(\d{2})$/.exec(month);
  if (!m) return "";
  const first = new Date(Date.UTC(Number(m[1]), Number(m[2]), 1));
  return first.toLocaleDateString("en-US", { weekday: "short", month: "short", day: "numeric", timeZone: "UTC" });
}
