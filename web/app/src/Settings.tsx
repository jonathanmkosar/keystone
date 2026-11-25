import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Navigate, NavLink, Route, Routes } from "react-router";
import {
  addModel,
  ApiError,
  deleteSecret,
  listModels,
  listSecrets,
  listSettings,
  readProfile,
  removeModel,
  replaceModel,
  resetSetting,
  setSecret,
  setSetting,
  writeProfile,
  type FixedRule,
  type ModelEntry,
  type ModelRegistry,
  type Person,
  type Profile,
  type RegisteredModel,
  type SecretName,
  type Setting,
} from "./api";
import { Icon } from "./Icon";
import { ThemeSwitch } from "./ThemeSwitch";
import type { ThemeChoice } from "./theme";

// The settings screens (P11-01). docs/architecture/settings-screens.md
// describes them. Every server call they make is in api.ts.

const screens: { to: string; label: string }[] = [
  { to: "profile", label: "Profile" },
  { to: "appearance", label: "Appearance" },
  { to: "models", label: "Models and keys" },
  { to: "general", label: "General" },
  { to: "connectors", label: "Connectors" },
  { to: "approvals", label: "Approvals" },
  { to: "spend", label: "Spend" },
];

type Props = {
  person: Person;
  theme: ThemeChoice;
  /** Wears a theme and keeps it with the person's settings. */
  onTheme: (c: ThemeChoice) => void;
  /** Wears a theme that has just been kept by another call. */
  onThemeKept: (c: ThemeChoice) => void;
  /** Called when a call finds the session has ended. */
  onSignedOut: () => void;
};

/** What a refused or failed call is told to the person as. */
function said(e: unknown, fallback: string): string {
  if (e instanceof ApiError) {
    if (e.said && e.status >= 400 && e.status < 500 && e.status !== 404) return sentence(e.said);
    if (e.kind === "not-ready") return "The server did not answer. Check that it is running.";
  }
  return fallback;
}

/** The server's refusals start in lower case. On the screen they are sentences. */
function sentence(text: string): string {
  const t = text.trim();
  if (t === "") return t;
  const first = t[0].toUpperCase() + t.slice(1);
  return /[.!?]$/.test(first) ? first : first + ".";
}

type Loaded<T> = { state: "loading" } | { state: "failed" } | { state: "ready"; data: T };

