import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Link, useSearchParams } from "react-router";
import { ApiError, listSettings, resetSetting, setSetting, type FixedRule, type Setting } from "./api";
import { Icon } from "./Icon";
import { said, Section, useLoaded, Waiting } from "./SettingsParts";
import { controlOf, formatHint, found, groupsOf, shortLineOf, titleOf, valueText } from "./settingsView";
import type { ThemeChoice } from "./theme";
import "./settings-general.css";

// SKETCHES for P11-04, settings at a glance. Three ways to draw General,
// chosen with ?sketch=a, b or c, on the real registry:
//   a  the short line, and "More" opens the long text in place
//   b  the short line, and an info button opens the long text in a sheet
//      (phone) or a popover (desktop)
//   c  a compact list of name and value, and each setting has a view of its own
// One is chosen in a decision record and the other two are removed.

type Props = {
  onSignedOut: () => void;
  onThemeKept: (c: ThemeChoice) => void;
};

type All = { settings: Setting[]; rules: FixedRule[] };
type Sketch = "a" | "b" | "c";

/** One setting's change in flight: what is typed and not yet kept, and how the last try went. */
type Change = {
  draft: string | null;
  setDraft: (v: string | null) => void;
  busy: boolean;
  error: string;
  save: (value: string) => void;
  /** Keeps what was typed, when it differs from what is stored. */
  keep: () => void;
  reset: () => void;
};

function useChange(setting: Setting, onChanged: (s: Setting) => void, onSignedOut: () => void): Change {
  const [draft, setDraft] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

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
  return {
    draft,
    setDraft,
    busy,
    error,
    save,
    keep: () => {
      if (draft !== null && draft !== setting.value) save(draft);
      else setDraft(null);
    },
    reset: () => void run(() => resetSetting(setting.key)),
  };
}

