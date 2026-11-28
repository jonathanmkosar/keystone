// The admin account's settings screens (P15-03), provisional on the Admin
// draft's Settings tab (design/screens/Admin.html):
//
//   /admin/settings                 the install's settings, and the default
//                                   each person's own setting starts from
//   /admin/people                   the people, each with a link to theirs
//   /admin/people/{id}/settings     one person's tenant-admin settings, the
//                                   router's policy among them
//
// Every row is drawn from the registry by its scope, so a setting another
// lane adds (the router's policy, P14-05) appears here with no change. What
// a row shows is worked out in settingsView.ts, as on a person's General
// screen; this file draws it and sends the admin's calls.
// docs/architecture/admin-settings.md describes the screens.

import { Fragment, useEffect, useId, useState, type ReactNode } from "react";
import { ApiError } from "./api";
import {
  changeInstallSetting,
  changePersonSetting,
  listInstallSettings,
  listPeople,
  listPersonSettings,
  type AdminSetting,
  type Changed,
  type Person,
} from "./adminSettingsCalls";
import { Icon } from "./Icon";
import { Mark } from "./Mark";
import { said, Section, useLoaded, Waiting } from "./SettingsParts";
import { groupsOf, isChanged, keeps, moreOf, optionText, rowOf, type Entry, type Filter } from "./settingsView";
import "./settings-general.css";
import "./admin-settings.css";

const personPath = /^\/admin\/people\/([a-f0-9]{16})\/settings\/?$/;

/** Whether a path is one of these screens. */
export function isAdminSettingsPath(path: string): boolean {
  return path === "/admin/settings" || path === "/admin/people" || personPath.test(path);
}

/** A setting as the rows read it: every one shown here is the admin's to change. */
function entryOf(s: AdminSetting): Entry {
  return { ...s, changeable: true };
}

type Save = (key: string, value: string | null) => Promise<Changed>;

/** One setting's change in flight. */
function useChange(setting: AdminSetting, save: Save, onChanged: (s: AdminSetting) => void, onSignedOut: () => void) {
  const [draft, setDraft] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState("");

  useEffect(() => {
    if (!saved) return;
    const t = setTimeout(() => setSaved(""), 3500);
    return () => clearTimeout(t);
  }, [saved]);

  async function run(value: string | null) {
    if (busy) return;
    setBusy(true);
    setError("");
    setSaved("");
    try {
      const done = await save(setting.key, value);
      onChanged(done.setting);
      setDraft(null);
      setSaved(done.change.restart ? "Saved. It takes effect when Keystone restarts." : "Saved.");
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") onSignedOut();
      else setError(said(e, "The setting could not be changed."));
    } finally {
      setBusy(false);
    }
  }
  return {
    draft,
    setDraft,
    busy,
    error,
    saved,
    save: (v: string) => void run(v),
    keep: () => {
      if (draft !== null && draft !== setting.value) void run(draft);
      else setDraft(null);
    },
    reset: () => void run(null),
  };
}

type Change = ReturnType<typeof useChange>;

function Editor({ setting, change, labelledBy }: { setting: AdminSetting; change: Change; labelledBy: string }) {
  const row = rowOf(entryOf(setting));
  const shown = change.draft ?? setting.value;
  if (row.control === "switch") {
    const on = setting.value === "true";
    return (
      <button
        type="button"
        role="switch"
        aria-checked={on}
        aria-labelledby={labelledBy}
        className={on ? "set-switch sg-switch is-on" : "set-switch sg-switch"}
        disabled={change.busy}
        onClick={() => change.save(on ? "false" : "true")}
      >
        <span className="set-switch-knob" />
      </button>
    );
  }
  if (row.control === "select") {
    return (
      <select className="input sg-select" aria-labelledby={labelledBy} value={setting.value} disabled={change.busy} onChange={(e) => change.save(e.target.value)}>
        {setting.options.map((o) => (
          <option key={o} value={o}>
            {optionText(o)}
          </option>
        ))}
      </select>
    );
  }
  if (row.control === "long-text") {
    return (
      <textarea
        className="input set-text sg-long"
        rows={6}
        aria-labelledby={labelledBy}
        value={shown}
        disabled={change.busy}
        spellCheck={false}
        onChange={(e) => change.setDraft(e.target.value)}
        onBlur={change.keep}
      />
    );
  }
  return (
    <input
      className={row.control === "text" ? "input sg-text" : "input sg-number"}
      type="text"
      aria-labelledby={labelledBy}
      value={shown}
      placeholder={setting.default === "" ? "Empty" : undefined}
      disabled={change.busy}
      spellCheck={false}
      autoComplete="off"
      autoCapitalize="off"
      onChange={(e) => change.setDraft(e.target.value)}
      onBlur={change.keep}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          change.keep();
        }
      }}
    />
  );
}

