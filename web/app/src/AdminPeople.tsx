// The admin account's screens for people, the install and monitoring
// (P15-02): People, A person and Install, drawn from
// design/screens/Admin.html and Operations.html with the design tokens,
// and provisional on those drafts. Every figure is a number: an admin
// never sees a person's chats, files, memory or logs, and each read is
// written in that person's own log, so the lists are read on opening and
// on Read again, never on a timer. docs/architecture/admin.md.

import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react";
import { ApiError } from "./api";
import {
  addPerson,
  bytes,
  dollars,
  failureWords,
  limitWords,
  listAdminPeople,
  newSetupCode,
  readAdminPerson,
  readInstall,
  readPersonUsage,
  setEnabled,
  type AdminPerson as Row,
  type DayUse,
  type Install,
  type ModelUse,
  type PersonUsage,
  type SetupCode,
} from "./adminPeopleCalls";
import { Icon } from "./Icon";
import "./operations.css";
import "./admin-people.css";

export type AdminTab = { kind: "people" } | { kind: "person"; id: string } | { kind: "install" };

function when(iso: string | undefined): string {
  if (!iso) return "never";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString("en-GB", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
}

function count(n: number): string {
  return n.toLocaleString("en-US");
}

function failedWords(e: unknown): string {
  if (e instanceof ApiError && e.kind === "signed-out") return "The admin session has ended. Sign in again.";
  if (e instanceof ApiError && e.said) return e.said;
  return "That did not work. Check that the server is running, then try again.";
}

function Pill({ tone, children }: { tone: "ok" | "warn" | "bad" | "idle"; children: ReactNode }) {
  return <span className={`ops-pill${tone === "idle" ? "" : ` is-${tone}`}`}>{children}</span>;
}

function stateOf(p: Row): ReactNode {
  if (!p.enabled) return <Pill tone="idle">Disabled</Pill>;
  if (!p.has_password) return <Pill tone="warn">{p.setup_until ? "Waiting for setup" : "No password yet"}</Pill>;
  if (p.crossed.length > 0) return <Pill tone="warn">Watch</Pill>;
  return <Pill tone="ok">Active</Pill>;
}

function spendWords(p: Row): string {
  if (p.spend.state === "unread") return `${dollars(p.spend.spent_micros)}, cap not set`;
  return `${dollars(p.spend.spent_micros)} of ${dollars(p.spend.limit_micros)}`;
}

function modelWords(m: Record<string, number>): string {
  const order: [string, string][] = [
    ["good", "good"],
    ["errors", "with errors"],
    ["failing", "failing"],
    ["refused", "refused"],
    ["idle", "idle"],
  ];
  const parts = order.filter(([k]) => (m[k] ?? 0) > 0).map(([k, w]) => `${m[k]} ${w}`);
  return parts.length ? parts.join(", ") : "none";
}

/** The setup code, shown once, to be handed to the person. */
function CodeCard({ name, code, onClose }: { name: string; code: SetupCode; onClose: () => void }) {
  return (
    <section className="adm-code" aria-label="Setup code" role="status">
      <p className="ops-over">Setup code for {name}</p>
      <p className="adm-code-text">{code.code}</p>
      <p className="ops-small">
        Give this to {name}. They open <span className="ops-mono">/setup</span> on this server, enter it, and choose their own password. It works once, until {when(code.expires)}. It is
        shown only now: Keystone keeps no copy it could show again.
      </p>
      <div>
        <button type="button" className="button" onClick={onClose}>
          Done
        </button>
      </div>
    </section>
  );
}

/** People: every person with their numbers, Add a person, and each one's controls. */
export function AdminPeople({ onOpen }: { onOpen: (id: string) => void }) {
  const [people, setPeople] = useState<Row[] | null>(null);
  const [error, setError] = useState("");
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [code, setCode] = useState<{ name: string; code: SetupCode } | null>(null);

  const load = useCallback(async () => {
    setError("");
    try {
      setPeople(await listAdminPeople());
    } catch (e) {
      setError(failedWords(e));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function add(e: FormEvent) {
    e.preventDefault();
    if (!name.trim() || busy) return;
    setBusy(true);
    setError("");
    try {
      const made = await addPerson(name.trim());
      setCode({ name: made.person.name, code: made.setup_code });
      setName("");
      setAdding(false);
      await load();
    } catch (err) {
      setError(failedWords(err));
    } finally {
      setBusy(false);
    }
  }

  async function toggle(p: Row) {
    if (p.enabled && !window.confirm(`Disable ${p.name}? They are signed out everywhere, cannot sign in, their schedules and tasks stop and their share links stop working. Nothing of theirs is deleted.`)) return;
    setError("");
    try {
      const after = await setEnabled(p.id, !p.enabled);
      setPeople((list) => (list ?? []).map((x) => (x.id === after.id ? after : x)));
    } catch (e) {
      setError(failedWords(e));
    }
  }

  return (
    <div className="ops-section">
      <div className="adm-bar">
        <p className="ops-small">Read when this page opens and when you read again. Each read is written in each person's own log, which they can see.</p>
        <div className="adm-actions">
          <button type="button" className="button" onClick={() => void load()}>
            <Icon name="retry" size={16} />
            Read again
          </button>
          <button type="button" className="button button-primary" onClick={() => setAdding((a) => !a)}>
            Add a person
          </button>
        </div>
      </div>
      {adding && (
        <form className="adm-add" onSubmit={add}>
          <label className="field">
            <span className="field-label">Their name</span>
            <input className="input" value={name} maxLength={100} onChange={(e) => setName(e.target.value)} autoFocus />
          </label>
          <button type="submit" className="button button-primary" disabled={!name.trim() || busy}>
            {busy ? "Adding" : "Add and make a setup code"}
          </button>
        </form>
      )}
      {code && <CodeCard name={code.name} code={code.code} onClose={() => setCode(null)} />}
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      {people === null && !error && <p className="ops-small">Loading.</p>}
      {people && (
        <div className="ops-table-wrap">
          <table className="ops-table">
            <thead>
              <tr>
                <th>Person</th>
                <th>Last here</th>
                <th>Spend this month</th>
                <th>Storage</th>
                <th>Tasks failed, stuck</th>
                <th>Schedules</th>
                <th>Models</th>
                <th>State</th>
                <th>
                  <span className="ops-hide">Controls</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {people.map((p) => (
                <tr key={p.id}>
                  <td className="ops-name">
                    {p.name}
                    <div className="ops-small ops-mono">{p.id}</div>
                  </td>
                  <td className="ops-mono">{when(p.last_sign_in)}</td>
                  <td className="ops-mono">{spendWords(p)}</td>
                  <td className="ops-mono">{bytes(p.storage.database + p.storage.files + p.storage.artifacts)}</td>
                  <td>
                    {p.jobs.failed > 0 ? <Pill tone="bad">{p.jobs.failed}</Pill> : <span className="ops-mono">0</span>}
                    <span className="ops-mono">, {p.jobs.stuck}</span>
                  </td>
                  <td className="ops-mono">{count(p.schedules)}</td>
                  <td className="ops-small">{modelWords(p.models)}</td>
                  <td>{stateOf(p)}</td>
                  <td>
                    <div className="adm-actions">
                      <button type="button" className="button" onClick={() => onOpen(p.id)}>
                        Open
                      </button>
                      <button type="button" className="button" onClick={() => void toggle(p)}>
                        {p.enabled ? "Disable" : "Enable"}
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

/** Bars for each day of a period, inline SVG whose sizes are attributes. */
function DayBars({ days, label }: { days: DayUse[]; label: string }) {
  if (days.length === 0) return <p className="ops-small">No calls in this period.</p>;
  const top = Math.max(1, ...days.map((d) => d.cost_micros));
  const w = 1000 / days.length;
  return (
    <svg className="adm-days" viewBox="0 0 1000 120" preserveAspectRatio="none" role="img" aria-label={label}>
      {days.map((d, i) => {
        const h = Math.max(2, (d.cost_micros / top) * 110);
        return (
          <rect key={d.day} className="adm-day" x={i * w + w * 0.15} y={120 - h} width={w * 0.7} height={h}>
            <title>{`${d.day}: ${dollars(d.cost_micros)}, ${count(d.calls)} calls`}</title>
          </rect>
        );
      })}
    </svg>
  );
}

function ModelTable({ models }: { models: ModelUse[] }) {
  if (models.length === 0) return <p className="ops-small">No calls in this period.</p>;
  return (
    <div className="ops-table-wrap">
      <table className="ops-table">
        <thead>
          <tr>
            <th>Model</th>
            <th>Class</th>
            <th>Calls</th>
            <th>Failed</th>
            <th>Refused</th>
            <th>Tokens in, out</th>
            <th>Cost</th>
          </tr>
        </thead>
        <tbody>
          {models.map((m) => (
            <tr key={`${m.model}\u0000${m.class}`}>
              <td className="ops-name">{m.model}</td>
              <td>{m.class}</td>
              <td className="ops-mono">{count(m.calls)}</td>
              <td className="ops-mono">{count(m.errors)}</td>
              <td className="ops-mono">{count(m.refusals)}</td>
              <td className="ops-mono">
                {count(m.input_tokens)}, {count(m.output_tokens)}
              </td>
              <td className="ops-mono">{dollars(m.cost_micros)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Failures({ failures }: { failures: Record<string, number> }) {
  return (
    <div className="ops-table-wrap">
      <table className="ops-table">
        <tbody>
          {Object.entries(failureWords).map(([k, words]) => (
            <tr key={k}>
              <td>{words}</td>
              <td className="ops-mono">{count(failures[k] ?? 0)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Tile({ over, big, small }: { over: string; big: ReactNode; small?: ReactNode }) {
  return (
    <div className="ops-tile">
      <p className="ops-over">{over}</p>
      <div className="ops-big">{big}</div>
      {small && <p className="ops-small">{small}</p>}
    </div>
  );
}

/** A person: their numbers, their usage over a period, failures by kind and storage over time. */
export function AdminPersonView({ id, onBack }: { id: string; onBack: () => void }) {
  const [person, setPerson] = useState<Row | null>(null);
  const [use, setUse] = useState<PersonUsage | null>(null);
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [error, setError] = useState("");
  const [code, setCode] = useState<SetupCode | null>(null);

  const load = useCallback(async () => {
    setError("");
    try {
      const [p, u] = await Promise.all([readAdminPerson(id), readPersonUsage(id, from, to)]);
      setPerson(p);
      setUse(u);
    } catch (e) {
      setError(failedWords(e));
    }
  }, [id, from, to]);

  useEffect(() => {
    void load();
    // A period is read when Read again is pressed, not as each date is typed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  async function makeCode() {
    if (!person) return;
    if (person.has_password && !window.confirm(`Make a new setup code for ${person.name}? When it is used, it replaces their password and signs them out everywhere. They will see that you made it.`)) return;
    setError("");
    try {
      setCode(await newSetupCode(person.id));
      setPerson(await readAdminPerson(person.id));
    } catch (e) {
      setError(failedWords(e));
    }
  }

  async function toggle() {
    if (!person) return;
    if (person.enabled && !window.confirm(`Disable ${person.name}? They are signed out everywhere and cannot sign in until you enable them. Nothing of theirs is deleted.`)) return;
    setError("");
    try {
      setPerson(await setEnabled(person.id, !person.enabled));
    } catch (e) {
      setError(failedWords(e));
    }
  }

  return (
    <div className="ops-section adm-person">
      <div className="adm-bar">
        <button type="button" className="button" onClick={onBack}>
          Back to people
        </button>
        <div className="adm-actions">
          <button type="button" className="button" onClick={() => void makeCode()} disabled={!person || !person.enabled}>
            New setup code
          </button>
          <button type="button" className="button" onClick={() => void toggle()} disabled={!person}>
            {person?.enabled === false ? "Enable" : "Disable"}
          </button>
        </div>
      </div>
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      {person && code && <CodeCard name={person.name} code={code} onClose={() => setCode(null)} />}
      {person && (
        <>
          <h2 className="adm-title">
            {person.name} {stateOf(person)}
          </h2>
          <p className="ops-small">
            Added {when(person.added)}. Last signed in {when(person.last_sign_in)}.{!person.enabled && ` Disabled since ${when(person.disabled_at)}.`}
            {person.setup_until && ` A setup code works until ${when(person.setup_until)}.`}
          </p>
          <div className="ops-tiles">
            <Tile over="Spend this month" big={dollars(person.spend.spent_micros)} small={person.spend.state === "unread" ? "The cap does not read." : `of ${dollars(person.spend.limit_micros)}, warning at ${person.spend.warn_percent}%`} />
            <Tile over="Storage" big={bytes(person.storage.database + person.storage.files + person.storage.artifacts)} small={`database ${bytes(person.storage.database)}, files ${bytes(person.storage.files)}, artifacts ${bytes(person.storage.artifacts)}`} />
            <Tile over="Tasks" big={`${person.jobs.failed} failed`} small={`${person.jobs.stuck} stuck, ${person.schedules} schedules`} />
            <Tile over="Models" big={modelWords(person.models)} small={person.crossed.length ? `Watch: ${person.crossed.map(limitWords).join(", ")}` : "No limit at its watch level."} />
          </div>
        </>
      )}
      <form
        className="adm-period"
        onSubmit={(e) => {
          e.preventDefault();
          void load();
        }}
      >
        <label className="field">
          <span className="field-label">From</span>
          <input className="input" type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
        </label>
        <label className="field">
          <span className="field-label">To</span>
          <input className="input" type="date" value={to} onChange={(e) => setTo(e.target.value)} />
        </label>
        <button type="submit" className="button">
          <Icon name="retry" size={16} />
          Read again
        </button>
      </form>
      {use && (
        <>
          <p className="ops-small">
            {use.period.from} to {use.period.to}: {count(use.total.calls)} calls, {dollars(use.total.cost_micros)}, of which {dollars(use.total.capped_micros)} on hosted and trusted models.
          </p>
          <h3 className="ops-over">Spend by day</h3>
          <DayBars days={use.days} label={`${person?.name ?? "This person"}'s spend by day`} />
          <h3 className="ops-over">By model</h3>
          <ModelTable models={use.models} />
          <h3 className="ops-over">Failures by kind</h3>
          <Failures failures={use.failures} />
          <h3 className="ops-over">Storage over time</h3>
          {use.storage.length === 0 ? (
            <p className="ops-small">No snapshots in this period.</p>
          ) : (
            <div className="ops-table-wrap">
              <table className="ops-table">
                <thead>
                  <tr>
                    <th>When</th>
                    <th>Database</th>
                    <th>Files</th>
                    <th>Artifacts</th>
                  </tr>
                </thead>
                <tbody>
                  {use.storage.slice(-14).map((s) => (
                    <tr key={s.at}>
                      <td className="ops-mono">{when(s.at)}</td>
                      <td className="ops-mono">{bytes(s.database)}</td>
                      <td className="ops-mono">{bytes(s.files)}</td>
                      <td className="ops-mono">{bytes(s.artifacts)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </div>
  );
}

/** Install: everyone this month, the disk, the people and the sampler. */
export function AdminInstall() {
  const [install, setInstall] = useState<Install | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setError("");
    try {
      setInstall(await readInstall());
    } catch (e) {
      setError(failedWords(e));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <div className="ops-section">
      <div className="adm-bar">
        <p className="ops-small">Everyone's figures summed. Each read is written in each person's own log.</p>
        <button type="button" className="button" onClick={() => void load()}>
          <Icon name="retry" size={16} />
          Read again
        </button>
      </div>
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      {install === null && !error && <p className="ops-small">Loading.</p>}
      {install && (
        <>
          <div className="ops-tiles">
            <Tile over="Spend, everyone, this month" big={dollars(install.total.capped_micros)} small={`of ${dollars(install.cap_micros)} in caps; ${count(install.total.calls)} calls`} />
            <Tile
              over="Disk"
              big={install.disk.problem ? "Not known" : `${install.disk.percent}% used`}
              small={install.disk.problem ? install.disk.problem : `${bytes(install.disk.used)} of ${bytes(install.disk.of)}, watch at ${install.disk.watch}%`}
            />
            <Tile over="People" big={count(install.people.total)} small={`${install.people.enabled} enabled, ${install.people.disabled} disabled, ${install.people.waiting} not set up`} />
            <Tile over="This server" big={install.version} small={`Snapshots every ${install.sampler.every}; newest ${when(install.sampler.newest)}`} />
          </div>
          <h3 className="ops-over">Spend by day</h3>
          <DayBars days={install.days} label="Everyone's spend by day" />
          <h3 className="ops-over">By model</h3>
          <ModelTable models={install.models} />
          <h3 className="ops-over">Failures by kind</h3>
          <Failures failures={install.failures} />
          <p className="ops-small">The install settings that were changed are shown with admin settings, when they come.</p>
        </>
      )}
    </div>
  );
}

/** The admin's sections: People (with A person) and Install. */
export function AdminSections() {
  const [tab, setTab] = useState<AdminTab>({ kind: "people" });
  return (
    <div className="ops adm">
      <header className="ops-head">
        <div>
          <h1 className="adm-h1">Admin</h1>
          <p className="ops-sub">This install and the people on it. Admin sees counts and spend. It never sees another person's chats, files, memory or keys.</p>
        </div>
      </header>
      <div role="tablist" aria-label="Admin sections" className="adm-tabs">
        <button type="button" role="tab" aria-selected={tab.kind !== "install"} className="adm-tab" onClick={() => setTab({ kind: "people" })}>
          People
        </button>
        <button type="button" role="tab" aria-selected={tab.kind === "install"} className="adm-tab" onClick={() => setTab({ kind: "install" })}>
          Install
        </button>
      </div>
      {tab.kind === "people" && <AdminPeople onOpen={(id) => setTab({ kind: "person", id })} />}
      {tab.kind === "person" && <AdminPersonView id={tab.id} onBack={() => setTab({ kind: "people" })} />}
      {tab.kind === "install" && <AdminInstall />}
    </div>
  );
}
