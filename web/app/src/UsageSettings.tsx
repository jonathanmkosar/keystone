// The Usage settings screen (P11-02): what the person's model calls cost
// for a period, against the monthly cap, by model, by chat and by kind of
// work. docs/architecture/usage-report.md describes it. Provisional on the
// settings mockups (P1-07): built from the design tokens and the draft
// Operations and Admin screens. The bars are inline SVG, so the page needs
// no style attribute and no chart library (decision 0432).

import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Link } from "react-router";
import { ApiError } from "./api";
import { Icon } from "./Icon";
import { readUsage, type Tally, type UsageAsk, type UsageCap, type UsageReport, type UsageReserve, type UsageRow, type UsageSum } from "./usageCalls";
import "./usage.css";

const presets: { value: string; label: string }[] = [
  { value: "this_month", label: "This month" },
  { value: "last_month", label: "Last month" },
  { value: "last_7_days", label: "Last 7 days" },
  { value: "last_30_days", label: "Last 30 days" },
  { value: "month", label: "A month" },
  { value: "range", label: "Dates" },
];

const monthNames = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];

/** An amount in millionths of a dollar, to the cent. */
function dollars(micros: number): string {
  if (micros > 0 && micros < 5000) return "under $0.01";
  return "$" + (micros / 1e6).toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

function count(n: number): string {
  return n.toLocaleString("en-US");
}

function plural(n: number, one: string, many: string): string {
  return `${count(n)} ${n === 1 ? one : many}`;
}

/** 2026-11 as November 2026. */
function monthWords(month: string): string {
  const [y, m] = month.split("-").map(Number);
  return `${monthNames[m - 1] ?? month} ${y}`;
}

/** The days of a period in words: 1 to 30 November 2026. */
function periodWords(from: string, to: string): string {
  const [fy, fm, fd] = from.split("-").map(Number);
  const [ty, tm, td] = to.split("-").map(Number);
  if (from === to) return `${td} ${monthNames[tm - 1]} ${ty}`;
  if (fy === ty && fm === tm) return `${fd} to ${td} ${monthNames[tm - 1]} ${ty}`;
  if (fy === ty) return `${fd} ${monthNames[fm - 1]} to ${td} ${monthNames[tm - 1]} ${ty}`;
  return `${fd} ${monthNames[fm - 1]} ${fy} to ${td} ${monthNames[tm - 1]} ${ty}`;
}

/** The parts of a sum by class, in words, leaving out the classes with no calls. */
function classWords(s: UsageSum): string {
  const parts: string[] = [];
  const paid = (name: string, t: Tally) => t.calls > 0 && parts.push(`${name} ${dollars(t.cost_micros)}`);
  paid("hosted", s.hosted);
  paid("trusted", s.trusted);
  if (s.local.calls > 0) parts.push(`local ${plural(s.local.calls, "call", "calls")}, free`);
  return parts.join(" · ");
}

/** A bar of share out of 1, drawn in inline SVG from the tokens. mark is a tick at a share, such as the warning threshold. */
function Bar({ share, tone, mark, label, small }: { share: number; tone: string; mark?: number; label?: string; small?: boolean }) {
  const width = Math.max(0, Math.min(1, Number.isFinite(share) ? share : 0)) * 1000;
  const a11y = label ? { role: "img", "aria-label": label } : { "aria-hidden": true };
  return (
    <svg className={small ? "use-bar is-small" : "use-bar"} viewBox="0 0 1000 10" preserveAspectRatio="none" {...a11y}>
      <rect className="use-bar-track" x="0" y="0" width="1000" height="10" />
      {width > 0 && <rect className={`use-bar-fill is-${tone}`} x="0" y="0" width={Math.max(width, 6)} height="10" />}
      {mark !== undefined && mark > 0 && mark < 1 && <rect className="use-bar-mark" x={mark * 1000 - 3} y="0" width="6" height="10" />}
    </svg>
  );
}

const capPill: Record<string, { text: string; tone: string }> = {
  ok: { text: "On track", tone: "ok" },
  warning: { text: "Near the cap", tone: "warn" },
  reached: { text: "Cap reached", tone: "bad" },
  unread: { text: "Cap does not read", tone: "bad" },
};

function CapCard({ cap, reserve, thisMonth }: { cap: UsageCap; reserve: UsageReserve; thisMonth: boolean }) {
  const pill = capPill[cap.state] ?? capPill.ok;
  const share = cap.limit_micros > 0 ? cap.spent_micros / cap.limit_micros : cap.spent_micros > 0 ? 1 : 0;
  const percent = Math.round(share * 100);
  const tone = cap.state === "ok" ? "ok" : cap.state === "warning" ? "warn" : "bad";
  const when = thisMonth ? "this month" : `in ${monthWords(cap.month)}`;
  return (
    <div className="set-card use-cap">
      <div className="use-cap-head">
        <p className="use-over">Spend {when}</p>
        <span className={`set-pill use-pill is-${pill.tone}`}>{pill.text}</span>
      </div>
      {cap.state === "unread" ? (
        <>
          <p className="use-big">{dollars(cap.spent_micros)}</p>
          <p className="form-error">{sentence(cap.problem ?? "The monthly cap does not read, so every call to a model that is not local is stopped.")}</p>
        </>
      ) : (
        <>
          <p className="use-big">
            {dollars(cap.spent_micros)} <span className="use-big-of">of {dollars(cap.limit_micros)}</span>
          </p>
          <Bar
            share={share}
            tone={tone}
            mark={cap.warn_percent / 100}
            label={`${percent} percent of the monthly cap used. The warning starts at ${cap.warn_percent} percent.`}
          />
          <p className="set-hint">
            {cap.state === "reached"
              ? "The cap is reached: calls to hosted and trusted models are stopped until the month ends. A local model in your fallback chain still answers."
              : `${dollars(cap.left_micros)} left ${when}. The warning starts at ${dollars(cap.warn_at_micros)}, ${cap.warn_percent}% of the cap.`}{" "}
            The cap counts hosted and trusted models; local models cost nothing. The admin sets the cap, and the month is a calendar month in UTC.
          </p>
        </>
      )}
      <div className="use-reserve">
        {reserve.problem ? (
          <p className="set-hint">
            The memory pass spent {dollars(reserve.spent_micros)}. {sentence(reserve.problem)}
          </p>
        ) : (
          <>
            <p className="set-hint">
              The memory pass spent {dollars(reserve.spent_micros)} of its {dollars(reserve.limit_micros)} reserve {when}, a part of the cap.
            </p>
            <Bar small share={reserve.limit_micros > 0 ? reserve.spent_micros / reserve.limit_micros : 0} tone={reserve.left_micros > 0 ? "ok" : "bad"} />
          </>
        )}
      </div>
    </div>
  );
}

function sentence(text: string): string {
  const t = text.trim();
  if (t === "") return t;
  const first = t[0].toUpperCase() + t.slice(1);
  return /[.!?]$/.test(first) ? first : first + ".";
}

function Totals({ report }: { report: UsageReport }) {
  const t = report.total;
  return (
    <div className="set-card use-totals">
      <div className="use-total">
        <span className="use-over">Cost</span>
        <span className="use-total-value">{dollars(t.cost_micros)}</span>
      </div>
      <div className="use-total">
        <span className="use-over">Calls</span>
        <span className="use-total-value">{count(t.calls)}</span>
      </div>
      <div className="use-total">
        <span className="use-over">Tokens in</span>
        <span className="use-total-value">{count(t.input_tokens)}</span>
      </div>
      <div className="use-total">
        <span className="use-over">Tokens out</span>
        <span className="use-total-value">{count(t.output_tokens)}</span>
      </div>
      <div className="use-total">
        <span className="use-over">Read from cache</span>
        <span className="use-total-value">{count(t.cache_read_tokens)}</span>
      </div>
      <div className="use-total">
        <span className="use-over">Written to cache</span>
        <span className="use-total-value">{count(t.cache_write_tokens)}</span>
      </div>
      {t.calls > 0 && <p className="set-hint use-totals-split">{sentence(classWords(t))}</p>}
    </div>
  );
}

const classWordsFor: Record<string, string> = { local: "Local", hosted: "Hosted", trusted: "Trusted" };

function Breakdown({ title, note, rows, total, kind }: { title: string; note: ReactNode; rows: UsageRow[]; total: UsageSum; kind: "model" | "chat" | "work" }) {
  if (rows.length === 0) return null;
  const top = Math.max(...rows.map((r) => r.cost_micros), 1);
  return (
    <section className="set-section" aria-label={title}>
      <div className="set-section-head">
        <h2 className="set-heading">{title}</h2>
        <p className="set-note">{note}</p>
      </div>
      <div className="set-card use-table-wrap">
        <table className="use-table">
          <thead>
            <tr>
              <th scope="col">{kind === "model" ? "Model" : kind === "chat" ? "Chat" : "Kind of work"}</th>
              <th scope="col" className="use-num">
                Calls
              </th>
              <th scope="col" className="use-num">
                In
              </th>
              <th scope="col" className="use-num">
                Out
              </th>
              <th scope="col" className="use-num">
                Cache read
              </th>
              <th scope="col" className="use-num">
                Cache write
              </th>
              <th scope="col" className="use-num">
                Cost
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={`${r.key}/${r.class ?? ""}/${i}`} className={r.rest ? "is-rest" : undefined}>
                <th scope="row" className="use-name">
                  <RowName row={r} kind={kind} />
                  <Bar small share={total.cost_micros > 0 ? r.cost_micros / top : 0} tone="accent" />
                </th>
                <td className="use-num">{count(r.calls)}</td>
                <td className="use-num">{count(r.input_tokens)}</td>
                <td className="use-num">{count(r.output_tokens)}</td>
                <td className="use-num">{count(r.cache_read_tokens)}</td>
                <td className="use-num">{count(r.cache_write_tokens)}</td>
                <td className="use-num use-cost">{dollars(r.cost_micros)}</td>
              </tr>
            ))}
          </tbody>
          <tfoot>
            <tr>
              <th scope="row">Total</th>
              <td className="use-num">{count(total.calls)}</td>
              <td className="use-num">{count(total.input_tokens)}</td>
              <td className="use-num">{count(total.output_tokens)}</td>
              <td className="use-num">{count(total.cache_read_tokens)}</td>
              <td className="use-num">{count(total.cache_write_tokens)}</td>
              <td className="use-num use-cost">{dollars(total.cost_micros)}</td>
            </tr>
          </tfoot>
        </table>
      </div>
    </section>
  );
}

