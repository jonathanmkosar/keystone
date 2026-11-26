// Connect Google (P12-02): the Google section of Settings → Connectors. It
// says whether Google is set up on this install and what the admin must add;
// connects the person's Google account for Gmail, Calendar or both; and
// finishes a sign-in when Google sends the browser back to
// /settings/connectors/google with a code. The code is posted once and taken
// out of the address bar at once. docs/architecture/google.md describes it.
// Every call is in api.ts.

import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { ApiError, disconnectGoogle, finishGoogle, googleStatus, startGoogle, type GoogleStatus } from "./api";
import { Icon } from "./Icon";

/** The states this page has finished, so a second render does not post one again. */
const finished = new Set<string>();

function said(e: unknown, fallback: string): string {
  if (e instanceof ApiError && e.said && e.status >= 400 && e.status < 500 && e.status !== 404) {
    const t = e.said.trim();
    const first = t.charAt(0).toUpperCase() + t.slice(1);
    return /[.!?]$/.test(first) ? first : first + ".";
  }
  if (e instanceof ApiError && e.kind === "not-ready") return "The server did not answer. Check that it is running.";
  return fallback;
}

const serviceNames: Record<string, string> = { gmail: "Gmail", calendar: "Calendar" };

export function GoogleSettings({ onSignedOut, onChanged }: { onSignedOut: () => void; onChanged: () => void }) {
  const location = useLocation();
  const navigate = useNavigate();
  const [status, setStatus] = useState<GoogleStatus | null>(null);
  const [failed, setFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [chosen, setChosen] = useState<Record<string, boolean>>({ gmail: true, calendar: true });
  const [asking, setAsking] = useState(false);

  function fail(e: unknown, fallback: string) {
    if (e instanceof ApiError && e.kind === "signed-out") onSignedOut();
    else setError(said(e, fallback));
  }

  useEffect(() => {
    let live = true;
    const back = location.pathname.endsWith("/connectors/google") ? new URLSearchParams(location.search) : null;
    const state = back?.get("state") ?? "";
    if (back) navigate("/settings/connectors", { replace: true }); // the code leaves the address bar and the history
    if (state !== "" && !finished.has(state)) {
      finished.add(state);
      setBusy(true);
      finishGoogle({ state, code: back?.get("code") ?? undefined, error: back?.get("error") ?? undefined })
        .then(
          (got) => {
            if (!live) return;
            setStatus(got);
            setNotice(got.connected ? "Google is connected. Its tools are on in your chats, and the tools menu beside the model picker switches them for one chat." : "");
            onChanged();
          },
          (e) => {
            if (!live) return;
            fail(e, "The sign-in could not be finished.");
            return googleStatus().then((got) => live && setStatus(got));
          },
        )
        .catch(() => live && setFailed(true))
        .finally(() => live && setBusy(false));
      return () => {
        live = false;
      };
    }
    googleStatus().then(
      (got) => live && setStatus(got),
      (e) => {
        if (!live) return;
        if (e instanceof ApiError && e.kind === "signed-out") onSignedOut();
        else setFailed(true);
      },
    );
    return () => {
      live = false;
    };
    // Read once, when the screen opens or Google sends the browser back.
  }, []);

  async function connect() {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const services = Object.keys(chosen).filter((k) => chosen[k]);
      const { url } = await startGoogle(services);
      window.location.assign(url);
    } catch (e) {
      fail(e, "The sign-in could not be started.");
      setBusy(false);
    }
  }

  async function disconnect() {
    setBusy(true);
    setError("");
    setNotice("");
    setAsking(false);
    try {
      setStatus(await disconnectGoogle());
      setNotice("Disconnected. Keystone asked Google to forget the sign-in.");
      onChanged();
    } catch (e) {
      fail(e, "Google could not be disconnected.");
    } finally {
      setBusy(false);
    }
  }

  const redirect = status ? status.redirect || window.location.origin + status.redirect_path : "";

  return (
    <section className="set-section" data-section="google">
      <div className="set-section-head">
        <h2 className="set-heading">Google</h2>
        <p className="set-note">
          Gmail and Google Calendar, with your own Google account. A chat can search and read your mail and your calendar, which is outside content. An email is sent, and guests are told of a change, only
          when you approve that one call on its card, whatever you set on the Approvals screen. A draft, and a change to an event only you are on, ask first unless you set them otherwise.
        </p>
      </div>
      {!status && !failed && (
        <p className="quiet" aria-busy="true">
          Loading.
        </p>
      )}
      {failed && <p className="quiet">Your Google sign-in could not be read. Check that the server is running.</p>}
      {status && (
        <div className="set-card set-stack">
          {!status.set_up && (
            <>
              <p className="google-line">Google is not set up on this install yet. The admin adds:</p>
              <ul className="google-missing">
                {status.missing.map((m) => (
                  <li key={m}>{m}</li>
                ))}
              </ul>
              <p className="set-hint">The OAuth client must have this return address registered:</p>
              <code className="con-command">{redirect}</code>
              <p className="set-hint">The steps are in docs/runbooks/google.md.</p>
            </>
          )}
          {status.set_up && status.connected && (
            <>
              <p className="google-line">
                Connected{status.email ? " as " : ""}
                {status.email && <strong>{status.email}</strong>}.
              </p>
              <span className="set-pills">
                {status.services.map((s) => (
                  <span key={s.service} className={s.added ? "set-pill is-ok" : "set-pill is-bad"}>
                    {serviceNames[s.service] ?? s.label} {s.added ? (s.enabled ? "on" : "off") : s.granted ? "not added" : "not allowed"}
                  </span>
                ))}
              </span>
              {status.services
                .filter((s) => s.problem)
                .map((s) => (
                  <span key={s.service} className="form-error">
                    {s.label}: {s.problem}
                  </span>
                ))}
            </>
          )}
          {status.set_up && (
            <div className="set-stack">
              <div className="set-row">
                {["gmail", "calendar"].map((s) => (
                  <label key={s} className="set-check">
                    <input type="checkbox" checked={chosen[s]} disabled={busy} onChange={(e) => setChosen((c) => ({ ...c, [s]: e.target.checked }))} />
                    {serviceNames[s]}
                  </label>
                ))}
              </div>
              <div className="set-row">
                <button type="button" className={status.connected ? "button" : "button button-primary"} disabled={busy || (!chosen.gmail && !chosen.calendar)} onClick={() => void connect()}>
                  <Icon name="plug" size={16} />
                  {status.connected ? "Connect again" : "Connect Google"}
                </button>
                {status.connected &&
                  (asking ? (
                    <span className="set-row">
                      <button type="button" className="button button-danger set-small" disabled={busy} onClick={() => void disconnect()}>
                        Disconnect Google
                      </button>
                      <button type="button" className="button set-small" onClick={() => setAsking(false)}>
                        Keep
                      </button>
                    </span>
                  ) : (
                    <button type="button" className="button set-small" disabled={busy} onClick={() => setAsking(true)}>
                      Disconnect
                    </button>
                  ))}
              </div>
              <p className="set-hint">Google asks you to sign in and to allow what is ticked, then sends you back here.</p>
            </div>
          )}
          {notice && (
            <p className="set-hint" role="status">
              {notice}
            </p>
          )}
          {error && (
            <p className="form-error" role="alert">
              {error}
            </p>
          )}
        </div>
      )}
    </section>
  );
}