/** One setting: its name, short line and value; opened, the explanation, the box, the default and the key. */
function Row({ setting, save, onChanged, onSignedOut }: { setting: AdminSetting; save: Save; onChanged: (s: AdminSetting) => void; onSignedOut: () => void }) {
  const change = useChange(setting, save, onChanged, onSignedOut);
  const [open, setOpen] = useState(false);
  const id = useId();
  const entry = entryOf(setting);
  const row = rowOf(entry);
  const more = moreOf(entry);
  const name = `${id}-name`;
  return (
    <div className={open ? "sg-row is-open" : "sg-row"} data-setting={row.key} data-scope={setting.scope}>
      <div className="sg-head">
        <span className="sg-title">
          <button type="button" className="sg-open" id={name} aria-expanded={open} aria-describedby={`${id}-line`} onClick={() => setOpen(!open)}>
            {row.title}
          </button>
          {row.changed && <span className="sg-changed">Changed</span>}
          {setting.pending ? <span className="as-pill as-wait">Waits for a restart</span> : setting.restart && <span className="as-pill">After a restart</span>}
        </span>
        <span className="sg-side">
          {row.flips ? <Editor setting={setting} change={change} labelledBy={name} /> : <span className="sg-now">{row.value}</span>}
          <Icon name="chevron" size={16} />
        </span>
        <span className="sg-line" id={`${id}-line`}>
          {row.line}
        </span>
      </div>
      {change.error && (
        <p className="form-error sg-error" role="alert">
          {change.error}
        </p>
      )}
      {open && (
        <div className="sg-more" role="group" aria-labelledby={name}>
          {more.about !== "" && <p className="sg-about">{more.about}</p>}
          {more.editable && (
            <div className="sg-edit">
              <Editor setting={setting} change={change} labelledBy={name} />
              <span className="sg-edit-notes">
                {more.hint && <span className="set-hint">{more.hint}</span>}
              </span>
            </div>
          )}
          <span className="set-ok" role="status">
            {change.saved}
          </span>
          {setting.pending && setting.in_force !== undefined && (
            <p className="set-hint">
              Keystone runs with <span className="set-mono">{setting.in_force === "" ? "nothing" : setting.in_force}</span> until it restarts.
            </p>
          )}
          <dl className="sg-facts">
            <div className="sg-fact">
              <dt>Default</dt>
              <dd className="set-mono">{more.default}</dd>
              {isChanged(entry) && (
                <button type="button" className="sg-act sg-reset" disabled={change.busy} onClick={change.reset}>
                  Reset to default
                </button>
              )}
            </div>
            <div className="sg-fact">
              <dt>Key</dt>
              <dd className="set-mono">
                {more.key.split(/(?<=[._])/).map((part, i) => (
                  <Fragment key={i}>
                    {i > 0 && <wbr />}
                    {part}
                  </Fragment>
                ))}
              </dd>
            </div>
          </dl>
        </div>
      )}
    </div>
  );
}

