// Operations and Monitor (P11-03): what is running for the person, how it
// is doing, what went wrong, and where the install is running out of room.
// docs/architecture/operations.md describes it. Drawn from
// design/screens/Operations.html with the design tokens, and provisional on
// the settings mockups (P1-07), which do not exist yet. Bars and trends are
// inline SVG whose sizes are attributes, so the page needs no style
// attribute, which the content security policy refuses, and no chart
// library.

import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { ApiError } from "./api";
import { AgentsSwitch } from "./AgentsSwitch";
import { Icon } from "./Icon";
import { openInspector } from "./Inspector";
import {
  amount,
  dollars,
  readOperations,
  type OpsLimit,
  type OpsModel,
  type OpsReport,
  type OpsSchedule,
  type OpsTask,
  type OpsTrace,
  type OpsTrend,
} from "./operationsCalls";
import "./operations.css";

/** How often the page reads the report again while it is open, in milliseconds. */
const refreshEvery = 60_000;

type Loaded = { state: "loading" } | { state: "failed"; signedOut: boolean } | { state: "ready"; report: OpsReport; read: number };

// ---------------------------------------------------------------------------
// Words.
// ---------------------------------------------------------------------------

function count(n: number): string {
  return n.toLocaleString("en-US");
}

function plural(n: number, one: string, many: string): string {
  return `${count(n)} ${n === 1 ? one : many}`;
}

/** How long ago a time was, in words, from now. */
function ago(iso: string | undefined, now: number): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return "";
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return "just now";
  const m = Math.round(s / 60);
  if (m < 60) return `${m} min ago`;
  const h = Math.round(m / 60);
  if (h < 48) return `${h} h ago`;
  return `${Math.round(h / 24)} days ago`;
}

/** A time to come, in words: a date and a time in the browser's zone, or "in N days". */
function ahead(iso: string | undefined, now: number): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return "";
  const days = (t - now) / 86_400_000;
  if (days < 1) return new Date(t).toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" });
  if (days < 60) return `in about ${Math.round(days)} days`;
  if (days < 730) return `in about ${Math.round(days / 30)} months`;
  return `in about ${Math.round(days / 365)} years`;
}

/** A time shown as it was sent, which for a schedule is the person's zone: Fri 27 Nov, 07:00. */
function wallClock(iso: string | undefined): string {
  if (!iso) return "";
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(iso);
  if (!m) return iso;
  const d = new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3])));
  const day = d.toLocaleDateString("en-GB", { weekday: "short", day: "numeric", month: "short", timeZone: "UTC" });
  return `${day}, ${m[4]}:${m[5]}`;
}

/** A window as the server writes it (24h0m0s) in words: 24 hours, 7 days, 30 minutes. */
function windowWords(d: string): string {
  const h = /^(\d+)h0m0s$/.exec(d);
  if (h) {
    const n = Number(h[1]);
    return n % 24 === 0 && n > 24 ? plural(n / 24, "day", "days") : plural(n, "hour", "hours");
  }
  const m = /^(\d+)m0s$/.exec(d);
  if (m) return plural(Number(m[1]), "minute", "minutes");
  return d;
}

function ms(n: number): string {
  if (n <= 0) return "none";
  if (n < 1000) return `${n} ms`;
  return `${(n / 1000).toLocaleString("en-US", { maximumFractionDigits: 1 })} s`;
}

function tokens(n: number): string {
  if (n >= 10_000) return `${(n / 1000).toLocaleString("en-US", { maximumFractionDigits: 0 })}k`;
  return count(n);
}

function sentence(text: string): string {
  const t = text.trim();
  if (t === "") return t;
  const first = t[0].toUpperCase() + t.slice(1);
  return /[.!?]$/.test(first) ? first : first + ".";
}

const statusWords: Record<string, { text: string; tone: string }> = {
  queued: { text: "Queued", tone: "warn" },
  running: { text: "Running", tone: "warn" },
  starting: { text: "Starting", tone: "warn" },
  paused: { text: "Paused", tone: "idle" },
  done: { text: "Done", tone: "ok" },
  stopped: { text: "Stopped", tone: "idle" },
  failed: { text: "Failed", tone: "bad" },
  gone: { text: "Gone", tone: "idle" },
};

