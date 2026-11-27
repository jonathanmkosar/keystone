import { Fragment, useEffect, useId, useState } from "react";
import { ApiError, listSettings, resetSetting, setSetting, type FixedRule, type Setting } from "./api";
import { Icon } from "./Icon";
import { said, Section, useLoaded, Waiting } from "./SettingsParts";
import { groupsOf, isChanged, keeps, moreOf, optionText, rowOf, type Filter } from "./settingsView";
import type { ThemeChoice } from "./theme";
import "./settings-general.css";

// General, at a glance (P11-04): every setting of the registry, each as a
// name, one short line and its value, with the long explanation, the box to
// change it in, its default and its key opened in place. It is drawn from
// GET /api/v1/settings and names no setting but the theme, which the page
// wears at once. What a row and its opened part show is worked out in
// settingsView.ts, which has the tests; this file draws it.
// docs/architecture/settings-screens.md describes the screen, and decision
// 0560 says why it is this one of the three that were tried.

/** The one setting this screen names: the page wears it at once. */
const themeKey = "appearance.theme";

type Props = {
  /** The theme the page wears now. */
  theme: ThemeChoice;
  onSignedOut: () => void;
  /** Wears a theme that has just been kept by a change here. */
  onThemeKept: (c: ThemeChoice) => void;
};

type All = { settings: Setting[]; rules: FixedRule[] };

/** One setting's change in flight: what is typed and not yet kept, and how the last try went. */
type Change = {
  draft: string | null;
  setDraft: (v: string | null) => void;
  busy: boolean;
  error: string;
  /** True for a moment after a value was kept. */
  saved: boolean;
  save: (value: string) => void;
  /** Keeps what was typed, when it differs from what is stored. */
  keep: () => void;
  reset: () => void;
};

function useChange(setting: Setting, onChanged: (s: Setting) => void, onSignedOut: () => void): Change {
  const [draft, setDraft] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (!saved) return;
    const t = setTimeout(() => setSaved(false), 2500);
    return () => clearTimeout(t);
  }, [saved]);

  async function run(change: () => Promise<Setting>) {
    if (busy) return;
    setBusy(true);
    setError("");
    setSaved(false);
    try {
      onChanged(await change());
      setDraft(null);
      setSaved(true);
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") onSignedOut();
      else setError(said(e, "The setting could not be changed."));
    } finally {
      setBusy(false);
    }
  }
  const save = (value: string) => void run(() => setSetting(setting.key, value));
  return {
    draft,
    setDraft,
    busy,
    error,
    saved,
    save,
    keep: () => {
      if (draft !== null && draft !== setting.value) save(draft);
      else setDraft(null);
    },
    reset: () => void run(() => resetSetting(setting.key)),
  };
}

/** A switch, flipped where it is. */
function Switch({ setting, change, labelledBy }: { setting: Setting; change: Change; labelledBy: string }) {
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

/**
 * The box a value is changed in, inside the opened row: a select of the
 * options, a box of several lines for a long text, or one line. A choice is
 * kept when it is made, and what is typed when the person presses Enter or
 * leaves the box. Nothing here takes the focus by itself.
 */
function Editor({ setting, change, labelledBy }: { setting: Setting; change: Change; labelledBy: string }) {
  const row = rowOf(setting);
  const shown = change.draft ?? setting.value;
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
      inputMode={setting.type === "int" ? "numeric" : setting.type === "float" ? "decimal" : undefined}
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

/** Copies a setting's key, for the admin who wants it in a command or a note. */
function CopyKey({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 2000);
    return () => clearTimeout(t);
  }, [copied]);
  return (
    <button
      type="button"
      className="sg-act"
      onClick={() => {
        void navigator.clipboard?.writeText(text).then(
          () => setCopied(true),
          () => undefined,
        );
      }}
    >
      <Icon name={copied ? "check" : "copy"} size={14} />
      <span role="status">{copied ? "Copied" : "Copy"}</span>
    </button>
  );
}

/**
 * One setting. Closed, it is a name, one short line and the value: the
 * whole row opens it, and a switch is flipped where it is. Open, it adds
 * the full explanation, the box to change the value in, the default with a
 * way back to it, and the key.
 */
function Row({ setting, onChanged, onSignedOut }: { setting: Setting; onChanged: (s: Setting) => void; onSignedOut: () => void }) {
  const change = useChange(setting, onChanged, onSignedOut);
  const [open, setOpen] = useState(false);
  const id = useId();
  const row = rowOf(setting);
  const more = moreOf(setting);
  const name = `${id}-name`;
  return (
    <div className={open ? "sg-row is-open" : "sg-row"} data-setting={row.key} data-type={setting.type} data-control={row.control}>
      <div className="sg-head">
        <span className="sg-title">
          <button type="button" className="sg-open" id={name} aria-expanded={open} aria-controls={open ? `${id}-more` : undefined} aria-describedby={`${id}-line`} onClick={() => setOpen(!open)}>
            {row.title}
          </button>
          {row.changed && <span className="sg-changed">Changed</span>}
        </span>
        <span className="sg-side">
          {row.flips ? <Switch setting={setting} change={change} labelledBy={name} /> : <span className="sg-now">{row.value}</span>}
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
        <div className="sg-more" id={`${id}-more`} role="group" aria-labelledby={name}>
          <p className="sg-about">{more.about}</p>
          {more.editable && (
            <div className="sg-edit">
              <Editor setting={setting} change={change} labelledBy={name} />
              <span className="sg-edit-notes">
                {more.hint && <span className="set-hint">{more.hint}</span>}
                <span className="set-ok" role="status">
                  {change.saved ? "Saved." : ""}
                </span>
              </span>
            </div>
          )}
          {more.text && <p className="sg-text-shown">{more.text}</p>}
          {more.who && <p className="set-hint">{more.who}</p>}
          <dl className="sg-facts">
            <div className="sg-fact">
              <dt>Default</dt>
              <dd className="set-mono">{more.default}</dd>
              {more.canReset && (
                <button type="button" className="sg-act sg-reset" disabled={change.busy} onClick={change.reset}>
                  Reset to default
                </button>
              )}
            </div>
            <div className="sg-fact">
              <dt>Key</dt>
              <dd className="set-mono">
                {/* A long key breaks after a dot or an underscore, not inside a word. */}
                {more.key.split(/(?<=[._])/).map((part, i) => (
                  <Fragment key={i}>
                    {i > 0 && <wbr />}
                    {part}
                  </Fragment>
                ))}
              </dd>
              <CopyKey text={more.key} />
            </div>
          </dl>
        </div>
      )}
    </div>
  );
}

