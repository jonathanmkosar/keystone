// Settings → Admin access (P15-01): every time an admin account read or
// changed something of the person's, newest first: when, which admin, what
// was read, and whether it went through. The entries are the person's own
// audit log's; the admin cannot read them. There is no mockup, so the
// screen is provisional. docs/architecture/admin.md.

import { useEffect, useState } from "react";
import { ApiError } from "./api";
import { listAdminAccess, type AdminAccess } from "./adminCalls";
import { Icon } from "./Icon";

type Listed = { kind: "loading" } | { kind: "failed" } | { kind: "ready"; rows: AdminAccess[] };

// The facts an admin may read, in words.
const factWords: Record<string, string> = {
  last_sign_in: "when you last signed in",
};

function what(a: AdminAccess): string {
  if (a.key) return `changed the setting ${a.key}`;
  if (a.read.length > 0) return "read " + a.read.map((f) => factWords[f] ?? f.replaceAll("_", " ")).join(", ");
  return a.action;
}

const outcomeWords: Record<string, string> = { ok: "Done", refused: "Refused", error: "Failed", started: "Did not finish" };

export function AdminAccessSettings({ onSignedOut }: { onSignedOut: () => void }) {
  const [listed, setListed] = useState<Listed>({ kind: "loading" });
  const [turn, setTurn] = useState(0);

  useEffect(() => {
    let live = true;
    listAdminAccess().then(
      (rows) => live && setListed({ kind: "ready", rows }),
      (e: unknown) => {
        if (!live) return;
        if (e instanceof ApiError && e.kind === "signed-out") onSignedOut();
        else setListed({ kind: "failed" });
      },
    );
    return () => {
      live = false;
    };
    // onSignedOut is the same function for the life of the screen.
  }, [turn]);

  return (
    <section className="set-section">
      <div className="set-section-head">
        <h2 className="set-heading">Admin access</h2>
        <p className="set-note">
          An admin account sees people's numbers and settings, never their chats, files, memory or logs. Each time one read or changed something of yours, it is written in your own audit log, and listed
          here. The admin cannot read this list.
        </p>
      </div>
      {listed.kind === "loading" && (
        <p className="quiet" aria-busy="true">
          Loading.
        </p>
      )}
      {listed.kind === "failed" && (
        <div className="set-stack">
          <p className="quiet">The list could not be read. Check that the server is running.</p>
          <div>
            <button type="button" className="button" onClick={() => setTurn((n) => n + 1)}>
              <Icon name="retry" size={16} />
              Try again
            </button>
          </div>
        </div>
      )}
      {listed.kind === "ready" && listed.rows.length === 0 && <p className="quiet">No admin has read or changed anything of yours.</p>}
      {listed.kind === "ready" && listed.rows.length > 0 && (
        <div className="set-card">
          {listed.rows.map((a) => (
            <div className="set-item" key={a.requestId} data-request={a.requestId}>
              <div className="set-item-main">
                <span className="set-item-title">
                  {a.by || "An admin"} {what(a)}
                </span>
                <span className="set-hint">
                  {new Date(a.at).toLocaleString()} · <span className="set-mono">{a.action}</span>
                </span>
              </div>
              <div className="set-control">
                <span className={a.outcome === "ok" ? "set-pill is-ok" : "set-pill"}>{outcomeWords[a.outcome] ?? a.outcome}</span>
              </div>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
