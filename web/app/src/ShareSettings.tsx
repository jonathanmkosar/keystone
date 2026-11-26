// Settings → Shared links (P10-04): every share link the person has made,
// the newest first, with what it shows, when it was made, how often it was
// opened and whether it still opens, and Turn off. There is no settings
// mockup, so the screen is provisional. docs/architecture/share.md.

import { useEffect, useState } from "react";
import { Link } from "react-router";
import { ApiError } from "./api";
import { Icon } from "./Icon";
import { allLinks, dayOf, linkLine, revokeLink, stateWords, type ShareLink } from "./share";
import "./share.css";

type Listed = { kind: "loading" } | { kind: "failed" } | { kind: "ready"; links: ShareLink[] };

function LinkRow({ link, onChanged, onSignedOut }: { link: ShareLink; onChanged: (l: ShareLink) => void; onSignedOut: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function turnOff() {
    setBusy(true);
    setError("");
    try {
      onChanged(await revokeLink(link.id));
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") onSignedOut();
      else setError("The link could not be turned off. Check that the server is running.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="set-item" data-link={link.id}>
      <div className="set-item-main">
        <Link to={`/chats/${link.chat_id}`} className="set-item-title share-chat-title">
          {link.chat_title || "An untitled chat"}
        </Link>
        <span className="set-hint">
          {linkLine(link)}
          {link.last_viewed && ` · last opened ${dayOf(link.last_viewed)}`}
        </span>
        {link.reason && <span className="set-hint">{link.reason}</span>}
        {error && (
          <span className="form-error" role="alert">
            {error}
          </span>
        )}
      </div>
      <div className="set-control">
        <span className={link.state === "open" ? "set-pill is-ok" : "set-pill"}>{stateWords[link.state]}</span>
        {link.state !== "revoked" && (
          <button type="button" className="set-reset" disabled={busy} onClick={() => void turnOff()}>
            Turn off
          </button>
        )}
      </div>
    </div>
  );
}

export function ShareSettings({ onSignedOut }: { onSignedOut: () => void }) {
  const [listed, setListed] = useState<Listed>({ kind: "loading" });
  const [turn, setTurn] = useState(0);

  useEffect(() => {
    let live = true;
    allLinks().then(
      (links) => live && setListed({ kind: "ready", links }),
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
        <h2 className="set-heading">Shared links</h2>
        <p className="set-note">
          A shared link lets anyone who has it read one chat without signing in, and nothing else of yours. Make one with Share at the top of a chat. A link is shown once, when it is made; turn off one you no
          longer want, and anyone who opens it is told it was turned off. A chat that holds content from a sensitive space, and an incognito chat, are never shared. How long a new link opens, and whether images
          and artifacts show, are on <Link to="/settings/general">General</Link>.
        </p>
      </div>
      {listed.kind === "loading" && (
        <p className="quiet" aria-busy="true">
          Loading.
        </p>
      )}
      {listed.kind === "failed" && (
        <div className="set-stack">
          <p className="quiet">The links could not be read. Check that the server is running.</p>
          <div>
            <button type="button" className="button" onClick={() => setTurn((n) => n + 1)}>
              <Icon name="retry" size={16} />
              Try again
            </button>
          </div>
        </div>
      )}
      {listed.kind === "ready" && listed.links.length === 0 && <p className="quiet">No shared links yet.</p>}
      {listed.kind === "ready" && listed.links.length > 0 && (
        <div className="set-card">
          {listed.links.map((l) => (
            <LinkRow
              key={l.id}
              link={l}
              onSignedOut={onSignedOut}
              onChanged={(got) => setListed((s) => (s.kind === "ready" ? { ...s, links: s.links.map((x) => (x.id === got.id ? got : x)) } : s))}
            />
          ))}
        </div>
      )}
    </section>
  );
}
