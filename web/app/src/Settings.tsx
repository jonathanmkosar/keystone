import { useState, type FormEvent } from "react";
import { Navigate, NavLink, Route, Routes } from "react-router";
import {
  addModel,
  ApiError,
  deleteSecret,
  listModels,
  listSecrets,
  readProfile,
  removeModel,
  replaceModel,
  setSecret,
  writeProfile,
  type ModelEntry,
  type ModelRegistry,
  type Person,
  type Profile,
  type RegisteredModel,
  type SecretName,
} from "./api";
import { ApprovalSettings } from "./ApprovalSettings";
import { ConnectorSettings } from "./ConnectorSettings";
import { GeneralSettings } from "./GeneralSettings";
import { Icon } from "./Icon";
import { said, Section, sentence, useLoaded, Waiting } from "./SettingsParts";
import { StyleSettings } from "./StyleSettings";
import { ThemeSwitch } from "./ThemeSwitch";
import type { ThemeChoice } from "./theme";
import { UsageSettings } from "./UsageSettings";
import { ShareSettings } from "./ShareSettings";

// The settings screens (P11-01). docs/architecture/settings-screens.md
// describes them. Every server call they make is in api.ts.

const screens: { to: string; label: string }[] = [
  { to: "profile", label: "Profile" },
  { to: "appearance", label: "Appearance" },
  { to: "models", label: "Models and keys" },
  { to: "general", label: "General" },
  { to: "connectors", label: "Connectors" },
  { to: "approvals", label: "Approvals" },
  { to: "styles", label: "Styles" },
  { to: "usage", label: "Usage" },
  { to: "operations", label: "Operations" }, // the page at /operations (P11-03)
  { to: "sharing", label: "Shared links" },
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
        <Route path="general" element={<GeneralSettings onSignedOut={props.onSignedOut} onThemeKept={props.onThemeKept} />} />
        <Route path="connectors" element={<ConnectorSettings onSignedOut={props.onSignedOut} />} />
        <Route path="connectors/google" element={<ConnectorSettings onSignedOut={props.onSignedOut} />} />
        <Route path="styles" element={<StyleSettings onSignedOut={props.onSignedOut} />} />
        <Route path="approvals" element={<ApprovalSettings onSignedOut={props.onSignedOut} />} />
        <Route path="usage" element={<UsageSettings onSignedOut={props.onSignedOut} />} />
        <Route path="sharing" element={<ShareSettings onSignedOut={props.onSignedOut} />} />
        <Route path="spend" element={<Navigate to="/settings/usage" replace />} />
        <Route path="operations" element={<Navigate to="/operations" replace />} />
        <Route path="*" element={<Navigate to="/settings/profile" replace />} />
      </Routes>
    </div>
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
