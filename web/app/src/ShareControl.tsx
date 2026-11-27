// The Share button in a chat's header (P10-04), where the mockups draw it
// (MainDark.html, MainLight.html), and the popover it opens: make a
// read-only link to this chat, as it is now or as it is at this moment,
// copy it, and turn off this chat's links. The popover has no mockup, so
// it is provisional. docs/architecture/share.md.

import { useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { ApiError, listSettings } from "./api";
import { useChats } from "./chats";
import { CopyButton } from "./Markdown";
import { SheetRow, useSheet } from "./PhoneSheet";
import { addressOf, chatLinks, linkLine, makeLink, revokeLink, stateWords, type MadeLink, type ShareLink, type ShareView } from "./share";
import "./share.css";

export function ShareControl({ chatId }: { chatId: string }) {
  const { lost } = useChats();
  const sheet = useSheet();
  const [open, setOpen] = useState(false);
  const [view, setView] = useState<ShareView>("live");
  const [links, setLinks] = useState<ShareLink[] | null>(null);
  const [made, setMade] = useState<MadeLink | null>(null);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState("");
  const box = useRef<HTMLDivElement>(null);

  // What the popover lists, and the view the person's setting chooses, read
  // each time it opens.
  useEffect(() => {
    if (!open) return;
    let live = true;
    chatLinks(chatId).then(
      (got) => live && setLinks(got),
      (e: unknown) => {
        if (!live) return;
        if (e instanceof ApiError && e.kind === "signed-out") lost();
        else setProblem("This chat's links could not be read. Check that the server is running.");
      },
    );
    listSettings().then(
      ({ settings }) => {
        const v = settings.find((s) => s.key === "share.default_view")?.value;
        if (live && (v === "live" || v === "snapshot")) setView(v);
      },
      () => {
        // The choice stays as it is.
      },
    );
    return () => {
      live = false;
    };
  }, [open, chatId, lost]);

  // Escape and a click outside close it.
  useEffect(() => {
    if (!open) return;
    const key = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    const click = (e: MouseEvent) => {
      if (box.current && !box.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("keydown", key);
    document.addEventListener("mousedown", click);
    return () => {
      document.removeEventListener("keydown", key);
      document.removeEventListener("mousedown", click);
    };
  }, [open]);

  function failed(e: unknown, fallback: string) {
    if (e instanceof ApiError && e.kind === "signed-out") lost();
    else if (e instanceof ApiError && e.status === 409 && e.said !== "") setProblem(e.said);
    else setProblem(fallback);
  }

  async function make() {
    setBusy(true);
    setProblem("");
    try {
      const got = await makeLink(chatId, view);
      setMade(got);
      setLinks((l) => [got.link, ...(l ?? [])]);
    } catch (e) {
      failed(e, "The link could not be made. Check that the server is running.");
    } finally {
      setBusy(false);
    }
  }

  async function turnOff(id: string) {
    setBusy(true);
    setProblem("");
    try {
      const got = await revokeLink(id);
      setLinks((l) => (l ?? []).map((x) => (x.id === got.id ? got : x)));
      if (made?.link.id === id) setMade(null);
    } catch (e) {
      failed(e, "The link could not be turned off. Check that the server is running.");
    } finally {
      setBusy(false);
    }
  }

  const openLinks = (links ?? []).filter((l) => l.state === "open" || l.state === "refused");
  return (
    <div className={sheet ? "share-control sheet-item" : "share-control"} ref={box}>
      {sheet ? (
        // In the phone's ... sheet it is a row, and the popover opens under it (decision 0590).
        <SheetRow icon="link" name="Share" state="a read-only link" expanded={open} onClick={() => setOpen((o) => !o)} />
      ) : (
        <button type="button" className="button share-button" aria-expanded={open} aria-haspopup="dialog" onClick={() => setOpen((o) => !o)}>
          Share
        </button>
      )}
      {open && (
        <div className="menu share-menu" role="dialog" aria-label="Share this chat">
          <p className="share-lead">Anyone with the link can read this chat without signing in. They see its messages and nothing else of yours: not your other chats, your memory or your settings.</p>
          <fieldset className="share-views">
            <legend className="share-legend">The link shows</legend>
            <label className="share-choice">
              <input type="radio" name={`share-view-${chatId}`} checked={view === "live"} onChange={() => setView("live")} />
              <span>
                The chat as it is when opened
                <span className="share-hint">Messages you send later can be read too.</span>
              </span>
            </label>
            <label className="share-choice">
              <input type="radio" name={`share-view-${chatId}`} checked={view === "snapshot"} onChange={() => setView("snapshot")} />
              <span>
                The chat as it is now
                <span className="share-hint">Later messages stay private.</span>
              </span>
            </label>
          </fieldset>
          <button type="button" className="button button-primary share-make" disabled={busy} onClick={() => void make()}>
            {busy ? "Working…" : "Make a link"}
          </button>
          {made && (
            <div className="share-made" role="status">
              <input className="input share-address" readOnly value={addressOf(made.path)} aria-label="The link" onFocus={(e) => e.currentTarget.select()} />
              <CopyButton text={addressOf(made.path)} label="Copy link" />
              <p className="share-hint">This is the only time the link is shown. Copy it now; a lost link is turned off and made again.</p>
            </div>
          )}
          {problem && (
            <p className="share-problem" role="alert">
              {problem}
            </p>
          )}
          {openLinks.length > 0 && (
            <div className="share-list">
              <div className="share-legend">This chat's links</div>
              {openLinks.map((l) => (
                <div key={l.id} className="share-row">
                  <span className="share-row-text">
                    {linkLine(l)}
                    {l.state !== "open" && <span className="share-row-state">{` · ${stateWords[l.state]}: ${l.reason ?? ""}`}</span>}
                  </span>
                  <button type="button" className="share-off" disabled={busy} onClick={() => void turnOff(l.id)}>
                    Turn off
                  </button>
                </div>
              ))}
            </div>
          )}
          <Link to="/settings/sharing" className="share-all" onClick={() => setOpen(false)}>
            All your links
          </Link>
        </div>
      )}
    </div>
  );
}