/** Settings under their plain headings, each group a card of rows. */
function Groups({ settings, onChanged, onSignedOut }: { settings: Setting[]; onChanged: (s: Setting) => void; onSignedOut: () => void }) {
  return (
    <>
      {groupsOf(settings).map((g) => (
        <section key={g.heading} className="sg-group" aria-label={g.heading}>
          <h3 className="sg-group-name">{g.heading}</h3>
          <div className="set-card">
            {g.settings.map((s) => (
              <Row key={s.key} setting={s} onChanged={onChanged} onSignedOut={onSignedOut} />
            ))}
          </div>
        </section>
      ))}
    </>
  );
}

export function GeneralSettings({ theme, onSignedOut, onThemeKept }: Props) {
  const [loaded, reload, put] = useLoaded<All>(listSettings, onSignedOut);
  const [query, setQuery] = useState("");
  const [section, setSection] = useState("");
  const [onlyChanged, setOnlyChanged] = useState(false);
  const [installOpen, setInstallOpen] = useState(false);

  if (loaded.state !== "ready") {
    return (
      <Section title="General">
        <Waiting state={loaded.state} what="The settings" onRetry={reload} />
      </Section>
    );
  }
  const { rules, settings: read } = loaded.data;
  // The theme can be chosen in the sidebar while this screen is open, and
  // the row says what the page wears, not what was read when it opened.
  const settings = read.map((s) => (s.key === themeKey && s.value !== theme ? { ...s, value: theme, overridden: true } : s));

  function changed(next: Setting) {
    put({ rules, settings: read.map((s) => (s.key === next.key ? next : s)) });
    // The theme is a setting like any other, and the page wears it at once.
    if (next.key === themeKey && (next.value === "dark" || next.value === "light" || next.value === "system")) onThemeKept(next.value);
  }

  const mine = settings.filter((s) => s.changeable);
  const install = settings.filter((s) => !s.changeable);
  const filter: Filter = { query, section, onlyChanged };
  const filtering = query.trim() !== "" || section !== "" || onlyChanged;
  const shownMine = mine.filter((s) => keeps(s, filter));
  const shownInstall = install.filter((s) => keeps(s, filter));
  const nChanged = mine.filter(isChanged).length;
  const sections = groupsOf(settings).map((g) => g.heading);

  function clear() {
    setQuery("");
    setSection("");
    setOnlyChanged(false);
  }

  return (
    <>
      <Section title="General" note="Every setting that is yours to change. Tap one to read more and to change it. A change is kept as soon as you make it.">
        <div className="sg-tools">
          <label className="sg-search">
            <Icon name="search" size={18} />
            <input
              type="search"
              className="input"
              placeholder="Search settings"
              aria-label="Search settings"
              value={query}
              autoComplete="off"
              autoCapitalize="off"
              spellCheck={false}
              onChange={(e) => setQuery(e.target.value)}
            />
          </label>
          <select className="input sg-section" aria-label="Section" value={section} onChange={(e) => setSection(e.target.value)}>
            <option value="">All sections</option>
            {sections.map((h) => (
              <option key={h} value={h}>
                {h}
              </option>
            ))}
          </select>
          <button type="button" className={onlyChanged ? "sg-chip is-on" : "sg-chip"} aria-pressed={onlyChanged} onClick={() => setOnlyChanged(!onlyChanged)}>
            Changed <span className="sg-chip-n">{nChanged}</span>
          </button>
        </div>
        <p className="set-hint" role="status">
          {filtering ? `${shownMine.length} of ${mine.length} settings` : `${mine.length} settings`}
          {filtering && (
            <>
              {" · "}
              <button type="button" className="sg-clear" onClick={clear}>
                Show all
              </button>
            </>
          )}
        </p>
      </Section>
      <Groups settings={shownMine} onChanged={changed} onSignedOut={onSignedOut} />
      {shownMine.length === 0 && <p className="quiet">{mine.length === 0 ? "No setting is yours to change." : onlyChanged && nChanged === 0 ? "You have changed no setting." : "No setting of yours matches."}</p>}

      {!onlyChanged && (
        <Section title="Set for the whole install" note="These are the admin's to change. They are shown so you can see what this install runs with.">
          {filtering ? (
            <p className="set-hint" role="status">
              {shownInstall.length} of {install.length} settings
            </p>
          ) : (
            <div>
              <button type="button" className={installOpen ? "button sg-all is-open" : "button sg-all"} aria-expanded={installOpen} onClick={() => setInstallOpen(!installOpen)}>
                <Icon name="chevron" size={16} />
                {installOpen ? "Hide them" : `Show the ${install.length} settings`}
              </button>
            </div>
          )}
          {(filtering || installOpen) && <Groups settings={shownInstall} onChanged={changed} onSignedOut={onSignedOut} />}
        </Section>
      )}

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