/** Reads something from the server when the screen opens, and again on reload. */
function useLoaded<T>(read: () => Promise<T>, onSignedOut: () => void): [Loaded<T>, () => void, (data: T) => void] {
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

function Waiting({ state, what, onRetry }: { state: "loading" | "failed"; what: string; onRetry: () => void }) {
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

function Section({ title, note, children }: { title: string; note?: ReactNode; children: ReactNode }) {
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

/** The settings screens, under /settings. */
export function Settings(props: Props) {
  return (
    <div className="settings">
      <h1 className="page-title">Settings</h1>
      <nav className="set-tabs" aria-label="Settings">
        {screens.map((s) => (
          <NavLink key={s.to} to={`/settings/${s.to}`} className="set-tab">
            {s.label}
          </NavLink>
        ))}
      </nav>
      <Routes>
        <Route index element={<Navigate to="profile" replace />} />
        <Route path="profile" element={<ProfileScreen {...props} />} />
        <Route path="appearance" element={<Appearance {...props} />} />
        <Route path="models" element={<ModelsAndKeys {...props} />} />
        <Route path="general" element={<General {...props} />} />
        <Route
          path="connectors"
          element={
            <NotBuilt title="Connectors" brings="P7-04, connectors, and then P12-02, the Google connectors">
              Connectors let Keystone reach your mail, calendar and files. A connector's token will be kept with your keys, under Models and keys.
            </NotBuilt>
          }
        />
        <Route
          path="approvals"
          element={
            <NotBuilt title="Approvals" brings="P7-02, approvals">
              Approvals decide when Keystone must ask before it does something outside itself. Until they are built, Keystone has no tool that acts outside itself.
            </NotBuilt>
          }
        />
        <Route
          path="spend"
          element={
            <NotBuilt title="Spend" brings="P11-02, usage and cost, with the monthly cap from P8-05">
              Spend will show what your hosted models have cost and let you set a cap. Each model call's tokens and cost are already recorded.
            </NotBuilt>
          }
        />
        <Route path="*" element={<Navigate to="/settings/profile" replace />} />
      </Routes>
    </div>
  );
}

function NotBuilt({ title, brings, children }: { title: string; brings: string; children: ReactNode }) {
  return (
    <Section title={title}>
      <div className="set-card set-stack">
        <p className="set-plain">Not built yet.</p>
        <p className="quiet">{children}</p>
        <p className="quiet">This screen comes with {brings}.</p>
      </div>
    </Section>
  );
}

// Profile

function ProfileScreen({ person, onSignedOut }: Props) {
  const [loaded, reload, put] = useLoaded<Profile>(readProfile, onSignedOut);
  const [text, setText] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  if (loaded.state !== "ready") {
    return (
      <Section title="Profile">
        <Waiting state={loaded.state} what="Your profile" onRetry={reload} />
      </Section>
    );
  }
  const profile = loaded.data;
  const draft = text ?? profile.text;
  const count = [...draft].length;
  const over = count > profile.max_chars;

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setNote(null);
    try {
      const kept = await writeProfile(draft);
      put(kept);
      setText(null);
      setNote({ kind: "ok", text: kept.text === "" ? "Cleared." : "Saved." });
    } catch (err) {
      if (err instanceof ApiError && err.kind === "signed-out") onSignedOut();
      else setNote({ kind: "err", text: said(err, "The profile could not be saved.") });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Section
      title="Profile"
      note="A few facts and preferences, in your own words. Keystone puts this text at the start of every message to a model, so a model knows it without being told again."
    >
      <form className="set-card set-stack" onSubmit={save}>
        <div className="field">
          <span className="field-label">Name</span>
          <span className="set-plain">{person.name}</span>
          <span className="set-hint">Your name was set when you were added to this install. It cannot be changed here yet.</span>
        </div>
        <label className="field">
          <span className="field-label">About you</span>
          <textarea
            className="input set-text"
            rows={8}
            value={draft}
            disabled={busy}
            placeholder="What you do, how you like answers written, anything you would otherwise repeat."
            onChange={(e) => {
              setText(e.target.value);
              setNote(null);
            }}
          />
        </label>
        <p className="set-hint">
          Your profile goes to every model you use, hosted ones too. Leave out anything that belongs in a sensitive space.
        </p>
        <div className="set-row">
          <button type="submit" className="button button-primary" disabled={busy || over || draft === profile.text}>
            Save
          </button>
          <span className={over ? "set-count is-over" : "set-count"}>
            {count} of {profile.max_chars} characters
          </span>
          {note && (
            <span className={note.kind === "ok" ? "set-ok" : "form-error"} role="status">
              {note.text}
            </span>
          )}
        </div>
      </form>
    </Section>
  );
}

// Appearance

function Appearance({ theme, onTheme }: Props) {
  return (
    <Section title="Appearance" note="Dark is copper and ivory on basalt. Light is copper on limestone. System follows your device.">
      <div className="set-card set-stack">
        <div className="field">
          <span className="field-label">Theme</span>
          <div className="set-theme">
            <ThemeSwitch choice={theme} onChoose={onTheme} />
          </div>
          <span className="set-hint">The choice is kept with your settings, so it follows you to another browser when you sign in there.</span>
        </div>
      </div>
    </Section>
  );
}

// Models and keys

type Keys = { secrets: SecretName[]; registry: ModelRegistry };

async function readKeys(): Promise<Keys> {
  const [secrets, registry] = await Promise.all([listSecrets(), listModels()]);
  return { secrets, registry };
}

/** A button that asks once more before it acts. */
function Remove({ label, onRemove, disabled }: { label: string; onRemove: () => void; disabled?: boolean }) {
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
        {label}
      </button>
      <button type="button" className="button set-small" onClick={() => setAsking(false)}>
        Keep
      </button>
    </span>
  );
}

function ModelsAndKeys({ onSignedOut }: Props) {
  const [loaded, reload] = useLoaded<Keys>(readKeys, onSignedOut);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<RegisteredModel | "new" | null>(null);

  if (loaded.state !== "ready") {
    return (
      <Section title="Models and keys">
        <Waiting state={loaded.state} what="Your models and keys" onRetry={reload} />
      </Section>
    );
  }
  const { secrets, registry } = loaded.data;

  // act runs one change and reads everything again, because a key and the
  // models that name it are shown together.
  async function act(change: () => Promise<void>, fallback: string): Promise<boolean> {
    setError("");
    try {
      await change();
      reload();
      return true;
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") onSignedOut();
      else setError(said(e, fallback));
      return false;
    }
  }

  return (
    <>
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      <Section
        title="Keys"
        note="A provider's key, kept under a name. It is stored encrypted on this machine and Keystone never shows it again. A model names the key it uses."
      >
        <div className="set-card">
          {secrets.length === 0 && <p className="quiet set-empty">No keys are stored.</p>}
          {secrets.map((s) => (
            <div key={s.name} className="set-item">
              <div className="set-item-main">
                <span className="set-mono">{s.name}</span>
                <span className="set-hint">{s.used_by.length > 0 ? `Used by ${s.used_by.join(", ")}` : "No model uses it"}</span>
              </div>
              <Remove label="Remove this key" onRemove={() => void act(() => deleteSecret(s.name), "The key could not be removed.")} />
            </div>
          ))}
        </div>
        <KeyForm onSave={(name, value) => act(() => setSecret(name, value), "The key could not be stored.")} />
      </Section>

      <Section
        title="Models"
        note={`The models you can pick for a chat. You have ${registry.models.length} of the ${registry.max} a person may hold.`}
      >
        <div className="set-card">
          {registry.models.length === 0 && <p className="quiet set-empty">No models are set up. Add one below, and store its key above.</p>}
          {registry.models.map((m) => (
            <ModelRow
              key={m.entry.name}
              model={m}
              onEdit={() => setEditing(m)}
              onRemove={() => void act(() => removeModel(m.entry.name), "The model could not be removed.")}
            />
          ))}
        </div>
        {editing === null ? (
          <div>
            <button type="button" className="button" onClick={() => setEditing("new")}>
              <Icon name="plus" size={16} />
              Add a model
            </button>
          </div>
        ) : (
          <ModelForm
            key={editing === "new" ? "new" : editing.entry.name}
            registry={registry}
            secrets={secrets}
            editing={editing === "new" ? null : editing}
            onCancel={() => setEditing(null)}
            onSave={async (entry, isNew) => {
              const done = await act(() => (isNew ? addModel(entry) : replaceModel(entry)), "The model could not be saved.");
              if (done) setEditing(null);
            }}
          />
        )}
      </Section>
    </>
  );
}

function KeyForm({ onSave }: { onSave: (name: string, value: string) => Promise<boolean> }) {
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    const done = await onSave(name.trim(), value);
    setBusy(false);
    // The value is dropped from the page whether or not it was stored.
    setValue("");
    if (done) setName("");
  }

  return (
    <form className="set-card set-stack" onSubmit={save}>
      <h3 className="set-subheading">Add a key</h3>
      <div className="set-grid">
        <label className="field">
          <span className="field-label">Name</span>
          <input
            className="input set-mono"
            value={name}
            disabled={busy}
            placeholder="provider.example"
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setName(e.target.value)}
          />
          <span className="set-hint">Lower-case letters, digits, dots, dashes and underscores. Storing under a name that is taken replaces the key.</span>
        </label>
        <label className="field">
          <span className="field-label">Key</span>
          <input
            className="input"
            type="password"
            value={value}
            disabled={busy}
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setValue(e.target.value)}
          />
          <span className="set-hint">Paste it here. It is sent once and cannot be read back.</span>
        </label>
      </div>
      <div>
        <button type="submit" className="button button-primary" disabled={busy || name.trim() === "" || value === ""}>
          Store the key
        </button>
      </div>
    </form>
  );
}

