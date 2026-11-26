// The notice on the home screen when a limit is at or past its watch level
// (P11-03): one line for each, linking to Operations. It reads
// GET /api/v1/operations/watch, the same limits as the Operations report,
// so the two never disagree. Drawn after the "Running now" strip of
// design/screens/Home.html with the design tokens; provisional on the
// settings mockups (P1-07). docs/architecture/operations.md has the server's
// side.

import { useEffect, useState } from "react";
import { Link } from "react-router";
import { Icon } from "./Icon";
import { amount, readWatch, type OpsLimit } from "./operationsCalls";
import "./operations.css";

/** What a crossed limit says, in a few words. */
function said(l: OpsLimit): string {
  if (l.problem) return `${l.label}: it could not be read, so it may be stopping work.`;
  if (l.state === "full") return `${l.label} is full: ${amount(l, l.used)} of ${amount(l, l.of)}.`;
  return `${l.label} is at ${l.percent}%: ${amount(l, l.used)} of ${amount(l, l.of)}. You are told from ${l.watch}%.`;
}

/** The notice, or nothing while every limit is under its watch level or the server cannot say. */
export function OperationsNotice() {
  const [crossed, setCrossed] = useState<OpsLimit[]>([]);
  useEffect(() => {
    let live = true;
    readWatch().then(
      (w) => live && setCrossed(w.crossed),
      () => {
        // The notice is a comfort: the home screen stands without it.
      },
    );
    return () => {
      live = false;
    };
  }, []);
  if (crossed.length === 0) return null;
  const full = crossed.some((l) => l.state !== "watch");
  return (
    <section className={full ? "ops-notice is-bad" : "ops-notice"} aria-label="Running out of room" role="status">
      <div className="ops-notice-head">
        <Icon name="alert" size={16} />
        <span className="ops-over">Running out of room</span>
      </div>
      <ul className="ops-notice-list">
        {crossed.map((l) => (
          <li key={l.key}>{said(l)}</li>
        ))}
      </ul>
      <Link to="/operations" className="ops-notice-link">
        See Operations
      </Link>
    </section>
  );
}
