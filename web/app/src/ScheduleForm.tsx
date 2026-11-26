// The form that makes or changes a scheduled task (P8-03): its name, its
// prompt, how often and when, the model its runs use, and incognito. The
// server checks every field again and says what it refused.

import { useEffect, useState, type FormEvent } from "react";
import { ApiError, listModels, type Recurrence, type Schedule, type ScheduleBody } from "./api";
import { dayNames } from "./schedules";

type Kind = Recurrence["kind"];

const kinds: { kind: Kind; label: string }[] = [
  { kind: "daily", label: "Every day" },
  { kind: "weekdays", label: "Weekdays, Monday to Friday" },
  { kind: "weekly", label: "On chosen days of the week" },
  { kind: "hourly", label: "Every hour" },
  { kind: "once", label: "Once" },
];

function today(): string {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

/** What a refused save is told to the person as. */
function refusal(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.kind === "signed-out") return "You are signed out. Sign in, then save again.";
    if (e.said && e.status >= 400 && e.status < 500) {
      const s = e.said.replace(/^the schedule is refused: /, "");
      const words = s.charAt(0).toUpperCase() + s.slice(1);
      return /[.!?]$/.test(words) ? words : `${words}.`;
    }
    if (e.kind === "not-ready") return "The server did not answer. Check that it is running.";
  }
  return "It was not saved. Try again.";
}

