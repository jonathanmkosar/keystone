// The Memory screen (P6-07), provisional on the memory mockups (P1-06),
// which are not approved: built from the design tokens alone. It shows
// what the person's knowledge store remembers, by space: the facts the
// memory pass added, each with the chat it came from, Correct and Forget;
// the space's pages; and beside them the profile and the map of the
// store. docs/architecture/memory-screens.md has the server's side.

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { ApiError } from "./api";
import { useChats } from "./chats";
import { Icon } from "./Icon";
import { correctFact, forgetFact, memoryOverview, type MemoryOverview, type MemorySpace, type Remembered } from "./memoryCalls";
import "./memory.css";

type Loaded = { state: "loading" } | { state: "ready"; memory: MemoryOverview } | { state: "failed"; why: string };

function failure(e: unknown, fallback: string): string {
  if (e instanceof ApiError) {
    if (e.kind === "signed-out") return "You are signed out. Sign in, then try again.";
    if (e.said && e.status >= 400 && e.status < 500) {
      const s = e.said.charAt(0).toUpperCase() + e.said.slice(1);
      return /[.!?]$/.test(s) ? s : `${s}.`;
    }
    if (e.kind === "not-ready") return "The server did not answer. Check that it is running.";
  }
  return fallback;
}

function day(when: string): string {
  const d = new Date(when);
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}

function storeName(adapter: string): string {
  if (adapter === "built-in") return "Keystone's built-in store";
  if (adapter === "waymarks") return "Waymarks";
  return "No knowledge store";
}

type Editing = { source: string; fact: string; mode: "correct" | "forget" } | null;

/** One fact, with Correct and Forget. */
function Fact({
  m,
  fact,
  editing,
  busy,
  onEdit,
  onCorrect,
  onForget,
}: {
  m: Remembered;
  fact: string;
  editing: Editing;
  busy: boolean;
  onEdit: (e: Editing) => void;
  onCorrect: (text: string) => void;
  onForget: () => void;
}) {
  const mine = editing !== null && editing.source === m.source_id && editing.fact === fact;
  const [draft, setDraft] = useState(fact);
  useEffect(() => {
    if (mine && editing?.mode === "correct") setDraft(fact);
  }, [mine, editing, fact]);

  if (mine && editing?.mode === "correct") {
    const save = (e: FormEvent) => {
      e.preventDefault();
      if (draft.trim() && draft.trim() !== fact) onCorrect(draft.trim());
    };
    return (
      <li className="mem-fact is-editing">
        <form className="mem-edit" onSubmit={save}>
          <label className="mem-edit-label" htmlFor={`fix-${m.source_id}`}>
            Correct the fact
          </label>
          <textarea id={`fix-${m.source_id}`} className="mem-edit-box" value={draft} rows={2} autoFocus onChange={(e) => setDraft(e.target.value)} />
          <span className="mem-edit-actions">
            <button type="submit" className="button button-primary" disabled={busy || !draft.trim() || draft.trim() === fact}>
              Save
            </button>
            <button type="button" className="button" disabled={busy} onClick={() => onEdit(null)}>
              Cancel
            </button>
          </span>
        </form>
      </li>
    );
  }
  return (
    <li className="mem-fact">
      <span className="mem-fact-text">{fact}</span>
      {mine && editing?.mode === "forget" ? (
        <span className="mem-confirm" role="alert">
          <span>Forget this fact?</span>
          <button type="button" className="button mem-danger" disabled={busy} onClick={onForget}>
            Forget
          </button>
          <button type="button" className="button" disabled={busy} onClick={() => onEdit(null)}>
            Keep
          </button>
        </span>
      ) : (
        <span className="mem-fact-actions">
          <button type="button" className="mem-link" disabled={busy} onClick={() => onEdit({ source: m.source_id, fact, mode: "correct" })}>
            <Icon name="pencil" size={14} />
            Correct
          </button>
          <button type="button" className="mem-link" disabled={busy} onClick={() => onEdit({ source: m.source_id, fact, mode: "forget" })}>
            <Icon name="trash" size={14} />
            Forget
          </button>
        </span>
      )}
    </li>
  );
}