function Pill({ tone, children }: { tone: string; children: ReactNode }) {
  return <span className={`ops-pill is-${tone}`}>{children}</span>;
}

function StatusPill({ status, waiting }: { status: string; waiting?: boolean }) {
  if (waiting) return <Pill tone="warn">Waiting for you</Pill>;
  const w = statusWords[status] ?? { text: sentence(status), tone: "idle" };
  return <Pill tone={w.tone}>{w.text}</Pill>;
}

const limitPill: Record<string, { text: string; tone: string }> = {
  ok: { text: "Fine", tone: "ok" },
  watch: { text: "Watch", tone: "warn" },
  full: { text: "Full", tone: "bad" },
  unknown: { text: "Not known", tone: "bad" },
};

const healthPill: Record<string, { text: string; tone: string }> = {
  good: { text: "Good", tone: "ok" },
  errors: { text: "Errors", tone: "warn" },
  failing: { text: "Not reachable", tone: "bad" },
  refused: { text: "Held back", tone: "warn" },
  idle: { text: "No calls", tone: "idle" },
};

const kindWords: Record<string, string> = { chat: "Chat turn", task: "Background task", schedule: "Scheduled run", subagent: "Sub-agent" };

// ---------------------------------------------------------------------------
// Bars and trends.
// ---------------------------------------------------------------------------

/** A bar of a share out of 1, with a tick at the watch level. */
function Bar({ share, tone, mark, label }: { share: number; tone: string; mark?: number; label?: string }) {
  const width = Math.max(0, Math.min(1, Number.isFinite(share) ? share : 0)) * 1000;
  const a11y = label ? { role: "img", "aria-label": label } : { "aria-hidden": true };
  return (
    <svg className="ops-bar" viewBox="0 0 1000 10" preserveAspectRatio="none" {...a11y}>
      <rect className="ops-bar-track" x="0" y="0" width="1000" height="10" />
      {width > 0 && <rect className={`ops-bar-fill is-${tone}`} x="0" y="0" width={Math.max(width, 6)} height="10" />}
      {mark !== undefined && mark > 0 && mark < 1 && <rect className="ops-bar-mark" x={mark * 1000 - 3} y="0" width="6" height="10" />}
    </svg>
  );
}