function ModelRow({ model, onEdit, onRemove }: { model: RegisteredModel; onEdit: () => void; onRemove: () => void }) {
  const e = model.entry;
  if (model.problem) {
    return (
      <div className="set-item">
        <div className="set-item-main">
          <span className="set-mono">{e.name}</span>
          <span className="form-error">{sentence(model.problem)}</span>
        </div>
        <Remove label="Remove this model" onRemove={onRemove} />
      </div>
    );
  }
  const facts = [e.adapter, e.provider_model, `${e.context_window.toLocaleString()} tokens`].filter(Boolean).join(" · ");
  return (
    <div className="set-item">
      <div className="set-item-main">
        <span className="set-item-title">
          {e.label} <span className="set-mono set-quiet">{e.name}</span>
        </span>
        <span className="set-hint">{facts}</span>
        <span className="set-pills">
          <span className={model.may_read_sensitive ? "set-pill is-ok" : "set-pill"}>{e.class}</span>
          {e.key ? (
            <span className={model.key_present ? "set-pill is-ok" : "set-pill is-bad"}>
              key {e.key} {model.key_present ? "stored" : "missing"}
            </span>
          ) : (
            <span className="set-pill">no key</span>
          )}
        </span>
      </div>
      <span className="set-row">
        <button type="button" className="button set-small" onClick={onEdit}>
          <Icon name="pencil" size={16} />
          Edit
        </button>
        <Remove label="Remove this model" onRemove={onRemove} />
      </span>
    </div>
  );
}