function RowName({ row, kind }: { row: UsageRow; kind: "model" | "chat" | "work" }) {
  let name: ReactNode = row.label;
  if (row.rest) {
    name = <span className="use-quiet">{row.label}</span>;
  } else if (kind === "chat" && row.key !== "") {
    name = <Link to={`/chats/${encodeURIComponent(row.key)}`}>{row.label}</Link>;
  } else if (kind === "chat") {
    name = <span className="use-quiet">{row.label}</span>;
  }
  const detail = kind === "model" && !row.rest ? [row.key !== row.label ? row.key : "", classWordsFor[row.class ?? ""] ?? ""].filter(Boolean).join(" · ") : classWords(row);
  return (
    <span className="use-name-text">
      <span className="use-name-main">{name}</span>
      {detail && <span className="set-hint">{detail}</span>}
    </span>
  );
}

type Choice = { kind: string; month: string; from: string; to: string };

function choiceOf(report: UsageReport): Choice {
  const p = report.period;
  return { kind: p.name, month: p.month ?? p.from.slice(0, 7), from: p.from, to: p.to };
}

function askOf(c: Choice): UsageAsk {
  if (c.kind === "month") return { month: c.month };
  if (c.kind === "range") return { from: c.from, to: c.to };
  return { period: c.kind };
}