/** Settings under their headings, with a search, a section and only what was changed. */
function Rows({ settings, save, onChanged, onSignedOut, empty }: { settings: AdminSetting[]; save: Save; onChanged: (s: AdminSetting) => void; onSignedOut: () => void; empty: string }) {
  const [query, setQuery] = useState("");
  const [section, setSection] = useState("");
  const [onlyChanged, setOnlyChanged] = useState(false);
  const filter: Filter = { query, section, onlyChanged };
  const shown = settings.filter((s) => keeps(entryOf(s), filter));
  const nChanged = settings.filter((s) => isChanged(entryOf(s))).length;
  return (
    <>
      <div className="sg-tools">
        <label className="sg-search">
          <Icon name="search" size={18} />
          <input type="search" className="input" placeholder="Search settings" aria-label="Search settings" value={query} autoComplete="off" spellCheck={false} onChange={(e) => setQuery(e.target.value)} />
        </label>
        <select className="input sg-section" aria-label="Section" value={section} onChange={(e) => setSection(e.target.value)}>
          <option value="">All sections</option>
          {groupsOf(settings).map((g) => (
            <option key={g.heading} value={g.heading}>
              {g.heading}
            </option>
          ))}
        </select>
        <button type="button" className={onlyChanged ? "sg-chip is-on" : "sg-chip"} aria-pressed={onlyChanged} onClick={() => setOnlyChanged(!onlyChanged)}>
          Changed <span className="sg-chip-n">{nChanged}</span>
        </button>
      </div>
      <p className="set-hint" role="status">
        {shown.length === settings.length ? `${settings.length} settings` : `${shown.length} of ${settings.length} settings`}
      </p>
      {groupsOf(shown).map((g) => (
        <section key={g.heading} className="sg-group" aria-label={g.heading}>
          <h3 className="sg-group-name">{g.heading}</h3>
          <div className="set-card">
            {g.settings.map((s) => (
              <Row key={s.key} setting={s} save={save} onChanged={onChanged} onSignedOut={onSignedOut} />
            ))}
          </div>
        </section>
      ))}
      {shown.length === 0 && <p className="quiet">{settings.length === 0 ? empty : "No setting matches."}</p>}
    </>
  );
}

/** The frame of the admin's settings screens: the mark, the sections and the page. */
function Frame({ here, children }: { here: "settings" | "people"; children: ReactNode }) {
  return (
    <div className="as-page">
      <header className="as-head">
        <div className="brand">
          <Mark size={28} />
          <span className="wordmark">Keystone</span>
        </div>
        <div>
          <h1 className="as-title">Admin</h1>
          <p className="quiet as-note">This install and the people on it. The admin sees settings and numbers, never another person's chats, files, memory or keys.</p>
        </div>
        <nav className="set-tabs" aria-label="Admin sections">
          <a className="set-tab" href="/admin">
            Overview
          </a>
          <a className={here === "people" ? "set-tab active" : "set-tab"} aria-current={here === "people" ? "page" : undefined} href="/admin/people">
            People
          </a>
          <a className={here === "settings" ? "set-tab active" : "set-tab"} aria-current={here === "settings" ? "page" : undefined} href="/admin/settings">
            Settings
          </a>
        </nav>
      </header>
      <main className="settings as-main">{children}</main>
    </div>
  );
}

