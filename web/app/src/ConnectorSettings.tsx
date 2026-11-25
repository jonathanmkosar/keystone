// The Connectors settings screen (P7-04): each of the person's connectors,
// with its switch, its token, the tools it listed and why it could not be
// reached; a form to add a remote one; and how a program on this machine is
// added, which is from the command line only. docs/architecture/connectors.md
// describes it. Every call is in api.ts.

import { useEffect, useState, type FormEvent } from "react";
import {
  addConnector,
  ApiError,
  changeConnector,
  checkConnector,
  listConnectors,
  removeConnector,
  setSecret,
  type Connector,
  type ConnectorBody,
} from "./api";
import { Icon } from "./Icon";
import "./connectors.css";

/** What a refused or failed call is told to the person as. */
function said(e: unknown, fallback: string): string {
  if (e instanceof ApiError) {
    if (e.said && e.status >= 400 && e.status < 500 && e.status !== 404) {
      const t = e.said.trim();
      const first = t.charAt(0).toUpperCase() + t.slice(1);
      return /[.!?]$/.test(first) ? first : first + ".";
    }
    if (e.kind === "not-ready") return "The server did not answer. Check that it is running.";
  }
  return fallback;
}

/** The body that changes a connector, from what it is now. A command's connector sends no address. */
function bodyOf(c: Connector, change: Partial<ConnectorBody>): ConnectorBody {
  const base: ConnectorBody = { label: c.label, secret: c.secret, enabled: c.enabled, outside: c.outside };
  if (c.transport === "http") Object.assign(base, { url: c.url ?? "", local: c.local });
  return { ...base, ...change };
}

function where(c: Connector): string {
  if (c.transport === "stdio") return [c.command ?? "", ...c.args].join(" ").trim();
  return c.url ?? "";
}

function kindOf(c: Connector): string {
  if (c.transport === "stdio") return "on this machine";
  return c.local ? "your own server" : "remote";
}

/** A button that asks once more before it acts. */
function Remove({ onRemove, disabled }: { onRemove: () => void; disabled?: boolean }) {
  const [asking, setAsking] = useState(false);
  if (!asking) {
    return (
      <button type="button" className="button set-small" disabled={disabled} onClick={() => setAsking(true)}>
        <Icon name="trash" size={16} />
        Remove
      </button>
    );
  }
  return (
    <span className="set-row">
      <button type="button" className="button button-danger set-small" disabled={disabled} onClick={onRemove}>
        Remove this connector
      </button>
      <button type="button" className="button set-small" onClick={() => setAsking(false)}>
        Keep
      </button>
    </span>
  );
}