const classNotes: Record<string, string> = {
  local: "Runs on this machine or the home network. It may read content from sensitive spaces.",
  hosted: "Someone else runs it. It never receives content from a sensitive space.",
  trusted: "Runs somewhere else, and you choose to trust it with content from sensitive spaces. Choose this with care.",
};

function ModelForm({
  registry,
  secrets,
  editing,
  onCancel,
  onSave,
}: {
  registry: ModelRegistry;
  secrets: SecretName[];
  editing: RegisteredModel | null;
  onCancel: () => void;
  onSave: (entry: ModelEntry, isNew: boolean) => Promise<void>;
}) {
  // An entry that is refused today has only its name, so it starts blank.
  const from = editing && !editing.problem ? editing.entry : null;
  const [name, setName] = useState(editing?.entry.name ?? "");
  const [label, setLabel] = useState(from?.label ?? "");
  const [adapter, setAdapter] = useState(from?.adapter ?? registry.adapters.find((a) => !a.in_process)?.name ?? "");
  const [endpoint, setEndpoint] = useState(from?.endpoint ?? "");
  const [providerModel, setProviderModel] = useState(from?.provider_model ?? "");
  const [cls, setCls] = useState(from?.class ?? "hosted");
  const [contextWindow, setContextWindow] = useState(from ? String(from.context_window) : "");
  const [outputLimit, setOutputLimit] = useState(from ? String(from.output_limit) : "");
  const [key, setKey] = useState(from?.key ?? "");
  const [caps, setCaps] = useState<string[]>(from?.capabilities ?? []);
  const [priceIn, setPriceIn] = useState(from ? String(from.price.input) : "0");
  const [priceOut, setPriceOut] = useState(from ? String(from.price.output) : "0");
  const [busy, setBusy] = useState(false);

  const inProcess = registry.adapters.find((a) => a.name === adapter)?.in_process ?? false;
  const speaks = registry.adapters.find((a) => a.name === adapter)?.speaks ?? "";

  async function save(e: FormEvent) {
    e.preventDefault();
    const entry: ModelEntry = {
      // Fields the form does not show, such as a certificate, are kept.
      ...(from ?? {}),
      name: name.trim(),
      label: label.trim(),
      adapter,
      class: inProcess ? "local" : cls,
      context_window: Number(contextWindow) || 0,
      output_limit: Number(outputLimit) || 0,
      capabilities: registry.capabilities.filter((c) => caps.includes(c)),
      price: { ...(from?.price ?? {}), input: Number(priceIn) || 0, output: Number(priceOut) || 0 },
      endpoint: inProcess ? undefined : endpoint.trim() || undefined,
      provider_model: inProcess ? undefined : providerModel.trim() || undefined,
      key: inProcess ? undefined : key.trim() || undefined,
    };
    if (inProcess) delete entry.ca_cert;
    setBusy(true);
    await onSave(entry, editing === null);
    setBusy(false);
  }

  return (
    <form className="set-card set-stack" onSubmit={save}>
      <h3 className="set-subheading">{editing ? `Edit ${editing.entry.name}` : "Add a model"}</h3>
      <div className="set-grid">
        <label className="field">
          <span className="field-label">Name</span>
          <input
            className="input set-mono"
            value={name}
            disabled={busy || editing !== null}
            placeholder="example-large"
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setName(e.target.value)}
          />
          <span className="set-hint">
            {editing ? "A model is renamed by removing it and adding it again." : "Keystone's own name for it. Lower-case letters, digits, dots, dashes and underscores."}
          </span>
        </label>
        <label className="field">
          <span className="field-label">Label</span>
          <input className="input" value={label} disabled={busy} placeholder="Example Large" onChange={(e) => setLabel(e.target.value)} />
          <span className="set-hint">What the model picker shows.</span>
        </label>
        <label className="field">
          <span className="field-label">Adapter</span>
          <select className="input" value={adapter} disabled={busy} onChange={(e) => setAdapter(e.target.value)}>
            {registry.adapters.map((a) => (
              <option key={a.name} value={a.name}>
                {a.name}
              </option>
            ))}
          </select>
          <span className="set-hint">{speaks ? `Speaks ${speaks}.` : ""}</span>
        </label>
        <label className="field">
          <span className="field-label">Class</span>
          <select className="input" value={inProcess ? "local" : cls} disabled={busy || inProcess} onChange={(e) => setCls(e.target.value)}>
            {registry.classes.map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </select>
          <span className="set-hint">{classNotes[inProcess ? "local" : cls] ?? ""}</span>
        </label>
        {!inProcess && (
          <>
            <label className="field set-wide">
              <span className="field-label">Endpoint</span>
              <input
                className="input set-mono"
                value={endpoint}
                disabled={busy}
                placeholder="https://api.provider-one.example"
                autoComplete="off"
                spellCheck={false}
                onChange={(e) => setEndpoint(e.target.value)}
              />
              <span className="set-hint">The base address the adapter calls. Never put a key in it.</span>
            </label>
            <label className="field">
              <span className="field-label">Model name</span>
              <input
                className="input set-mono"
                value={providerModel}
                disabled={busy}
                placeholder="example-large-1"
                autoComplete="off"
                spellCheck={false}
                onChange={(e) => setProviderModel(e.target.value)}
              />
              <span className="set-hint">The provider's own name for the model.</span>
            </label>
            <label className="field">
              <span className="field-label">Key name</span>
              <input
                className="input set-mono"
                value={key}
                disabled={busy}
                list="set-key-names"
                placeholder="provider.example"
                autoComplete="off"
                spellCheck={false}
                onChange={(e) => setKey(e.target.value)}
              />
              <datalist id="set-key-names">
                {secrets.map((s) => (
                  <option key={s.name} value={s.name} />
                ))}
              </datalist>
              <span className="set-hint">The name of a stored key, never the key itself. Leave it empty when the endpoint asks for none.</span>
            </label>
          </>
        )}
        <label className="field">
          <span className="field-label">Context window</span>
          <input className="input" type="number" min={1} step={1} value={contextWindow} disabled={busy} placeholder="200000" onChange={(e) => setContextWindow(e.target.value)} />
          <span className="set-hint">The most tokens one call may hold, input and output together.</span>
        </label>
        <label className="field">
          <span className="field-label">Output limit</span>
          <input className="input" type="number" min={1} step={1} value={outputLimit} disabled={busy} placeholder="16000" onChange={(e) => setOutputLimit(e.target.value)} />
          <span className="set-hint">The most tokens the model writes in one reply.</span>
        </label>
        <label className="field">
          <span className="field-label">Price in, dollars for a million tokens</span>
          <input className="input" type="number" min={0} step="any" value={priceIn} disabled={busy} onChange={(e) => setPriceIn(e.target.value)} />
        </label>
        <label className="field">
          <span className="field-label">Price out, dollars for a million tokens</span>
          <input className="input" type="number" min={0} step="any" value={priceOut} disabled={busy} onChange={(e) => setPriceOut(e.target.value)} />
        </label>
        <fieldset className="set-fieldset set-wide">
          <legend className="field-label">What it can do</legend>
          <div className="set-checks">
            {registry.capabilities.map((c) => (
              <label key={c} className="set-check">
                <input
                  type="checkbox"
                  checked={caps.includes(c)}
                  disabled={busy}
                  onChange={(e) => setCaps(e.target.checked ? [...caps, c] : caps.filter((x) => x !== c))}
                />
                {c}
              </label>
            ))}
          </div>
        </fieldset>
      </div>
      <div className="set-row">
        <button type="submit" className="button button-primary" disabled={busy || name.trim() === "" || label.trim() === ""}>
          {editing ? "Save the model" : "Add the model"}
        </button>
        <button type="button" className="button" disabled={busy} onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}

// General: one control for every setting the person may change, drawn from
// what GET /api/v1/settings returns. Nothing below names a setting.

type All = { settings: Setting[]; rules: FixedRule[] };

/** "chats.search_max_results" reads as "Search max results". */
function titleOfKey(key: string): string {
  const words = key.slice(key.indexOf(".") + 1).replace(/[._]/g, " ");
  return words.charAt(0).toUpperCase() + words.slice(1);
}

function titleOfLane(lane: string): string {
  return lane.charAt(0).toUpperCase() + lane.slice(1);
}

function General({ onSignedOut, onThemeKept }: Props) {
  const [loaded, reload, put] = useLoaded<All>(listSettings, onSignedOut);

  if (loaded.state !== "ready") {
    return (
      <Section title="General">
        <Waiting state={loaded.state} what="The settings" onRetry={reload} />
      </Section>
    );
  }
  const { settings, rules } = loaded.data;
  const mine = settings.filter((s) => s.changeable);
  const fixed = settings.filter((s) => !s.changeable);
  const lanes = [...new Set(mine.map((s) => s.lane))].sort();

  function changed(next: Setting) {
    put({ rules, settings: settings.map((s) => (s.key === next.key ? next : s)) });
    // The theme is a setting like any other, and the page wears it at once.
    if (next.key === "appearance.theme" && (next.value === "dark" || next.value === "light" || next.value === "system")) onThemeKept(next.value);
  }

  return (
    <>
      <Section title="General" note="Every setting that is yours to change. A change is kept as soon as you make it, and each one can go back to its default.">
        {mine.length === 0 && <p className="quiet">No setting is yours to change.</p>}
      </Section>
      {lanes.map((lane) => (
        <Section key={lane} title={titleOfLane(lane)}>
          <div className="set-card">
            {mine
              .filter((s) => s.lane === lane)
              .map((s) => (
                <SettingRow key={s.key} setting={s} onChanged={changed} onSignedOut={onSignedOut} />
              ))}
          </div>
        </Section>
      ))}
      <Section title="Set for the whole install" note="These are the admin's to change, so they are shown and cannot be changed here.">
        <details className="set-card set-details">
          <summary className="set-summary">{fixed.length} settings</summary>
          {fixed.map((s) => (
            <div key={s.key} className="set-item">
              <div className="set-item-main">
                <span className="set-item-title">
                  {titleOfKey(s.key)} <span className="set-mono set-quiet">{s.key}</span>
                </span>
                <span className="set-hint">{s.description}</span>
              </div>
              <span className="set-mono set-value">{s.value === "" ? "(empty)" : s.value}</span>
            </div>
          ))}
        </details>
      </Section>
      <Section title="Fixed rules" note="Four rules that are not settings. Nothing on this screen, and nothing an admin can do, switches one off.">
        <div className="set-card">
          {rules.map((r) => (
            <div key={r.key} className="set-item">
              <div className="set-item-main">
                <span className="set-plain">{r.rule}</span>
              </div>
              <span className="set-pill is-ok">Always on</span>
            </div>
          ))}
        </div>
      </Section>
    </>
  );
}

const typeHints: Record<string, string> = {
  int: "A whole number.",
  float: "A number.",
  duration: "A length of time, such as 30s, 5m or 1h.",
};

/** One setting: its name, what it does, and the control its type calls for. */
function SettingRow({ setting, onChanged, onSignedOut }: { setting: Setting; onChanged: (s: Setting) => void; onSignedOut: () => void }) {
  const [draft, setDraft] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const shown = draft ?? setting.value;
  const id = `set-${setting.key}`;

  async function run(change: () => Promise<Setting>) {
    setBusy(true);
    setError("");
    try {
      onChanged(await change());
      setDraft(null);
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") onSignedOut();
      else setError(said(e, "The setting could not be changed."));
    } finally {
      setBusy(false);
    }
  }
  const save = (value: string) => void run(() => setSetting(setting.key, value));
  // A field is kept when the person leaves it or presses Enter.
  const keep = () => {
    if (draft !== null && draft !== setting.value) save(draft);
    else setDraft(null);
  };

  let control: ReactNode;
  if (setting.type === "bool") {
    const on = setting.value === "true";
    control = (
      <button
        type="button"
        id={id}
        role="switch"
        aria-checked={on}
        className={on ? "set-switch is-on" : "set-switch"}
        disabled={busy}
        onClick={() => save(on ? "false" : "true")}
      >
        <span className="set-switch-knob" />
        <span className="set-switch-text">{on ? "On" : "Off"}</span>
      </button>
    );
  } else if (setting.options.length > 0) {
    control = (
      <select id={id} className="input" value={setting.value} disabled={busy} onChange={(e) => save(e.target.value)}>
        {setting.options.map((o) => (
          <option key={o} value={o}>
            {o}
          </option>
        ))}
      </select>
    );
  } else if (setting.type === "string" && (setting.default.length > 60 || setting.value.length > 60 || setting.value.includes("\n"))) {
    control = (
      <textarea id={id} className="input set-text" rows={4} value={shown} disabled={busy} onChange={(e) => setDraft(e.target.value)} onBlur={keep} />
    );
  } else {
    const number = setting.type === "int" || setting.type === "float";
    control = (
      <input
        id={id}
        className={setting.type === "string" ? "input" : "input set-short"}
        type={number ? "number" : "text"}
        step={setting.type === "int" ? 1 : setting.type === "float" ? "any" : undefined}
        value={shown}
        disabled={busy}
        spellCheck={false}
        autoComplete="off"
        onChange={(e) => setDraft(e.target.value)}
        onBlur={keep}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            keep();
          }
        }}
      />
    );
  }

  const long = setting.type === "string" && setting.options.length === 0;
  return (
    <div className={long ? "set-item set-item-long" : "set-item"} data-setting={setting.key} data-type={setting.type}>
      <div className="set-item-main">
        <label className="set-item-title" htmlFor={id}>
          {titleOfKey(setting.key)} <span className="set-mono set-quiet">{setting.key}</span>
        </label>
        <span className="set-hint">
          {setting.description} {typeHints[setting.type] ?? ""}
        </span>
        {error && (
          <span className="form-error" role="alert">
            {error}
          </span>
        )}
      </div>
      <div className="set-control">
        {control}
        {setting.overridden ? (
          <button type="button" className="set-reset" disabled={busy} onClick={() => void run(() => resetSetting(setting.key))}>
            Reset to default{long ? "" : `: ${setting.default === "" ? "empty" : setting.default}`}
          </button>
        ) : (
          <span className="set-hint">Default</span>
        )}
      </div>
    </div>
  );
}