/** /admin/settings: the install's settings and each person's starting defaults. */
function InstallSettings({ onSignedOut }: { onSignedOut: () => void }) {
  const [loaded, reload, put] = useLoaded<AdminSetting[]>(listInstallSettings, onSignedOut);
  const [part, setPart] = useState<"install" | "tenant">("install");
  if (loaded.state !== "ready") {
    return (
      <Section title="Settings">
        <Waiting state={loaded.state} what="The install's settings" onRetry={reload} />
      </Section>
    );
  }
  const all = loaded.data;
  const changed = (next: AdminSetting) => put(all.map((s) => (s.key === next.key ? next : s)));
  const install = all.filter((s) => s.scope === "install");
  const defaults = all.filter((s) => s.scope === "tenant");
  const waiting = install.filter((s) => s.pending).length;
  return (
    <>
      <Section
        title="Settings"
        note={
          part === "install"
            ? "One value for the whole install, kept in keystone.json. A change takes effect at once, except a setting marked After a restart, which takes effect when Keystone next starts."
            : "The value each person's own setting starts with. A new person starts with it, and a person who has not chosen follows it. A person's own choice stays theirs: the admin cannot change it."
        }
      >
        <div className="as-parts" role="tablist" aria-label="Which settings">
          <button type="button" role="tab" aria-selected={part === "install"} className={part === "install" ? "sg-chip is-on" : "sg-chip"} onClick={() => setPart("install")}>
            The whole install <span className="sg-chip-n">{install.length}</span>
          </button>
          <button type="button" role="tab" aria-selected={part === "tenant"} className={part === "tenant" ? "sg-chip is-on" : "sg-chip"} onClick={() => setPart("tenant")}>
            Each person starts with <span className="sg-chip-n">{defaults.length}</span>
          </button>
        </div>
        {part === "install" && waiting > 0 && (
          <p className="as-banner" role="status">
            {waiting === 1 ? "One change waits" : `${waiting} changes wait`} for Keystone to restart.
          </p>
        )}
      </Section>
      <Rows key={part} settings={part === "install" ? install : defaults} save={changeInstallSetting} onChanged={changed} onSignedOut={onSignedOut} empty="There is no setting here." />
    </>
  );
}

/** /admin/people: the people, each with a link to their settings. */
function PeopleList({ onSignedOut }: { onSignedOut: () => void }) {
  const [loaded, reload] = useLoaded<Person[]>(listPeople, onSignedOut);
  return (
    <Section title="People" note="Each person's own log records that the admin read this list.">
      {loaded.state !== "ready" ? (
        <Waiting state={loaded.state} what="The people" onRetry={reload} />
      ) : loaded.data.length === 0 ? (
        <p className="quiet">There is no one on this install yet.</p>
      ) : (
        <div className="set-card">
          {loaded.data.map((p) => (
            <div key={p.id} className="set-item">
              <div className="set-item-main">
                <span className="set-plain">{p.name}</span>
              </div>
              <a className="button" href={`/admin/people/${p.id}/settings`}>
                Settings
              </a>
            </div>
          ))}
        </div>
      )}
    </Section>
  );
}

/** /admin/people/{id}/settings: one person's tenant-admin settings. */
function PersonSettings({ id, onSignedOut }: { id: string; onSignedOut: () => void }) {
  const [loaded, reload, put] = useLoaded<AdminSetting[]>(() => listPersonSettings(id), onSignedOut);
  const [name, setName] = useState("");
  useEffect(() => {
    // The name is the people list's; its read is in each person's log.
    listPeople().then(
      (people) => setName(people.find((p) => p.id === id)?.name ?? ""),
      () => undefined,
    );
  }, [id]);
  return (
    <>
      <Section
        title={name ? `Settings for ${name}` : "A person's settings"}
        note="The settings the admin sets for this person, such as the router's policy and the monthly cap. Their own settings are theirs, and are not shown. Each read and each change is in their own log, which they can see in their Settings."
      >
        <p>
          <a href="/admin/people">All people</a>
        </p>
      </Section>
      {loaded.state !== "ready" ? (
        <Waiting state={loaded.state} what="This person's settings" onRetry={reload} />
      ) : (
        <Rows
          settings={loaded.data}
          save={(key, value) => changePersonSetting(id, key, value)}
          onChanged={(next) => put(loaded.data.map((s) => (s.key === next.key ? next : s)))}
          onSignedOut={onSignedOut}
          empty="There is no setting the admin sets for each person."
        />
      )}
    </>
  );
}

/** The screen for a path, or null when the path is not one of these. */
export function AdminSettingsPage({ path, onSignedOut }: { path: string; onSignedOut: () => void }) {
  const m = personPath.exec(path);
  if (m) {
    return (
      <Frame here="people">
        <PersonSettings id={m[1]} onSignedOut={onSignedOut} />
      </Frame>
    );
  }
  if (path === "/admin/people") {
    return (
      <Frame here="people">
        <PeopleList onSignedOut={onSignedOut} />
      </Frame>
    );
  }
  return (
    <Frame here="settings">
      <InstallSettings onSignedOut={onSignedOut} />
    </Frame>
  );
}