/** A limit's trend as a line, with the limit itself as the top. */
function Spark({ trend, tone }: { trend: OpsTrend; tone: string }) {
  const pts = trend.points;
  if (pts.length < 2) return null;
  const t0 = Date.parse(pts[0].at);
  const t1 = Date.parse(pts[pts.length - 1].at);
  const top = Math.max(...pts.map((p) => Math.max(p.used, 0)), 1);
  const span = Math.max(t1 - t0, 1);
  const line = pts
    .map((p) => {
      const x = ((Date.parse(p.at) - t0) / span) * 120;
      const y = 28 - (Math.max(p.used, 0) / top) * 26;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
  return (
    <svg className="ops-spark" viewBox="0 0 120 30" preserveAspectRatio="none" aria-hidden="true">
      <polyline className={`ops-spark-line is-${tone}`} points={line} fill="none" vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

function toneOf(state: string): string {
  return state === "ok" ? "ok" : state === "watch" ? "warn" : "bad";
}

function growth(l: OpsLimit): string {
  if (!l.trend.known) return "Too early to tell";
  if (l.trend.per_day === 0) return "Steady";
  const sign = l.trend.per_day < 0 ? "Down " : "";
  return `${sign}${amount(l, Math.abs(l.trend.per_day))} a day`;
}

function reaches(l: OpsLimit, now: number): string {
  if (l.state === "unknown") return "";
  if (l.state === "full") return "Reached";
  if (!l.trend.known) return "Not known yet";
  if (l.trend.resets) {
    const resets = new Date(l.trend.resets).toLocaleDateString("en-GB", { day: "numeric", month: "long", timeZone: "UTC" });
    if (!l.trend.full_at || Date.parse(l.trend.full_at) >= Date.parse(l.trend.resets)) {
      return l.trend.at_reset !== undefined ? `Not before ${resets}: ${amount(l, l.trend.at_reset)} by then` : `Not before ${resets}`;
    }
  }
  if (!l.trend.full_at) return l.trend.per_day <= 0 ? "Not at this rate" : "Not within a hundred years";
  return ahead(l.trend.full_at, now);
}

// ---------------------------------------------------------------------------
// The page.
// ---------------------------------------------------------------------------

/** Operations and Monitor, at /operations. */
export function Operations({ onSignedOut }: { onSignedOut?: () => void }) {
  const [loaded, setLoaded] = useState<Loaded>({ state: "loading" });
  const [now, setNow] = useState(() => Date.now());

  const load = useCallback(async () => {
    try {
      const report = await readOperations();
      setLoaded({ state: "ready", report, read: Date.now() });
    } catch (e) {
      const signedOut = e instanceof ApiError && e.kind === "signed-out";
      if (signedOut) onSignedOut?.();
      setLoaded((l) => (l.state === "ready" ? l : { state: "failed", signedOut }));
    }
  }, [onSignedOut]);

  useEffect(() => {
    void load();
    const again = window.setInterval(() => {
      if (document.visibilityState === "visible") void load();
    }, refreshEvery);
    const tick = window.setInterval(() => setNow(Date.now()), 5000);
    return () => {
      window.clearInterval(again);
      window.clearInterval(tick);
    };
  }, [load]);

  return (
    <div className="ops">
      <header className="ops-head">
        <div>
          <h1 className="page-title">Operations</h1>
          <p className="ops-sub">
            What is running, how it is doing, what went wrong, and where room is running out.
            {loaded.state === "ready" && ` Updated ${ago(new Date(loaded.read).toISOString(), now)}.`}
          </p>
        </div>
        <button type="button" className="button" onClick={() => void load()}>
          <Icon name="retry" size={16} />
          Read again
        </button>
      </header>
      <AgentsSwitch onChange={() => void load()} />
      {loaded.state === "loading" && (
        <p className="quiet" aria-busy="true">
          Loading.
        </p>
      )}
      {loaded.state === "failed" && (
        <p className="quiet">{loaded.signedOut ? "You are signed out. Sign in, then open this page again." : "The report could not be read. Check that the server is running, then read it again."}</p>
      )}
      {loaded.state === "ready" && <Report report={loaded.report} now={now} />}
    </div>
  );
}

function Report({ report, now }: { report: OpsReport; now: number }) {
  const limits = report.limits.filter((l) => !l.key.startsWith("run:"));
  const runs = report.limits.filter((l) => l.key.startsWith("run:"));
  return (
    <>
      <Tiles report={report} now={now} />
      <Attention report={report} now={now} />
      <section className="ops-section" aria-label="Room to grow">
        <h2 className="ops-over">Room to grow</h2>
        <LimitTable limits={limits} now={now} />
        {runs.length > 0 && (
          <>
            <h3 className="ops-over ops-over-small">Budgets of runs under way</h3>
            <LimitTable limits={runs} now={now} />
          </>
        )}
      </section>
      <Models report={report} now={now} />
      <Tasks report={report} now={now} />
      <Schedules report={report} />
      <Traces traces={report.traces} now={now} />
    </>
  );
}

function Tile({ over, pill, big, children }: { over: string; pill: ReactNode; big: ReactNode; children?: ReactNode }) {
  return (
    <div className="set-card ops-tile">
      <div className="ops-tile-head">
        <p className="ops-over">{over}</p>
        {pill}
      </div>
      <div className="ops-big">{big}</div>
      {children}
    </div>
  );
}

function Tiles({ report, now }: { report: OpsReport; now: number }) {
  const j = report.jobs;
  const running = (j.counts.running ?? 0) + (j.counts.queued ?? 0);
  const cap = report.limits.find((l) => l.key === "cap");
  const calls = report.models.models.reduce((n, m) => n + m.calls, 0);
  const errors = report.models.models.reduce((n, m) => n + m.errors, 0);
  const failedTraces = report.traces.filter((t) => t.status === "failed").length;
  return (
    <div className="ops-tiles">
      <Tile over="Running now" pill={j.paused ? <Pill tone="idle">Paused</Pill> : running > 0 ? <Pill tone="warn">Running</Pill> : <Pill tone="ok">Quiet</Pill>} big={count(running)}>
        <p className="ops-small">
          {plural(j.open, "background task or run is", "background tasks and runs are")} open
          {(j.counts.waiting ?? 0) > 0 && `, ${count(j.counts.waiting)} waiting for you`}
          {(j.counts.paused ?? 0) > 0 && `, ${count(j.counts.paused)} paused`}.
        </p>
      </Tile>
      <Tile
        over={`Failed, last ${windowWords(report.models.window)}`}
        pill={j.failed_count + failedTraces > 0 ? <Pill tone="bad">{plural(j.failed_count + failedTraces, "to look at", "to look at")}</Pill> : <Pill tone="ok">None</Pill>}
        big={count(j.failed_count)}
      >
        <p className="ops-small">
          Background tasks and runs. {plural(failedTraces, "recent turn", "recent turns")} failed too.
        </p>
      </Tile>
      <Tile over="Model calls" pill={errors > 0 ? <Pill tone="warn">{plural(errors, "error", "errors")}</Pill> : <Pill tone="ok">Good</Pill>} big={count(calls)}>
        <p className="ops-small">Since {ago(report.models.since, now)}, across {plural(report.models.models.filter((m) => m.calls > 0).length, "model", "models")}.</p>
      </Tile>
      {cap && (
        <Tile
          over="Spend this month"
          pill={<Pill tone={cap.state === "ok" ? "ok" : toneOf(cap.state)}>{cap.state === "ok" ? "On track" : limitPill[cap.state]?.text}</Pill>}
          big={
            cap.state === "unknown" ? (
              dollars(cap.used)
            ) : (
              <>
                {dollars(cap.used)} <span className="ops-big-of">of {dollars(cap.of)}</span>
              </>
            )
          }
        >
          {cap.state !== "unknown" && <Bar share={cap.of > 0 ? cap.used / cap.of : 1} tone={toneOf(cap.state)} mark={cap.watch / 100} label={`${cap.percent} percent of the monthly cap used`} />}
          <p className="ops-small">
            {cap.problem
              ? sentence(cap.problem)
              : cap.trend.at_reset !== undefined && cap.trend.known
                ? `At this rate, ${dollars(cap.trend.at_reset)} by the end of the month.`
                : "Too early in the month to tell a rate."}
          </p>
        </Tile>
      )}
    </div>
  );
}

type AttentionRow = { key: string; tone: string; state: string; what: ReactNode; why: string; when: string; go?: ReactNode };

function Attention({ report, now }: { report: OpsReport; now: number }) {
  const rows: AttentionRow[] = [];
  for (const l of report.limits) {
    if (!l.crossed) continue;
    rows.push({
      key: "limit-" + l.key,
      tone: toneOf(l.state),
      state: limitPill[l.state]?.text ?? "Watch",
      what: l.label,
      why: l.problem
        ? sentence(l.problem)
        : `${amount(l, l.used)} of ${amount(l, l.of)}, ${l.percent}%. Watched from ${l.watch}%.${l.trend.full_at && l.state !== "full" ? ` Fills ${ahead(l.trend.full_at, now)} at this rate.` : ""}`,
      when: "now",
      go: l.key === "cap" || l.key === "memory_reserve" ? <Link to="/settings/usage">Usage</Link> : l.chat_id ? <Link to={`/chats/${l.chat_id}`}>Its chat</Link> : undefined,
    });
  }
  for (const t of report.jobs.stuck) {
    rows.push({
      key: "stuck-" + t.id,
      tone: "warn",
      state: "Stuck",
      what: t.title,
      why: `Its round began ${ago(t.round_started ?? t.created, now)} and has not ended. ${t.progress ?? ""}`.trim(),
      when: ago(t.round_started ?? t.created, now),
      go: <Link to={`/chats/${t.work_chat_id}`}>Its chat</Link>,
    });
  }
  for (const t of report.jobs.failed) {
    rows.push({
      key: "failed-" + t.id,
      tone: "bad",
      state: "Failed",
      what: t.title,
      why: t.error ? sentence(t.error) : "It did not finish.",
      when: ago(t.ended ?? t.created, now),
      go: <Link to={`/chats/${t.work_chat_id}`}>Its chat</Link>,
    });
  }
  for (const m of report.models.models) {
    if (m.state !== "failing" && m.state !== "errors") continue;
    rows.push({
      key: "model-" + m.name,
      tone: m.state === "failing" ? "bad" : "warn",
      state: healthPill[m.state].text,
      what: m.label,
      why: `${m.errors} of ${m.calls - m.refusals} calls failed.${m.last_error ? ` Last: ${sentence(m.last_error.message)}` : ""}`,
      when: ago(m.last_error?.at, now),
      go: <Link to="/settings/models">Models</Link>,
    });
  }
  for (const s of report.schedules.schedules) {
    if (s.last_run?.status !== "failed") continue;
    rows.push({
      key: "schedule-" + s.id,
      tone: "bad",
      state: "Failed",
      what: s.name,
      why: s.last_run.error ? sentence(s.last_run.error) : "Its last run did not finish.",
      when: ago(s.last_run.started, now),
      go: <Link to="/scheduled">Scheduled</Link>,
    });
  }
  return (
    <section className="ops-section" aria-label="Needs attention">
      <h2 className="ops-over">Needs attention</h2>
      {rows.length === 0 ? (
        <p className="set-card set-empty">Nothing needs you. Every limit is under its watch level, and nothing failed or is stuck.</p>
      ) : (
        <div className="set-card ops-table-wrap">
          <table className="ops-table">
            <thead>
              <tr>
                <th scope="col">State</th>
                <th scope="col">What</th>
                <th scope="col">Why</th>
                <th scope="col">When</th>
                <th scope="col">
                  <span className="ops-hide">Where to look</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.key}>
                  <td>
                    <Pill tone={r.tone}>{r.state}</Pill>
                  </td>
                  <td className="ops-name">{r.what}</td>
                  <td>{r.why}</td>
                  <td className="ops-mono">{r.when}</td>
                  <td>{r.go}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function LimitTable({ limits, now }: { limits: OpsLimit[]; now: number }) {
  return (
    <div className="set-card ops-table-wrap">
      <table className="ops-table">
        <thead>
          <tr>
            <th scope="col">Limit</th>
            <th scope="col">Used</th>
            <th scope="col">Trend</th>
            <th scope="col">Reaches the limit</th>
            <th scope="col">State</th>
          </tr>
        </thead>
        <tbody>
          {limits.map((l) => {
            const pill = limitPill[l.state] ?? limitPill.unknown;
            const tone = toneOf(l.state);
            return (
              <tr key={l.key}>
                <td>
                  <div className="ops-name">
                    {l.chat_id ? <Link to={`/chats/${l.chat_id}`}>{l.label}</Link> : l.label}
                    {l.scope === "install" && <span className="ops-tag">Install</span>}
                  </div>
                  {l.note && <div className="ops-small">{l.note}</div>}
                </td>
                <td className="ops-used">
                  {l.state === "unknown" ? (
                    <span className="ops-small">{sentence(l.problem ?? "Not known.")}</span>
                  ) : (
                    <>
                      <span className="ops-mono">
                        {amount(l, l.used)} of {amount(l, l.of)} · {l.percent}%
                      </span>
                      <Bar share={l.of > 0 ? l.used / l.of : 1} tone={tone} mark={l.watch / 100} label={`${l.percent} percent used; watched from ${l.watch} percent`} />
                    </>
                  )}
                </td>
                <td className="ops-trend">
                  <Spark trend={l.trend} tone={tone} />
                  <span className="ops-mono">{growth(l)}</span>
                </td>
                <td className="ops-mono">{reaches(l, now)}</td>
                <td>
                  <Pill tone={pill.tone}>{pill.text}</Pill>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function Models({ report, now }: { report: OpsReport; now: number }) {
  const list: OpsModel[] = report.models.models;
  return (
    <section className="ops-section" aria-label="Models">
      <h2 className="ops-over">Models, since {ago(report.models.since, now)}</h2>
      {list.length === 0 ? (
        <p className="set-card set-empty">
          No models are in your registry, and none took a call. Add one on <Link to="/settings/models">Models and keys</Link>.
        </p>
      ) : (
        <div className="set-card ops-table-wrap">
          <table className="ops-table">
            <thead>
              <tr>
                <th scope="col">Model</th>
                <th scope="col">State</th>
                <th scope="col">Median time</th>
                <th scope="col">Errors</th>
                <th scope="col">Calls</th>
                <th scope="col">Spend</th>
                <th scope="col">Last error</th>
              </tr>
            </thead>
            <tbody>
              {list.map((m) => {
                const pill = healthPill[m.state] ?? healthPill.idle;
                return (
                  <tr key={m.name}>
                    <td>
                      <div className="ops-name">
                        {m.label}
                        <span className="ops-tag">{m.class}</span>
                      </div>
                      <div className="ops-small">
                        {m.where}
                        {!m.in_registry && " No longer in your registry."}
                        {m.problem && ` ${sentence(m.problem)}`}
                      </div>
                    </td>
                    <td>
                      <Pill tone={pill.tone}>{pill.text}</Pill>
                    </td>
                    <td className="ops-mono">{ms(m.median_ms)}</td>
                    <td className="ops-mono">{m.calls - m.refusals > 0 ? `${m.error_percent}%` : "none"}</td>
                    <td className="ops-mono">
                      {count(m.calls)}
                      {m.refusals > 0 && <span className="ops-small"> ({count(m.refusals)} not sent)</span>}
                    </td>
                    <td className="ops-mono">{dollars(m.cost_micros)}</td>
                    <td className="ops-small">{m.last_error ? `${ago(m.last_error.at, now)}: ${sentence(m.last_error.message)}` : ""}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function taskSpend(t: OpsTask): string {
  return t.budget_micros > 0 ? `${dollars(t.spent_micros)} of ${dollars(t.budget_micros)}` : dollars(t.spent_micros);
}

function Tasks({ report, now }: { report: OpsReport; now: number }) {
  const j = report.jobs;
  const order = ["running", "queued", "paused", "waiting", "done", "stopped", "failed"];
  const words = order.filter((k) => (j.counts[k] ?? 0) > 0).map((k) => `${count(j.counts[k])} ${k === "waiting" ? "waiting for you" : k}`);
  return (
    <section className="ops-section" aria-label="Background tasks">
      <h2 className="ops-over">Background tasks and runs</h2>
      <p className="ops-small">{words.length > 0 ? sentence(words.join(", ")) : "None yet."}</p>
      {j.newest.length > 0 && (
        <div className="set-card ops-table-wrap">
          <table className="ops-table">
            <thead>
              <tr>
                <th scope="col">Task</th>
                <th scope="col">State</th>
                <th scope="col">Steps</th>
                <th scope="col">Spend</th>
                <th scope="col">Started</th>
              </tr>
            </thead>
            <tbody>
              {j.newest.map((t) => (
                <tr key={t.id}>
                  <td>
                    <div className="ops-name">
                      <Link to={`/chats/${t.work_chat_id}`}>{t.title}</Link>
                      {t.scheduled && <span className="ops-tag">Scheduled</span>}
                    </div>
                    {(t.error || t.progress) && <div className="ops-small">{t.error ? sentence(t.error) : t.progress}</div>}
                  </td>
                  <td>
                    <StatusPill status={t.status} waiting={t.waiting && (t.status === "running" || t.status === "queued")} />
                  </td>
                  <td className="ops-mono">{count(t.steps)}</td>
                  <td className="ops-mono">{taskSpend(t)}</td>
                  <td className="ops-mono">{ago(t.created, now)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function scheduleState(s: OpsSchedule): ReactNode {
  if (!s.enabled) return <Pill tone="idle">Paused</Pill>;
  if (s.held) return <Pill tone="idle">Held</Pill>;
  if (!s.last_run) return <Pill tone="ok">Waiting</Pill>;
  return <StatusPill status={s.last_run.status} waiting={s.last_run.waiting} />;
}

function Schedules({ report }: { report: OpsReport }) {
  const s = report.schedules;
  return (
    <section className="ops-section" aria-label="Schedules">
      <h2 className="ops-over">Schedules</h2>
      {s.schedules.length === 0 ? (
        <p className="set-card set-empty">
          No scheduled tasks. Make one on <Link to="/scheduled">Scheduled</Link>.
        </p>
      ) : (
        <>
          <p className="ops-small">
            {plural(s.enabled, "schedule is", "schedules are")} on, in {s.zone}.{s.paused && " Every schedule is held by your switch."}
          </p>
          <div className="set-card ops-table-wrap">
            <table className="ops-table">
              <thead>
                <tr>
                  <th scope="col">Schedule</th>
                  <th scope="col">Next</th>
                  <th scope="col">Last run</th>
                  <th scope="col">State</th>
                </tr>
              </thead>
              <tbody>
                {s.schedules.map((x) => (
                  <tr key={x.id}>
                    <td>
                      <div className="ops-name">
                        <Link to="/scheduled">{x.name}</Link>
                      </div>
                      <div className="ops-small">{x.says}</div>
                    </td>
                    <td className="ops-mono">{x.next ? wallClock(x.next) : "none"}</td>
                    <td>
                      {x.last_run ? (
                        <>
                          <span className="ops-mono">{wallClock(x.last_run.started)}</span>
                          {x.last_run.late && <span className="ops-small"> late{x.last_run.late_reason === "stopped" ? ": Keystone was not running" : x.last_run.late_reason === "busy" ? ": the run before was still going" : ""}</span>}
                          {x.last_run.error && <div className="ops-small">{sentence(x.last_run.error)}</div>}
                          {x.last_run.chat_id && (
                            <div className="ops-small">
                              <Link to={`/chats/${x.last_run.chat_id}`}>Its chat</Link>
                            </div>
                          )}
                        </>
                      ) : (
                        <span className="ops-small">Not run yet</span>
                      )}
                    </td>
                    <td>{scheduleState(x)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </section>
  );
}

function Traces({ traces, now }: { traces: OpsTrace[]; now: number }) {
  return (
    <section className="ops-section" aria-label="Recent traces">
      <h2 className="ops-over">Recent traces</h2>
      {traces.length === 0 ? (
        <p className="set-card set-empty">No turns yet.</p>
      ) : (
        <div className="set-card ops-table-wrap">
          <table className="ops-table">
            <thead>
              <tr>
                <th scope="col">When</th>
                <th scope="col">What</th>
                <th scope="col">Calls</th>
                <th scope="col">Tool steps</th>
                <th scope="col">Tokens in · out</th>
                <th scope="col">Cost</th>
                <th scope="col">Outcome</th>
              </tr>
            </thead>
            <tbody>
              {traces.map((t) => (
                <tr key={t.request_id}>
                  <td className="ops-mono">{ago(t.started, now)}</td>
                  <td>
                    <div className="ops-name">
                      {t.chat_id && t.turn_id ? (
                        <Link to={`/chats/${t.chat_id}`} onClick={() => openInspector(t.chat_id!, t.turn_id!)} title="Open the chat with the turn inspector">
                          {t.title}
                        </Link>
                      ) : (
                        t.title
                      )}
                    </div>
                    <div className="ops-small">
                      {kindWords[t.kind] ?? t.kind}
                      {t.turns > 1 && `, ${plural(t.turns, "turn", "turns")}`}
                      {t.models.length > 0 && ` · ${t.models.join(", ")}`}
                    </div>
                    {t.error && <div className="ops-small ops-err">{sentence(t.error)}</div>}
                  </td>
                  <td className="ops-mono">
                    {count(t.calls)}
                    {t.errors > 0 && <span className="ops-small"> ({count(t.errors)} failed)</span>}
                    {t.refusals > 0 && <span className="ops-small"> ({count(t.refusals)} not sent)</span>}
                  </td>
                  <td className="ops-small">{t.tools.length === 0 ? "none" : t.tools.map((s, i) => <span key={i} className={s.outcome === "ok" ? "" : "ops-err"}>{(i > 0 ? ", " : "") + s.name}</span>)}</td>
                  <td className="ops-mono">
                    {tokens(t.usage.input_tokens + t.usage.cache_read_tokens + t.usage.cache_write_tokens)} · {tokens(t.usage.output_tokens)}
                  </td>
                  <td className="ops-mono">{dollars(t.usage.cost_micros)}</td>
                  <td>
                    <StatusPill status={t.status} waiting={t.waiting} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