/** The control a setting's type calls for. A long text has a box of several lines. */
function Control({ setting, change, id, labelledBy }: { setting: Setting; change: Change; id: string; labelledBy: string }) {
  const kind = controlOf(setting);
  const shown = change.draft ?? setting.value;
  if (!setting.changeable) return <span className="sg-fixed set-mono">{valueText(setting, setting.value)}</span>;
  if (kind === "switch") {
    const on = setting.value === "true";
    return (
      <button
        type="button"
        id={id}
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
  if (kind === "select") {
    return (
      <select id={id} className="input sg-select" aria-labelledby={labelledBy} value={setting.value} disabled={change.busy} onChange={(e) => change.save(e.target.value)}>
        {setting.options.map((o) => (
          <option key={o} value={o}>
            {o}
          </option>
        ))}
      </select>
    );
  }
  if (kind === "long-text") {
    return (
      <textarea
        id={id}
        className="input set-text sg-long"
        rows={5}
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
      id={id}
      className={kind === "text" ? "input sg-text-input" : "input sg-number"}
      type="text"
      inputMode={setting.type === "int" ? "numeric" : setting.type === "float" ? "decimal" : undefined}
      aria-labelledby={labelledBy}
      value={shown}
      placeholder={kind === "text" && setting.default === "" ? "Empty" : undefined}
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
      className="sg-copy"
      onClick={() => {
        void navigator.clipboard?.writeText(text).then(
          () => setCopied(true),
          () => undefined,
        );
      }}
    >
      <Icon name={copied ? "check" : "copy"} size={14} />
      <span aria-live="polite">{copied ? "Copied" : "Copy"}</span>
    </button>
  );
}

/** The long text and the facts: the description, what the default is, and the key. */
function Detail({ setting, change, editor }: { setting: Setting; change: Change; editor?: ReactNode }) {
  const hint = formatHint(setting);
  return (
    <div className="sg-detail">
      <p className="sg-about">{setting.description}</p>
      {editor}
      {hint && setting.changeable && <p className="set-hint">{hint}</p>}
      <dl className="sg-facts">
        <div className="sg-fact">
          <dt>Default</dt>
          <dd className="set-mono">{valueText(setting, setting.default, 80)}</dd>
          {setting.overridden && setting.changeable && (
            <button type="button" className="sg-reset" disabled={change.busy} onClick={change.reset}>
              Reset
            </button>
          )}
        </div>
        <div className="sg-fact">
          <dt>Key</dt>
          <dd className="set-mono">{setting.key}</dd>
          <CopyKey text={setting.key} />
        </div>
      </dl>
    </div>
  );
}

type RowProps = { setting: Setting; onChanged: (s: Setting) => void; onSignedOut: () => void };

/** The head of a row in sketches a and b: the name, the short line, and the control. */
function Head({ setting, change, id, after }: { setting: Setting; change: Change; id: string; after?: ReactNode }) {
  const kind = controlOf(setting);
  const long = kind === "long-text";
  return (
    <div className={kind === "text" || long ? "sg-head is-wide" : "sg-head"}>
      <div className="sg-words">
        <span className="sg-name">
          <span id={`${id}-name`}>{titleOf(setting)}</span>
          {setting.overridden && <span className="sg-changed">Changed</span>}
          {after}
        </span>
        <span className="sg-line">{shortLineOf(setting)}</span>
      </div>
      <div className="sg-value">
        {long ? <span className="sg-preview">{valueText(setting, change.draft ?? setting.value, 90)}</span> : <Control setting={setting} change={change} id={id} labelledBy={`${id}-name`} />}
      </div>
    </div>
  );
}

// Sketch a: "More" opens the long text in place.

function RowA({ setting, onChanged, onSignedOut }: RowProps) {
  const change = useChange(setting, onChanged, onSignedOut);
  const [open, setOpen] = useState(false);
  const id = useId();
  const long = controlOf(setting) === "long-text";
  return (
    <div className="sg-row" data-setting={setting.key} data-type={setting.type}>
      <Head setting={setting} change={change} id={id} />
      {change.error && (
        <p className="form-error" role="alert">
          {change.error}
        </p>
      )}
      <button type="button" className="sg-more" aria-expanded={open} aria-controls={`${id}-detail`} onClick={() => setOpen(!open)}>
        {open ? "Less" : long && setting.changeable ? "More, and edit" : "More"}
        <Icon name="chevron" size={14} />
      </button>
      {open && (
        <div id={`${id}-detail`}>
          <Detail setting={setting} change={change} editor={long && setting.changeable ? <Control setting={setting} change={change} id={id} labelledBy={`${id}-name`} /> : undefined} />
        </div>
      )}
    </div>
  );
}

// Sketch b: an info button opens the long text in a sheet on a phone and a
// popover on a desktop.

function RowB({ setting, onChanged, onSignedOut }: RowProps) {
  const change = useChange(setting, onChanged, onSignedOut);
  const [open, setOpen] = useState(false);
  const id = useId();
  const long = controlOf(setting) === "long-text";
  const button = useRef<HTMLButtonElement>(null);
  const wrap = useRef<HTMLDivElement>(null);
  const panel = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    // The panel takes the focus, never a text box, so no keyboard rises.
    panel.current?.focus();
    function down(e: MouseEvent) {
      if (!panel.current?.contains(e.target as Node) && !button.current?.contains(e.target as Node)) setOpen(false);
    }
    function key(e: KeyboardEvent) {
      if (e.key === "Escape") {
        setOpen(false);
        button.current?.focus();
      }
    }
    document.addEventListener("mousedown", down);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("mousedown", down);
      document.removeEventListener("keydown", key);
    };
  }, [open]);

  // On a phone the sheet is fixed to what can be seen, which the keyboard
  // shrinks and moves (attach.css does the same for the attach picker).
  useEffect(() => {
    const vv = window.visualViewport;
    const el = wrap.current;
    if (!open || !vv || !el) return;
    const place = () => {
      el.style.setProperty("--seen-top", `${vv.offsetTop}px`);
      el.style.setProperty("--seen-height", `${vv.height}px`);
    };
    place();
    vv.addEventListener("resize", place);
    vv.addEventListener("scroll", place);
    return () => {
      vv.removeEventListener("resize", place);
      vv.removeEventListener("scroll", place);
    };
  }, [open]);

  return (
    <div className="sg-row" data-setting={setting.key} data-type={setting.type}>
      <Head
        setting={setting}
        change={change}
        id={id}
        after={
          <button
            type="button"
            ref={button}
            className="sg-info"
            aria-label={`About ${titleOf(setting)}`}
            aria-haspopup="dialog"
            aria-expanded={open}
            onClick={() => setOpen(!open)}
          >
            i
          </button>
        }
      />
      {change.error && (
        <p className="form-error" role="alert">
          {change.error}
        </p>
      )}
      {open && (
        <div className="sg-sheet-wrap" ref={wrap}>
          <div className="sg-sheet" role="dialog" aria-labelledby={`${id}-about`} tabIndex={-1} ref={panel}>
            <div className="sg-sheet-head">
              <h3 className="set-subheading" id={`${id}-about`}>
                {titleOf(setting)}
              </h3>
              <button
                type="button"
                className="icon-button"
                aria-label="Close"
                onClick={() => {
                  setOpen(false);
                  button.current?.focus();
                }}
              >
                <Icon name="close" size={18} />
              </button>
            </div>
            <Detail setting={setting} change={change} editor={long && setting.changeable ? <Control setting={setting} change={change} id={id} labelledBy={`${id}-name`} /> : undefined} />
          </div>
        </div>
      )}
    </div>
  );
}

// Sketch c: a compact list, and a view of its own for each setting.

function RowC({ setting, onOpen }: { setting: Setting; onOpen: () => void }) {
  return (
    <button type="button" className="sg-nav" data-setting={setting.key} onClick={onOpen}>
      <span className="sg-nav-name">{titleOf(setting)}</span>
      <span className={setting.overridden ? "sg-nav-value is-changed" : "sg-nav-value"}>{valueText(setting, setting.value, 22)}</span>
      <Icon name="caret" size={16} />
    </button>
  );
}

