import { useCallback, useEffect, useState, type ReactNode } from "react";
import { ApiError } from "./api";
import { Icon } from "./Icon";

// The parts every settings screen is made of: how a refusal is said, how a
// screen reads what it shows, and a section with its heading. They were in
// Settings.tsx until General got a file of its own (P11-04).

/** What a refused or failed call is told to the person as. */
export function said(e: unknown, fallback: string): string {
  if (e instanceof ApiError) {
    if (e.said && e.status >= 400 && e.status < 500 && e.status !== 404) return sentence(e.said);
    if (e.kind === "not-ready") return "The server did not answer. Check that it is running.";
  }
  return fallback;
}

/** The server's refusals start in lower case. On the screen they are sentences. */
export function sentence(text: string): string {
  const t = text.trim();
  if (t === "") return t;
  const first = t[0].toUpperCase() + t.slice(1);
  return /[.!?]$/.test(first) ? first : first + ".";
}

export type Loaded<T> = { state: "loading" } | { state: "failed" } | { state: "ready"; data: T };

/** Reads something from the server when the screen opens, and again on reload. */
export function useLoaded<T>(read: () => Promise<T>, onSignedOut: () => void): [Loaded<T>, () => void, (data: T) => void] {
  const [loaded, setLoaded] = useState<Loaded<T>>({ state: "loading" });
  const [turn, setTurn] = useState(0);
  useEffect(() => {
    let live = true;
    read().then(
      (data) => live && setLoaded({ state: "ready", data }),
      (e) => {
        if (!live) return;
        if (e instanceof ApiError && e.kind === "signed-out") onSignedOut();
        else setLoaded({ state: "failed" });
      },
    );
    return () => {
      live = false;
    };
    // read and onSignedOut are the same functions for the life of a screen,
    // so only a reload reads again.
  }, [turn]);
  const reload = useCallback(() => setTurn((n) => n + 1), []);
  const put = useCallback((data: T) => setLoaded({ state: "ready", data }), []);
  return [loaded, reload, put];
}

export function Waiting({ state, what, onRetry }: { state: "loading" | "failed"; what: string; onRetry: () => void }) {
  if (state === "loading") {
    return (
      <p className="quiet" aria-busy="true">
        Loading.
      </p>
    );
  }
  return (
    <div className="set-stack">
      <p className="quiet">{what} could not be read. Check that the server is running.</p>
      <div>
        <button type="button" className="button" onClick={onRetry}>
          <Icon name="retry" size={16} />
          Try again
        </button>
      </div>
    </div>
  );
}

export function Section({ title, note, children }: { title: string; note?: ReactNode; children?: ReactNode }) {
  return (
    <section className="set-section">
      <div className="set-section-head">
        <h2 className="set-heading">{title}</h2>
        {note && <p className="set-note">{note}</p>}
      </div>
      {children}
    </section>
  );
}