function ConnectorRow({ c, onChanged, onRemoved, onSignedOut }: { c: Connector; onChanged: (c: Connector) => void; onRemoved: () => void; onSignedOut: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [token, setToken] = useState<string | null>(null);

  async function run(change: () => Promise<void>, fallback: string) {
    setBusy(true);
    setError("");
    try {
      await change();
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") onSignedOut();
      else setError(said(e, fallback));
    } finally {
      setBusy(false);
    }
  }

  async function storeToken(e: FormEvent) {
    e.preventDefault();
    const value = token ?? "";
    setToken(null); // the token leaves the page whether or not it was stored
    if (value === "") return;
    const name = c.secret || `connector.${c.name}`;
    await run(async () => {
      await setSecret(name, value);
      onChanged(await changeConnector(c.name, bodyOf(c, { secret: name })));
    }, "The token could not be stored.");
  }

  const tokenPill = c.secret === "" ? <span className="set-pill">no token</span> : <span className={c.token_present ? "set-pill is-ok" : "set-pill is-bad"}>token {c.token_present ? "stored" : "missing"}</span>;
  return (
    <div className="set-item" data-connector={c.name}>
      <div className="set-item-main">
        <span className="set-item-title">
          {c.label || c.name} <span className="set-mono set-quiet">{c.name}</span>
        </span>
        <span className="set-hint set-mono con-where">{where(c)}</span>
        <span className="set-pills">
          <span className="set-pill">{kindOf(c)}</span>
          {tokenPill}
          {c.outside ? <span className="set-pill">outside content</span> : <span className="set-pill is-ok">trusted results</span>}
          {c.added_by === "command line" && <span className="set-pill">added from the command line</span>}
        </span>
        {c.problem ? (
          <span className="form-error">Could not be reached when last tried: {c.problem}</span>
        ) : c.tools.length > 0 ? (
          <details className="con-tools">
            <summary>
              {c.tools.length} {c.tools.length === 1 ? "tool" : "tools"}
            </summary>
            {c.tools.map((t) => (
              <span key={t.name} className="con-tool">
                <span className="set-mono">{t.name}</span>
                <span className={t.read_only ? "set-pill is-ok" : "set-pill"}>{t.read_only ? "reads" : "changes"}</span>
                <span className="con-tool-text">{t.description}</span>
              </span>
            ))}
          </details>
        ) : (
          <span className="set-hint">{c.listed ? "It listed no tools." : "Not reached yet."}</span>
        )}
        {token !== null && (
          <form className="set-row" onSubmit={(e) => void storeToken(e)}>
            <input
              className="input"
              type="password"
              aria-label={`Token for ${c.label || c.name}`}
              value={token}
              autoComplete="off"
              spellCheck={false}
              disabled={busy}
              onChange={(e) => setToken(e.target.value)}
            />
            <button type="submit" className="button button-primary set-small" disabled={busy || token === ""}>
              Store the token
            </button>
            <button type="button" className="button set-small" onClick={() => setToken(null)}>
              Cancel
            </button>
          </form>
        )}
        {error && (
          <span className="form-error" role="alert">
            {error}
          </span>
        )}
      </div>
      <div className="con-controls">
        <button
          type="button"
          role="switch"
          aria-checked={c.enabled}
          aria-label={`${c.label || c.name}: ${c.enabled ? "on" : "off"}`}
          className={c.enabled ? "set-switch is-on" : "set-switch"}
          disabled={busy}
          onClick={() => void run(async () => onChanged(await changeConnector(c.name, bodyOf(c, { enabled: !c.enabled }))), "The connector could not be switched.")}
        >
          <span className="set-switch-knob" />
          <span className="set-switch-text">{c.enabled ? "On" : "Off"}</span>
        </button>
        <label className="set-check">
          <input
            type="checkbox"
            checked={c.outside}
            disabled={busy}
            onChange={(e) => void run(async () => onChanged(await changeConnector(c.name, bodyOf(c, { outside: e.target.checked }))), "The connector could not be changed.")}
          />
          Results are outside content
        </label>
        <span className="set-row">
          <button type="button" className="button set-small" disabled={busy} onClick={() => void run(async () => onChanged(await checkConnector(c.name)), "The connector could not be checked.")}>
            <Icon name="retry" size={16} />
            Check now
          </button>
          <button type="button" className="button set-small" disabled={busy} onClick={() => setToken(token === null ? "" : null)}>
            {c.secret && c.token_present ? "Replace the token" : "Store a token"}
          </button>
          <Remove disabled={busy} onRemove={() => void run(async () => { await removeConnector(c.name); onRemoved(); }, "The connector could not be removed.")} />
        </span>
      </div>
    </div>
  );
}

function AddForm({ onAdded, onSignedOut }: { onAdded: (c: Connector) => void; onSignedOut: () => void }) {
  const [name, setName] = useState("");
  const [label, setLabel] = useState("");
  const [url, setUrl] = useState("");
  const [local, setLocal] = useState(false);
  const [outside, setOutside] = useState(true);
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function add(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    const value = token;
    setToken(""); // the token leaves the page whether or not it was stored
    try {
      // Added first and reached after, so a connector that is refused leaves
      // no token behind, and one that takes a token is reached with it.
      const n = name.trim();
      const secret = value !== "" ? `connector.${n}` : "";
      const added = await addConnector({ name: n, label: label.trim(), url: url.trim(), local, secret, outside, check: false });
      let shown = added;
      try {
        if (secret !== "") await setSecret(secret, value);
        shown = await checkConnector(added.name);
      } finally {
        onAdded(shown); // listed even when its token or its check failed
      }
      setName("");
      setLabel("");
      setUrl("");
      setLocal(false);
      setOutside(true);
    } catch (err) {
      if (err instanceof ApiError && err.kind === "signed-out") onSignedOut();
      else setError(said(err, "The connector could not be added."));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="set-card set-stack" onSubmit={(e) => void add(e)}>
      <h3 className="set-subheading">Add a remote connector</h3>
      <div className="set-grid">
        <label className="field">
          <span className="field-label">Name</span>
          <input className="input set-mono" value={name} disabled={busy} placeholder="notes" autoComplete="off" spellCheck={false} onChange={(e) => setName(e.target.value)} />
          <span className="set-hint">Lower-case letters, digits and underscores. It starts the name of each of its tools, as notes_search.</span>
        </label>
        <label className="field">
          <span className="field-label">Label</span>
          <input className="input" value={label} disabled={busy} placeholder="My notes" onChange={(e) => setLabel(e.target.value)} />
        </label>
        <label className="field set-wide">
          <span className="field-label">Address</span>
          <input className="input set-mono" value={url} disabled={busy} placeholder="https://mcp.example/mcp" autoComplete="off" spellCheck={false} onChange={(e) => setUrl(e.target.value)} />
          <span className="set-hint">The server's MCP address, for the streamable HTTP transport. It must start with https://, unless it is your own server.</span>
        </label>
        <label className="set-check set-wide">
          <input type="checkbox" checked={local} disabled={busy} onChange={(e) => setLocal(e.target.checked)} />
          This is my own server, on this machine or my home network
        </label>
        <label className="field">
          <span className="field-label">Token</span>
          <input className="input" type="password" value={token} disabled={busy} autoComplete="off" spellCheck={false} onChange={(e) => setToken(e.target.value)} />
          <span className="set-hint">If the server asks for one. It is kept with your keys and sent to this server only.</span>
        </label>
        <label className="set-check">
          <input type="checkbox" checked={outside} disabled={busy} onChange={(e) => setOutside(e.target.checked)} />
          Results are outside content
        </label>
      </div>
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      <div>
        <button type="submit" className="button button-primary" disabled={busy || name.trim() === "" || url.trim() === ""}>
          {busy ? "Adding and reaching it" : "Add the connector"}
        </button>
      </div>
    </form>
  );
}

export function ConnectorSettings({ onSignedOut }: { onSignedOut: () => void }) {
  const [state, setState] = useState<{ kind: "loading" } | { kind: "failed" } | { kind: "ready"; connectors: Connector[]; max: number }>({ kind: "loading" });
  const [turn, setTurn] = useState(0);

  useEffect(() => {
    let live = true;
    listConnectors().then(
      (got) => live && setState({ kind: "ready", ...got }),
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

  const put = (c: Connector) => setState((s) => (s.kind === "ready" ? { ...s, connectors: s.connectors.map((x) => (x.name === c.name ? c : x)) } : s));

  return (
    <>
      <section className="set-section">
        <div className="set-section-head">
          <h2 className="set-heading">Connectors</h2>
          <p className="set-note">
            Connectors give your chats tools from other places: MCP servers on this machine or on the internet. A chat sees each tool by its name until it needs one, and you can switch a connector off for one chat
            in the tools menu beside the model picker. What a connector returns is outside content unless you say otherwise, so after reading it a tool that changes something asks you first.
          </p>
        </div>
        {state.kind === "loading" && (
          <p className="quiet" aria-busy="true">
            Loading.
          </p>
        )}
        {state.kind === "failed" && (
          <div className="set-stack">
            <p className="quiet">Your connectors could not be read. Check that the server is running.</p>
            <div>
              <button type="button" className="button" onClick={() => setTurn((n) => n + 1)}>
                <Icon name="retry" size={16} />
                Try again
              </button>
            </div>
          </div>
        )}
        {state.kind === "ready" && (
          <>
            <div className="set-card">
              {state.connectors.length === 0 && <p className="quiet set-empty">No connectors yet. Add a remote one below.</p>}
              {state.connectors.map((c) => (
                <ConnectorRow
                  key={c.name}
                  c={c}
                  onChanged={put}
                  onSignedOut={onSignedOut}
                  onRemoved={() => setState((s) => (s.kind === "ready" ? { ...s, connectors: s.connectors.filter((x) => x.name !== c.name) } : s))}
                />
              ))}
            </div>
            <p className="set-hint">
              You have {state.connectors.length} of the {state.max} connectors a person may have.
            </p>
            {state.connectors.length < state.max && <AddForm onSignedOut={onSignedOut} onAdded={(c) => setState((s) => (s.kind === "ready" ? { ...s, connectors: [...s.connectors, c].sort((a, b) => a.name.localeCompare(b.name)) } : s))} />}
          </>
        )}
      </section>
      <section className="set-section">
        <div className="set-section-head">
          <h2 className="set-heading">A program on this machine</h2>
          <p className="set-note">
            A connector that runs a program on this machine can read everything Keystone can, so it is added from the command line by whoever runs Keystone, never from the browser. Store its token first, then
            add it from a file that names the program:
          </p>
        </div>
        <div className="set-card set-stack">
          <code className="con-command">{"go run ./tools/tenant secret set <your-id> connector.files\ngo run ./tools/tenant connector add <your-id> files.json"}</code>
          <p className="set-hint">Once added it is listed above, where you can switch it, check it and remove it. Its program and arguments can be changed from the command line only.</p>
        </div>
      </section>
    </>
  );
}