function ViewC({ setting, onChanged, onSignedOut, onBack }: RowProps & { onBack: () => void }) {
  const change = useChange(setting, onChanged, onSignedOut);
  const id = useId();
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    heading.current?.focus();
    window.scrollTo?.(0, 0);
  }, []);
  return (
    <section className="set-section sg-view" data-setting={setting.key}>
      <div>
        <button type="button" className="sg-back" onClick={onBack}>
          <Icon name="caret" size={16} />
          General
        </button>
      </div>
      <div className="set-section-head">
        <h2 className="set-heading" id={`${id}-name`} tabIndex={-1} ref={heading}>
          {titleOf(setting)}
          {setting.overridden && <span className="sg-changed">Changed</span>}
        </h2>
        <p className="set-note">{shortLineOf(setting)}</p>
      </div>
      <div className="set-card sg-view-card">
        <Control setting={setting} change={change} id={id} labelledBy={`${id}-name`} />
        {change.error && (
          <p className="form-error" role="alert">
            {change.error}
          </p>
        )}
      </div>
      <Detail setting={setting} change={change} />
    </section>
  );
}

export function GeneralSettings({ onSignedOut, onThemeKept }: Props) {
  const [loaded, reload, put] = useLoaded<All>(listSettings, onSignedOut);
  const [params] = useSearchParams();
  const sketch: Sketch = params.get("sketch") === "b" ? "b" : params.get("sketch") === "c" ? "c" : "a";
  const [query, setQuery] = useState("");
  const [onlyChanged, setOnlyChanged] = useState(false);
  const [viewing, setViewing] = useState<string | null>(null);

  if (loaded.state !== "ready") {
    return (
      <Section title="General">
        <Waiting state={loaded.state} what="The settings" onRetry={reload} />
      </Section>
    );
  }
  const { settings, rules } = loaded.data;

  function changed(next: Setting) {
    put({ rules, settings: settings.map((s) => (s.key === next.key ? next : s)) });
    // The theme is a setting like any other, and the page wears it at once.
    if (next.key === "appearance.theme" && (next.value === "dark" || next.value === "light" || next.value === "system")) onThemeKept(next.value);
  }

  const picker = (
    <p className="sg-sketches">
      Sketch:{" "}
      {(["a", "b", "c"] as Sketch[]).map((s) => (
        <Link key={s} to={`?sketch=${s}`} className={s === sketch ? "is-on" : undefined}>
          {s}
        </Link>
      ))}
    </p>
  );

  const one = viewing === null ? undefined : settings.find((s) => s.key === viewing);
  if (sketch === "c" && one) {
    return (
      <>
        {picker}
        <ViewC setting={one} onChanged={changed} onSignedOut={onSignedOut} onBack={() => setViewing(null)} />
      </>
    );
  }

  const mine = settings.filter((s) => s.changeable);
  const fixed = settings.filter((s) => !s.changeable);
  const nChanged = mine.filter((s) => s.overridden).length;
  const keep = (s: Setting) => found(s, query) && (!onlyChanged || s.overridden);
  const shownMine = mine.filter(keep);
  const shownFixed = onlyChanged ? [] : fixed.filter((s) => found(s, query));
  const searching = query.trim() !== "" || onlyChanged;

  const row = (s: Setting) =>
    sketch === "a" ? (
      <RowA key={s.key} setting={s} onChanged={changed} onSignedOut={onSignedOut} />
    ) : sketch === "b" ? (
      <RowB key={s.key} setting={s} onChanged={changed} onSignedOut={onSignedOut} />
    ) : (
      <RowC key={s.key} setting={s} onOpen={() => setViewing(s.key)} />
    );

  return (
    <>
      {picker}
      <Section title="General" note="Every setting that is yours to change. A change is kept as soon as you make it.">
        <div className="sg-tools">
          <label className="sg-search">
            <Icon name="search" size={18} />
            <input type="search" className="input" placeholder="Search settings" aria-label="Search settings" value={query} autoComplete="off" spellCheck={false} onChange={(e) => setQuery(e.target.value)} />
          </label>
          <button type="button" className={onlyChanged ? "sg-chip is-on" : "sg-chip"} aria-pressed={onlyChanged} onClick={() => setOnlyChanged(!onlyChanged)}>
            Changed <span className="sg-chip-n">{nChanged}</span>
          </button>
        </div>
        <p className="set-hint" role="status">
          {searching ? `${shownMine.length} of ${mine.length} settings` : `${mine.length} settings`}
        </p>
      </Section>
      {groupsOf(shownMine).map((g) => (
        <section key={g.heading} className="sg-group">
          <h3 className="sg-group-name">{g.heading}</h3>
          <div className="set-card">{g.settings.map(row)}</div>
        </section>
      ))}
      {searching && shownMine.length === 0 && <p className="quiet">No setting of yours matches.</p>}
      <Section title="Set for the whole install" note="These are the admin's to change, so they are shown and cannot be changed here.">
        <details className="set-card set-details" open={query.trim() !== "" && shownFixed.length > 0 ? true : undefined}>
          <summary className="set-summary">
            {shownFixed.length} {query.trim() !== "" ? "matching settings" : "settings"}
          </summary>
          {groupsOf(shownFixed).map((g) => (
            <div key={g.heading} className="sg-fixed-group">
              <h3 className="sg-group-name">{g.heading}</h3>
              {g.settings.map(row)}
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