/** Settings → Usage. */
export function UsageSettings({ onSignedOut }: { onSignedOut: () => void }) {
  const [ask, setAsk] = useState<UsageAsk>({});
  const [report, setReport] = useState<UsageReport | null>(null);
  const [state, setState] = useState<"loading" | "failed" | "ready">("loading");
  const [error, setError] = useState("");
  const [choice, setChoice] = useState<Choice | null>(null);
  const [turn, setTurn] = useState(0);

  useEffect(() => {
    let live = true;
    setState((s) => (s === "ready" ? s : "loading"));
    readUsage(ask).then(
      (r) => {
        if (!live) return;
        setReport(r);
        setChoice(choiceOf(r));
        setState("ready");
        setError("");
      },
      (e) => {
        if (!live) return;
        if (e instanceof ApiError && e.kind === "signed-out") onSignedOut();
        else if (e instanceof ApiError && e.status === 400 && e.said) setError(sentence(e.said));
        else if (report) setError("The report could not be read. Check that the server is running.");
        else setState("failed");
      },
    );
    return () => {
      live = false;
    };
    // Only a new ask, or a retry, reads again.
  }, [ask, turn]);

  if (state !== "ready" || !report || !choice) {
    return (
      <section className="set-section">
        <div className="set-section-head">
          <h2 className="set-heading">Usage</h2>
        </div>
        {state === "loading" ? (
          <p className="quiet" aria-busy="true">
            Loading.
          </p>
        ) : (
          <div className="set-stack">
            <p className="quiet">Your usage could not be read. Check that the server is running.</p>
            <div>
              <button type="button" className="button" onClick={() => setTurn((n) => n + 1)}>
                <Icon name="retry" size={16} />
                Try again
              </button>
            </div>
          </div>
        )}
      </section>
    );
  }

  function pick(kind: string) {
    if (!choice) return;
    const next = { ...choice, kind };
    setChoice(next);
    if (kind !== "range") setAsk(askOf(next));
  }

  function showRange(e: FormEvent) {
    e.preventDefault();
    if (choice) setAsk(askOf(choice));
  }

  const p = report.period;
  const thisMonth = report.cap.month === new Date().toISOString().slice(0, 7);
  return (
    <>
      <section className="set-section">
        <div className="set-section-head">
          <h2 className="set-heading">Usage</h2>
          <p className="set-note">
            What your model calls cost, summed from the record Keystone keeps of every call. Each table adds up to the total. Days are calendar days in UTC.
          </p>
        </div>
        <CapCard cap={report.cap} reserve={report.memory_reserve} thisMonth={thisMonth} />
      </section>

      <section className="set-section" aria-label="Period">
        <form className="use-picker" onSubmit={showRange}>
          <label className="field">
            <span className="field-label">Period</span>
            <select className="input" value={choice.kind} onChange={(e) => pick(e.target.value)}>
              {presets.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          </label>
          {choice.kind === "month" && (
            <label className="field">
              <span className="field-label">Month</span>
              <input
                className="input"
                type="month"
                value={choice.month}
                required
                onChange={(e) => {
                  const next = { ...choice, month: e.target.value };
                  setChoice(next);
                  if (/^\d{4}-\d{2}$/.test(e.target.value)) setAsk(askOf(next));
                }}
              />
            </label>
          )}
          {choice.kind === "range" && (
            <>
              <label className="field">
                <span className="field-label">From</span>
                <input className="input" type="date" value={choice.from} required onChange={(e) => setChoice({ ...choice, from: e.target.value })} />
              </label>
              <label className="field">
                <span className="field-label">To</span>
                <input className="input" type="date" value={choice.to} required onChange={(e) => setChoice({ ...choice, to: e.target.value })} />
              </label>
              <button type="submit" className="button">
                Show
              </button>
            </>
          )}
        </form>
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        <p className="use-period" aria-live="polite">
          {periodWords(p.from, p.to)}
        </p>
        <Totals report={report} />
        {report.total.calls === 0 && <p className="quiet">No model calls in this period.</p>}
      </section>

      <Breakdown
        title="By model"
        kind="model"
        rows={report.models}
        total={report.total}
        note={`Costliest first. Each model with its class when the calls were made. A breakdown shows ${report.rows} lines and adds up the rest in one more; usage.report_rows on General changes how many.`}
      />
      <Breakdown
        title="By chat"
        kind="chat"
        rows={report.chats}
        total={report.total}
        note="A background task is counted in its own chat. Calls in a chat you have deleted stay counted, on a line of their own."
      />
      <Breakdown
        title="By kind of work"
        kind="work"
        rows={report.kinds}
        total={report.total}
        note="Chat replies, titles, summaries and recaps, the memory pass, embeddings, and everything a background or scheduled task did."
      />
    </>
  );
}