/** One space: its facts, by the reply they came from, and its pages. */
function Space({
  sp,
  editing,
  busy,
  onEdit,
  onCorrect,
  onForget,
}: {
  sp: MemorySpace;
  editing: Editing;
  busy: boolean;
  onEdit: (e: Editing) => void;
  onCorrect: (m: Remembered, fact: string, text: string) => void;
  onForget: (m: Remembered, fact: string) => void;
}) {
  // A sensitive space's facts are shown only when asked for.
  const [shown, setShown] = useState(!sp.sensitive);
  const facts = sp.memories.reduce((n, m) => n + m.facts.length, 0);
  return (
    <section className="mem-space" aria-labelledby={`space-${sp.name}`}>
      <header className="mem-space-head">
        <h2 id={`space-${sp.name}`} className="mem-space-name">
          {sp.name}
        </h2>
        {sp.sensitive && <span className="mem-badge">Sensitive</span>}
        <span className="quiet mem-count">
          {facts === 1 ? "1 fact" : `${facts} facts`} · {sp.pages.length + sp.more_pages === 1 ? "1 page" : `${sp.pages.length + sp.more_pages} pages`}
        </span>
      </header>
      {sp.sensitive && (
        <p className="quiet mem-small">
          A sensitive space: its content reaches only models marked local or trusted.{" "}
          <button type="button" className="mem-link" onClick={() => setShown((s) => !s)}>
            {shown ? "Hide" : "Show"}
          </button>
        </p>
      )}
      {shown && sp.memories.length > 0 && (
        <ul className="mem-sources">
          {sp.memories.map((m) => (
            <li key={m.source_id} className="mem-source">
              <p className="mem-from">
                From{" "}
                {m.chat_gone ? (
                  <span>a chat since deleted</span>
                ) : (
                  <Link to={`/chats/${m.chat_id}`}>{m.chat_title || "an untitled chat"}</Link>
                )}
                <span className="mem-mono"> · {day(m.added)}</span>
              </p>
              <ul className="mem-facts">
                {m.facts.map((f) => (
                  <Fact
                    key={f}
                    m={m}
                    fact={f}
                    editing={editing}
                    busy={busy}
                    onEdit={onEdit}
                    onCorrect={(text) => onCorrect(m, f, text)}
                    onForget={() => onForget(m, f)}
                  />
                ))}
              </ul>
            </li>
          ))}
        </ul>
      )}
      {shown && sp.memories.length === 0 && <p className="quiet mem-small">The memory pass has added nothing here.</p>}
      {shown && sp.pages.length > 0 && (
        <details className="mem-pages">
          <summary>Pages in {sp.name}</summary>
          <ul>
            {sp.pages.map((p) => (
              <li key={p.id}>
                <span>{p.title}</span> <span className="mem-mono quiet">{p.path}</span>
              </li>
            ))}
            {sp.more_pages > 0 && <li className="quiet">and {sp.more_pages} more</li>}
          </ul>
          <p className="quiet mem-small">Pages are written in the store itself. To change one, change it there.</p>
        </details>
      )}
    </section>
  );
}

export function Memory() {
  const { lost } = useChats();
  const [loaded, setLoaded] = useState<Loaded>({ state: "loading" });
  const [editing, setEditing] = useState<Editing>(null);
  const [busy, setBusy] = useState(false);
  const [said, setSaid] = useState<{ text: string; problem: boolean } | null>(null);

  const load = useCallback(async () => {
    try {
      setLoaded({ state: "ready", memory: await memoryOverview() });
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") lost();
      else setLoaded({ state: "failed", why: failure(e, "What is remembered could not be read.") });
    }
  }, [lost]);

  useEffect(() => {
    void load();
  }, [load]);

  async function change(work: () => Promise<{ note: string }>) {
    setBusy(true);
    setSaid(null);
    try {
      const done = await work();
      setSaid({ text: done.note, problem: false });
      setEditing(null);
      await load();
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") lost();
      else setSaid({ text: failure(e, "The change could not be made. Nothing was changed."), problem: true });
      if (e instanceof ApiError && e.status === 404) await load();
    } finally {
      setBusy(false);
    }
  }

  if (loaded.state === "loading") {
    return (
      <div className="mem" aria-busy="true">
        <p className="quiet mem-pad">Reading what is remembered.</p>
      </div>
    );
  }
  if (loaded.state === "failed") {
    return (
      <div className="mem">
        <div className="mem-pad">
          <h1 className="page-title">Memory</h1>
          <p className="quiet">{loaded.why}</p>
          <button type="button" className="button" onClick={() => void load()}>
            <Icon name="retry" size={16} />
            Try again
          </button>
        </div>
      </div>
    );
  }

  const m = loaded.memory;
  const remembered = m.spaces.some((sp) => sp.memories.length > 0);
  return (
    <div className="mem">
      <header className="mem-head">
        <div>
          <h1 className="page-title">Memory</h1>
          <p className="quiet mem-store">
            {storeName(m.store.adapter)}. {m.store.note}
          </p>
        </div>
      </header>
      {m.store.unavailable && (
        <p className="mem-problem" role="alert">
          {m.store.unavailable}
        </p>
      )}
      {said && (
        <p className={said.problem ? "mem-problem" : "mem-said"} role={said.problem ? "alert" : "status"}>
          {said.text}
        </p>
      )}
      <div className="mem-body">
        <div className="mem-main">
          {!remembered && m.store.adapter !== "none" && (
            <p className="quiet mem-small">
              Nothing has been remembered from your chats yet. After each reply, the memory pass adds the lasting facts it finds, unless the chat is incognito.
            </p>
          )}
          {m.spaces.map((sp) => (
            <Space
              key={sp.name}
              sp={sp}
              editing={editing}
              busy={busy}
              onEdit={(e) => {
                setEditing(e);
                setSaid(null);
              }}
              onCorrect={(mem, fact, text) => void change(() => correctFact(mem.source_id, fact, text))}
              onForget={(mem, fact) => void change(() => forgetFact(mem.source_id, fact))}
            />
          ))}
          {m.more > 0 && <p className="quiet mem-small">{m.more === 1 ? "1 older source is" : `${m.more} older sources are`} not listed.</p>}
        </div>
        <aside className="mem-side">
          <section className="mem-card">
            <h2 className="mem-card-title">Profile</h2>
            {m.profile ? <p className="mem-profile">{m.profile}</p> : <p className="quiet mem-small">You have written no profile.</p>}
            <p className="quiet mem-small">
              It starts every chat, with every model, hosted ones too. <Link to="/settings/profile">Edit it in Settings</Link>.
            </p>
          </section>
          <section className="mem-card">
            <h2 className="mem-card-title">Map of your store</h2>
            {m.map ? <pre className="mem-map">{m.map}</pre> : <p className="quiet mem-small">There is no map: the store holds no pages yet.</p>}
            <p className="quiet mem-small">Built from the store's pages and rebuilt when they change. A model that may not read sensitive content is sent it without the sensitive spaces.</p>
          </section>
        </aside>
      </div>
    </div>
  );
}
