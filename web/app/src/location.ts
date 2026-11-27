// Location per message (P10-06): the pure part of the composer's "Use my
// location" control. It reads the settings, asks a geolocation it is given
// for one position, rounds it before anything else sees it, and has every
// word the control shows. It imports nothing, so Node's own test runner runs
// it as it is with a stand-in geolocation (test/location.test.ts, decision
// 0501), and so it can reach nothing but what it is given.
//
// The browser is asked only from askPosition, and askPosition is called
// only from the control's tap (LocationControl.tsx): never on load, never
// on a timer, never watched.

/** What the control reads from the settings registry. */
export type LocationSettings = {
  /** location.share: the control is drawn. */
  on: boolean;
  /** location.decimals: how many decimal places a position keeps, 1 to 4. */
  decimals: number;
  /** location.timeout: how long the browser may take to find the position, in milliseconds. */
  timeoutMs: number;
};

/** A position as it is sent with a message: already rounded. */
export type SentPosition = { latitude: number; longitude: number };

/** A rounded position, with how far it was rounded. */
export type Rounded = SentPosition & { decimals: number };

/** The part of the browser's navigator.geolocation that is used. */
export type GeoLike = {
  getCurrentPosition: (
    found: (p: { coords: { latitude: number; longitude: number } }) => void,
    failed: (e: { code: number }) => void,
    options?: { enableHighAccuracy?: boolean; timeout?: number; maximumAge?: number },
  ) => void;
};

/** What a tap came to: a rounded position, or why there is none, for a person. */
export type Asked = { ok: true; place: Rounded } | { ok: false; problem: string };

export const minDecimals = 1;
export const maxDecimals = 4;

/** A number of decimals kept between one and four. */
export function clampDecimals(n: number): number {
  if (!Number.isFinite(n)) return 2;
  return Math.max(minDecimals, Math.min(maxDecimals, Math.round(n)));
}

/** A coordinate rounded to a number of decimal places, with no negative zero. */
export function roundTo(v: number, decimals: number): number {
  const p = 10 ** clampDecimals(decimals);
  const r = Math.round(v * p) / p;
  return r === 0 ? 0 : r;
}

/** A position rounded to a number of decimal places. */
export function rounded(latitude: number, longitude: number, decimals: number): Rounded {
  const d = clampDecimals(decimals);
  return { latitude: roundTo(latitude, d), longitude: roundTo(longitude, d), decimals: d };
}

/** About how far a position rounded to these decimals may be from where it was read: a degree of latitude is about 111 km. */
export function distanceWords(decimals: number): string {
  switch (clampDecimals(decimals)) {
    case 1:
      return "11 km";
    case 2:
      return "1 km";
    case 3:
      return "110 m";
    default:
      return "11 m";
  }
}

const units: Record<string, number> = { ms: 1, s: 1000, m: 60_000, h: 3_600_000 };

/** A Go duration such as 10s or 1m30s, in milliseconds; undefined when it is not one. */
export function durationMs(text: string): number | undefined {
  const t = text.trim();
  if (t === "0") return 0;
  const part = /(\d+(?:\.\d*)?|\.\d+)(ms|s|m|h)/y;
  let ms = 0;
  let at = 0;
  if (t === "") return undefined;
  while (at < t.length) {
    part.lastIndex = at;
    const m = part.exec(t);
    if (!m) return undefined;
    ms += Number(m[1]) * units[m[2]];
    at = part.lastIndex;
  }
  return ms;
}

/** The control's settings from the settings route's list, or undefined when one is missing or does not read. */
export function readLocationSettings(list: readonly { key: string; value: string }[]): LocationSettings | undefined {
  const value = (key: string) => list.find((s) => s.key === key)?.value;
  const share = value("location.share");
  const decimals = Number(value("location.decimals"));
  const timeoutMs = durationMs(value("location.timeout") ?? "");
  if ((share !== "true" && share !== "false") || !Number.isInteger(decimals) || timeoutMs === undefined) return undefined;
  return { on: share === "true", decimals: clampDecimals(decimals), timeoutMs };
}

/** Every word the control shows. */
export const words = {
  name: "Use my location",
  finding: "Finding your location…",
  remove: "Remove the location",
  mark: "Sent with location",
  denied: "Keystone may not use your location. To share it, allow location for this site in your browser's settings, then tap again.",
  unavailable: "Your position could not be found. Check that location services are on, then tap again.",
  timeout: "Finding your position took too long. Tap to try again.",
  unsupported: "This browser cannot share a location.",
  insecure: "Location needs a secure page. Open Keystone at its https address to use it.",
};

/** The chip over the box while a location waits to be sent. */
export function chipText(decimals: number): string {
  return `Location: about ${distanceWords(decimals)}`;
}

/** The control's hint: what is sent, with what, and to whom. */
export function hint(decimals: number): string {
  return `Sends your approximate location (about ${distanceWords(decimals)}) with this message only, to whichever model answers it, hosted models included.`;
}

/** Why the browser gave no position, for a person, by the GeolocationPositionError code. */
export function failureWords(code: number): string {
  switch (code) {
    case 1:
      return words.denied;
    case 3:
      return words.timeout;
    default:
      return words.unavailable;
  }
}

/** Whether this page can ask for a position at all, and why not. */
export function availability(secure: boolean, geo: GeoLike | undefined): { available: boolean; reason: string } {
  if (!secure) return { available: false, reason: words.insecure };
  if (!geo || typeof geo.getCurrentPosition !== "function") return { available: false, reason: words.unsupported };
  return { available: true, reason: "" };
}

/**
 * Asks the geolocation for one position and rounds it before returning it.
 * The browser asks the person for permission the first time. Low accuracy
 * is asked for, since the position is rounded to a kilometre or so anyway,
 * and no cached position is taken, so the place is where the phone is now.
 */
export function askPosition(geo: GeoLike | undefined, secure: boolean, settings: LocationSettings): Promise<Asked> {
  const can = availability(secure, geo);
  if (!can.available || !geo) return Promise.resolve({ ok: false, problem: can.reason });
  return new Promise<Asked>((resolve) => {
    try {
      geo.getCurrentPosition(
        (p) => {
          const { latitude, longitude } = p.coords;
          if (!Number.isFinite(latitude) || !Number.isFinite(longitude) || Math.abs(latitude) > 90 || Math.abs(longitude) > 180) {
            resolve({ ok: false, problem: words.unavailable });
            return;
          }
          resolve({ ok: true, place: rounded(latitude, longitude, settings.decimals) });
        },
        (e) => resolve({ ok: false, problem: failureWords(e?.code ?? 2) }),
        { enableHighAccuracy: false, timeout: settings.timeoutMs > 0 ? settings.timeoutMs : Infinity, maximumAge: 0 },
      );
    } catch {
      resolve({ ok: false, problem: words.unsupported });
    }
  });
}

/** The position a message carries: the rounded latitude and longitude, and nothing else. */
export function sentOf(place: Rounded | undefined): SentPosition | undefined {
  return place ? { latitude: place.latitude, longitude: place.longitude } : undefined;
}