export function ScheduleForm({
  from,
  zoneLabel,
  onSaved,
  onCancel,
  save,
}: {
  /** The schedule being changed, or nothing for a new one. */
  from?: Schedule;
  zoneLabel: string;
  onSaved: (s: Schedule) => void;
  onCancel: () => void;
  save: (body: ScheduleBody) => Promise<Schedule>;
}) {
  const r = from?.recurrence;
  const [name, setName] = useState(from?.name ?? "");
  const [prompt, setPrompt] = useState(from?.prompt ?? "");
  const [kind, setKind] = useState<Kind>(r?.kind ?? "daily");
  const [time, setTime] = useState(r?.time ?? "07:00");
  const [minute, setMinute] = useState(String(r?.minute ?? 0));
  const [days, setDays] = useState<string[]>(r?.days ?? ["mon"]);
  const [date, setDate] = useState(r?.date ?? today());
  const [model, setModel] = useState(from?.model ?? "");
  const [incognito, setIncognito] = useState(from?.incognito ?? false);
  // Each run's budget in dollars (P8-05); empty is the person's agents.schedule_budget_usd.
  const [budget, setBudget] = useState(from?.budget_usd !== undefined ? String(from.budget_usd) : "");
  const [models, setModels] = useState<{ name: string; label: string }[]>([]);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState("");

  useEffect(() => {
    let live = true;
    listModels().then(
      (reg) => {
        if (!live) return;
        setModels(reg.models.filter((m) => !m.problem).map((m) => ({ name: m.entry.name, label: m.entry.label || m.entry.name })));
      },
      () => {
        // With no list, the default model still works.
      },
    );
    return () => {
      live = false;
    };
  }, []);

  function toggleDay(key: string) {
    setDays((d) => (d.includes(key) ? d.filter((x) => x !== key) : [...d, key]));
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    setProblem("");
    const recurrence: Recurrence = { kind };
    if (kind === "hourly") recurrence.minute = Number(minute) || 0;
    else recurrence.time = time;
    if (kind === "weekly") recurrence.days = dayNames.map((d) => d.key).filter((k) => days.includes(k));
    if (kind === "once") recurrence.date = date;
    const body: ScheduleBody = { name, prompt, recurrence, incognito };
    if (model) body.model = model;
    if (budget.trim() !== "") {
      const usd = Number(budget);
      if (!Number.isFinite(usd) || usd < 0) {
        setProblem("A budget is a number of US dollars, 0 or more, or empty for your budget for scheduled runs.");
        return;
      }
      body.budget_usd = usd;
    }
    setBusy(true);
    try {
      onSaved(await save(body));
    } catch (err) {
      setProblem(refusal(err));
      setBusy(false);
    }
  }

  const known = model === "" || models.some((m) => m.name === model);
  return (
    <form className="sched-form" onSubmit={(e) => void submit(e)} aria-label={from ? `Change ${from.name}` : "New scheduled task"}>
      <h2 className="sched-name">{from ? `Change “${from.name}”` : "New scheduled task"}</h2>
      <label className="field">
        <span className="field-label">Name</span>
        <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="Morning brief" required />
      </label>
      <label className="field">
        <span className="field-label">Prompt: the message each run sends, in a new chat</span>
        <textarea
          className="input sched-prompt-input"
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          rows={5}
          placeholder="Tell me what needs me today."
          required
        />
      </label>
      <div className="sched-form-row">
        <label className="field">
          <span className="field-label">How often</span>
          <select className="input" value={kind} onChange={(e) => setKind(e.target.value as Kind)}>
            {kinds.map((k) => (
              <option key={k.kind} value={k.kind}>
                {k.label}
              </option>
            ))}
          </select>
        </label>
        {kind === "hourly" ? (
          <label className="field">
            <span className="field-label">Minutes past the hour</span>
            <input className="input sched-short" type="number" min={0} max={59} value={minute} onChange={(e) => setMinute(e.target.value)} />
          </label>
        ) : (
          <label className="field">
            <span className="field-label">At</span>
            <input className="input sched-short" type="time" value={time} onChange={(e) => setTime(e.target.value)} required />
          </label>
        )}
        {kind === "once" && (
          <label className="field">
            <span className="field-label">On</span>
            <input className="input" type="date" value={date} onChange={(e) => setDate(e.target.value)} required />
          </label>
        )}
      </div>
      {kind === "weekly" && (
        <fieldset className="sched-days">
          <legend className="field-label">On these days</legend>
          {dayNames.map((d) => (
            <button
              key={d.key}
              type="button"
              className={days.includes(d.key) ? "sched-day is-on" : "sched-day"}
              aria-pressed={days.includes(d.key)}
              onClick={() => toggleDay(d.key)}
            >
              {d.short}
            </button>
          ))}
        </fieldset>
      )}
      <p className="quiet sched-small">Times are in {zoneLabel}. A time that has passed today runs next time, not now.</p>
      <label className="field">
        <span className="field-label">Model</span>
        <select className="input" value={model} onChange={(e) => setModel(e.target.value)}>
          <option value="">The default for a new chat</option>
          {models.map((m) => (
            <option key={m.name} value={m.name}>
              {m.label}
            </option>
          ))}
          {!known && <option value={model}>{model}</option>}
        </select>
      </label>
      <label className="field">
        <span className="field-label">Budget for each run, in US dollars</span>
        <input
          className="input sched-short"
          type="number"
          min={0}
          step="0.01"
          inputMode="decimal"
          value={budget}
          onChange={(e) => setBudget(e.target.value)}
          placeholder="Yours"
          aria-describedby="sched-budget-hint"
        />
        <span id="sched-budget-hint" className="quiet sched-small">
          Empty uses your budget for scheduled runs, on General. A call that could pass it is not sent: the run goes on with a local model in your chain, or stops and says so.
        </span>
      </label>
      <label className="sched-check">
        <input type="checkbox" checked={incognito} onChange={(e) => setIncognito(e.target.checked)} />
        Incognito: no memory is kept from its runs
      </label>
      {problem && (
        <p className="form-error" role="alert">
          {problem}
        </p>
      )}
      <div className="sched-actions">
        <button type="submit" className="button button-primary" disabled={busy}>
          {busy ? "Saving" : from ? "Save changes" : "Make it"}
        </button>
        <button type="button" className="button" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
      </div>
    </form>
  );
}
