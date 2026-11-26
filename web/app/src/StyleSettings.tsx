// The Styles settings screen (P9-05): every style a person may choose, the
// built-in ones to read and their own to make, change and delete, and which
// one a new chat starts with. There is no styles mockup, so it is drawn
// like the other settings screens and is provisional. Every call is in
// styleCalls.ts; docs/architecture/styles.md describes the routes.

import { useEffect, useState, type FormEvent } from "react";
import { ApiError } from "./api";
import { Icon } from "./Icon";
import { changeStyle, createStyle, deleteStyle, listStyles, setDefaultStyle, type Style, type StyleList, type StyleLimits } from "./styleCalls";
import "./style-presets.css";

/** The server's refusals start in lower case. On the screen they are sentences. */
function sentence(text: string): string {
  const t = text.trim();
  if (t === "") return t;
  const first = t[0].toUpperCase() + t.slice(1);
  return /[.!?]$/.test(first) ? first : first + ".";
}

/** What a refused or failed call is told to the person as. */
function said(e: unknown, fallback: string): string {
  if (e instanceof ApiError) {
    if (e.said && e.status >= 400 && e.status < 500 && e.status !== 404) return sentence(e.said);
    if (e.kind === "not-ready") return "The server did not answer. Check that it is running.";
  }
  return fallback;
}

/** The name and instructions of a style being made or changed. */
function StyleForm({
  limits,
  initial,
  saveLabel,
  onSave,
  onCancel,
  onSignedOut,
}: {
  limits: StyleLimits;
  initial?: Style;
  saveLabel: string;
  onSave: (name: string, instructions: string) => Promise<void>;
  onCancel?: () => void;
  onSignedOut: () => void;
}) {
  const [name, setName] = useState(initial?.name ?? "");
  const [text, setText] = useState(initial?.instructions ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const count = [...text].length;
  const over = count > limits.instructions_max_chars;
  const unchanged = initial !== undefined && name.trim() === initial.name && text.trim() === initial.instructions;

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await onSave(name, text);
      if (!initial) {
        setName("");
        setText("");
      }
    } catch (err) {
      if (err instanceof ApiError && err.kind === "signed-out") onSignedOut();
      else setError(said(err, "The style could not be saved. Check that the server is running."));
    } finally {
      setBusy(false);
    }
  }

  const prefix = initial ? `style-${initial.id}` : "style-new";
  return (
    <form className="set-stack style-form" onSubmit={submit}>
      <label className="field" htmlFor={`${prefix}-name`}>
        <span className="field-label">Name</span>
        <input
          id={`${prefix}-name`}
          className="input"
          value={name}
          maxLength={limits.name_max_chars}
          disabled={busy}
          placeholder="Plain and brief"
          onChange={(e) => setName(e.target.value)}
        />
      </label>
      <label className="field" htmlFor={`${prefix}-text`}>
        <span className="field-label">Instructions</span>
        <textarea
          id={`${prefix}-text`}
          className="input set-text"
          rows={4}
          value={text}
          disabled={busy}
          placeholder="How replies should be written: their length, tone, layout and the words to use or avoid."
          onChange={(e) => setText(e.target.value)}
        />
      </label>
      <div className="set-row">
        <button type="submit" className="button button-primary" disabled={busy || over || name.trim() === "" || text.trim() === "" || unchanged}>
          {saveLabel}
        </button>
        {onCancel && (
          <button type="button" className="button" disabled={busy} onClick={onCancel}>
            Cancel
          </button>
        )}
        <span className={over ? "set-count is-over" : "set-count"}>
          {count} of {limits.instructions_max_chars} characters
        </span>
        {error && (
          <span className="form-error" role="alert">
            {error}
          </span>
        )}
      </div>
    </form>
  );
}

/** One style in the list: its name, its instructions and what can be done with it. */
function StyleRow({
  style,
  isDefault,
  limits,
  onList,
  onSignedOut,
}: {
  style: Style;
  isDefault: boolean;
  limits: StyleLimits;
  onList: (change: (l: StyleList) => StyleList) => void;
  onSignedOut: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function run(act: () => Promise<void>, fallback: string) {
    setBusy(true);
    setError("");
    try {
      await act();
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") onSignedOut();
      else setError(said(e, fallback));
    } finally {
      setBusy(false);
    }
  }

  const makeDefault = () =>
    run(async () => {
      const got = await setDefaultStyle(style.id);
      onList(() => got);
    }, "The default could not be changed. Check that the server is running.");

  const remove = () =>
    run(async () => {
      await deleteStyle(style.id);
      // Read again: a default that named the style went back to the shipped one.
      const got = await listStyles().catch(() => undefined);
      onList((l) => got ?? { ...l, styles: l.styles.filter((s) => s.id !== style.id) });
    }, "The style could not be deleted. Check that the server is running.");

  return (
    <li className="set-item style-row" data-style={style.id}>
      <div className="set-item-main">
        <div className="style-row-head">
          <span className="style-row-name">{style.name}</span>
          {style.built_in ? <span className="set-pill">Built in</span> : <span className="set-pill is-ok">Yours</span>}
          {isDefault && <span className="set-pill is-ok">Default for new chats</span>}
        </div>
        {editing ? (
          <StyleForm
            limits={limits}
            initial={style}
            saveLabel="Save"
            onSignedOut={onSignedOut}
            onCancel={() => setEditing(false)}
            onSave={async (name, instructions) => {
              const got = await changeStyle(style.id, { name, instructions });
              onList((l) => ({ ...l, styles: l.styles.map((s) => (s.id === got.id ? got : s)) }));
              setEditing(false);
            }}
          />
        ) : (
          <p className="style-row-text">{style.instructions || "No instructions: replies are written as the model writes them, and nothing is added to a chat's calls."}</p>
        )}
        {error && (
          <span className="form-error" role="alert">
            {error}
          </span>
        )}
      </div>
      {!editing && (
        <div className="style-row-actions">
          {!isDefault && (
            <button type="button" className="button set-small" disabled={busy} onClick={() => void makeDefault()}>
              Make default
            </button>
          )}
          {!style.built_in && !confirming && (
            <>
              <button type="button" className="button set-small" disabled={busy} onClick={() => setEditing(true)}>
                <Icon name="pencil" size={16} />
                Edit
              </button>
              <button type="button" className="button set-small" disabled={busy} onClick={() => setConfirming(true)}>
                <Icon name="trash" size={16} />
                Delete
              </button>
            </>
          )}
          {confirming && (
            <>
              <button type="button" className="button set-small style-danger" disabled={busy} onClick={() => void remove()}>
                Delete {style.name}
              </button>
              <button type="button" className="button set-small" disabled={busy} onClick={() => setConfirming(false)}>
                Keep it
              </button>
            </>
          )}
        </div>
      )}
    </li>
  );
}

export function StyleSettings({ onSignedOut }: { onSignedOut: () => void }) {
  const [state, setState] = useState<{ kind: "loading" } | { kind: "failed" } | { kind: "ready"; list: StyleList }>({ kind: "loading" });
  const [turn, setTurn] = useState(0);

  useEffect(() => {
    let live = true;
    listStyles().then(
      (list) => live && setState({ kind: "ready", list }),
      (e) => {
        if (!live) return;
        if (e instanceof ApiError && e.kind === "signed-out") onSignedOut();
        else setState({ kind: "failed" });
      },
    );
    return () => {
      live = false;
    };
    // onSignedOut is the same function for the life of the screen.
  }, [turn]);

  function onList(change: (l: StyleList) => StyleList) {
    setState((s) => (s.kind === "ready" ? { kind: "ready", list: change(s.list) } : s));
  }

  return (
    <section className="set-section">
      <div className="set-section-head">
        <h2 className="set-heading">Styles</h2>
        <p className="set-note">
          A style says how replies are written: their length, tone and layout. Pick one for each chat beside the model in the composer. Its instructions go at the start of every message in that chat, so a
          chat that keeps its style costs no more to continue. A style changes only the writing: never what Keystone may do, what asks you first, or what a model may read.
        </p>
      </div>
      {state.kind === "loading" && (
        <p className="quiet" aria-busy="true">
          Loading.
        </p>
      )}
      {state.kind === "failed" && (
        <div className="set-stack">
          <p className="quiet">The styles could not be read. Check that the server is running.</p>
          <div>
            <button type="button" className="button" onClick={() => setTurn((n) => n + 1)}>
              <Icon name="retry" size={16} />
              Try again
            </button>
          </div>
        </div>
      )}
      {state.kind === "ready" && <Ready list={state.list} onList={onList} onSignedOut={onSignedOut} />}
    </section>
  );
}

function Ready({ list, onList, onSignedOut }: { list: StyleList; onList: (change: (l: StyleList) => StyleList) => void; onSignedOut: () => void }) {
  const own = list.styles.filter((s) => !s.built_in).length;
  const full = own >= list.limits.max_styles;
  const known = list.styles.some((s) => s.id === list.default);
  return (
    <div className="set-stack">
      <div className="set-card">
        <ul className="style-list" aria-label="Styles">
          {list.styles.map((st) => (
            <StyleRow key={st.id} style={st} isDefault={st.id === list.default} limits={list.limits} onList={onList} onSignedOut={onSignedOut} />
          ))}
        </ul>
        {!known && <p className="set-hint style-note">Your default names a style that is not there, so a new chat starts with no style. Make one of these the default.</p>}
      </div>
      <div className="set-card style-new">
        <div className="style-new-head">
          <h3 className="set-subheading">New style</h3>
          <p className="set-hint">
            {own} of {list.limits.max_styles} styles of your own.{full ? " Delete one to make another." : ""}
          </p>
        </div>
        {!full && (
          <StyleForm
            limits={list.limits}
            saveLabel="Make style"
            onSignedOut={onSignedOut}
            onSave={async (name, instructions) => {
              const made = await createStyle(name, instructions);
              onList((l) => ({ ...l, styles: [...l.styles.filter((s) => s.built_in), ...[...l.styles.filter((s) => !s.built_in), made].sort((a, b) => a.name.localeCompare(b.name))] }));
            }}
          />
        )}
      </div>
    </div>
  );
}
