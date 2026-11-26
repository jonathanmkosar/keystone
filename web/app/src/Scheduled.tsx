// The Scheduled screen (P8-03): every scheduled task, with how its last run
// went and when it runs next; the chosen one with its switch, Run now,
// Edit, Delete, its facts, its prompt and its runs, each run a chat; Pause
// all; and the form that makes and changes one. From the draft mockup
// design/screens/Scheduled.html, in the tokens. docs/architecture/schedules.md
// has the server's side.

import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router";
import {
  ApiError,
  createSchedule,
  deleteSchedule,
  getSchedule,
  holdSchedules,
  listSchedules,
  listSettings,
  runScheduleNow,
  setScheduleEnabled,
  updateSchedule,
  type Schedule,
  type ScheduleReply,
  type ScheduleRun,
  type SchedulesReply,
} from "./api";
import { useChats } from "./chats";
import { Icon } from "./Icon";
import { ScheduleForm } from "./ScheduleForm";
import { dollars, lastSaid, open, pill, took, when, why, zoneWords, type Tone } from "./schedules";
import "./scheduled.css";

/** How often a screen with a run under way looks again. */
const watchEvery = 2000;

/** How often the screen looks again when nothing is under way. */
const lookEvery = 30000;
function failure(e: unknown, fallback: string): string {
  if (e instanceof ApiError) {
    if (e.kind === "signed-out") return "You are signed out. Sign in, then try again.";
    if (e.said && e.status >= 400 && e.status < 500 && e.status !== 404) {
      const s = e.said.charAt(0).toUpperCase() + e.said.slice(1);
      return /[.!?]$/.test(s) ? s : `${s}.`;
    }
    if (e.kind === "not-ready") return "The server did not answer. Check that it is running.";
  }
  return fallback;
}

function Pill({ label, tone }: { label: string; tone: Tone }) {
  return <span className={`sched-pill is-${tone}`}>{label}</span>;
}

function Row({ s, chosen, onChoose }: { s: Schedule; chosen: boolean; onChoose: () => void }) {
  const last = lastSaid(s.last_run);
  let next = "";
  if (!s.enabled) next = "paused by you";
  else if (s.held) next = "held";
  else if (s.next) next = `next: ${when(s.next)}`;
  else if (s.recurrence.kind === "once") next = "its time has passed";
  return (
    <button type="button" className={chosen ? "sched-row is-chosen" : "sched-row"} aria-current={chosen ? "true" : undefined} onClick={onChoose}>
      <span className="sched-row-name">{s.name}</span>
      <span className="sched-row-says">{s.says}</span>
      <span className="sched-row-foot">
        {!s.enabled ? <Pill label="Paused" tone="idle" /> : last ? <Pill label={last.label} tone={last.tone} /> : <Pill label="Not run yet" tone="idle" />}
        <span className="sched-mono">{next}</span>
      </span>
    </button>
  );
}

function RunRow({ r }: { r: ScheduleRun }) {
  const p = pill(r);
  const line = why(r);
  return (
    <tr>
      <td className="sched-when">
        {r.trigger === "now" ? (
          <>
            {when(r.started)}
            <div className="sched-small quiet">Run now</div>
          </>
        ) : (
          when(r.due)
        )}
      </td>
      <td>
        <Pill label={p.label} tone={p.tone} />
        {line && <div className="sched-why">{line}</div>}
      </td>
      <td className="sched-mono">{took(r.began, r.ended)}</td>
      <td className="sched-mono">{r.steps}</td>
      <td className="sched-mono">{dollars(r.spent_micros)}</td>
      <td>
        {r.chat_id && r.status !== "gone" && (
          <Link to={`/chats/${r.chat_id}`} className="sched-open">
            {r.waiting ? "Answer" : "Open"}
          </Link>
        )}
      </td>
    </tr>
  );
}

function Detail({
  reply,
  budget,
  onChanged,
  onEdit,
  onDeleted,
}: {
  reply: ScheduleReply;
  budget: string;
  onChanged: () => void;
  onEdit: () => void;
  onDeleted: () => void;
}) {
  const { schedule: s, runs, zone } = reply;
  const { refresh: refreshChats } = useChats();
  const [busy, setBusy] = useState("");
  const [problem, setProblem] = useState("");
  const [confirming, setConfirming] = useState(false);
  const underWay = runs.some(open);

  async function act(what: string, f: () => Promise<unknown>, fallback: string) {
    setBusy(what);
    setProblem("");
    try {
      await f();
      onChanged();
    } catch (e) {
      setProblem(failure(e, fallback));
    }
    setBusy("");
  }

  let next = "";
  if (!s.enabled) next = "Paused: it starts no run until you turn it on.";
  else if (s.held) next = "Held, with every schedule. Resume all to let it run.";
  else if (s.next) next = `Next run ${when(s.next)}.`;
  else if (s.recurrence.kind === "once") next = "Its time has passed.";

  return (
    <section className="sched-detail" aria-label={s.name}>
      <div className="sched-detail-head">
        <h2 className="sched-name">{s.name}</h2>
        <div className="sched-tools">
          <button
            type="button"
            role="switch"
            aria-checked={s.enabled}
            aria-label={s.enabled ? `${s.name} is on` : `${s.name} is paused`}
            className={s.enabled ? "set-switch is-on" : "set-switch"}
            disabled={busy !== ""}
            onClick={() => void act("switch", () => setScheduleEnabled(s.id, !s.enabled), "It did not change. Try again.")}
          >
            <span className="set-switch-knob" />
            <span className="set-switch-text">{s.enabled ? "On" : "Paused"}</span>
          </button>
          <button
            type="button"
            className="button"
            disabled={busy !== "" || underWay}
            title={underWay ? "A run is under way" : undefined}
            onClick={() =>
              void act(
                "run",
                async () => {
                  await runScheduleNow(s.id);
                  refreshChats();
                },
                "It did not start. Try again.",
              )
            }
          >
            {busy === "run" ? "Starting" : "Run now"}
          </button>
          <button type="button" className="button" disabled={busy !== ""} onClick={onEdit}>
            <Icon name="pencil" size={16} />
            Edit
          </button>
          <button type="button" className="icon-button" aria-label={`Delete ${s.name}`} title="Delete" disabled={busy !== ""} onClick={() => setConfirming(true)}>
            <Icon name="trash" />
          </button>
        </div>
      </div>
      <p className="sched-next" role="status">
        {next}
      </p>
      {confirming && (
        <div className="sched-confirm" role="alertdialog" aria-label="Delete this scheduled task">
          <p>Delete “{s.name}”? Its runs' chats stay in your chats. A run under way goes on.</p>
          <div className="sched-actions">
            <button
              type="button"
              className="button button-danger"
              disabled={busy !== ""}
              onClick={() =>
                void (async () => {
                  setBusy("delete");
                  try {
                    await deleteSchedule(s.id);
                    onDeleted();
                  } catch (e) {
                    setProblem(failure(e, "It was not deleted. Try again."));
                    setBusy("");
                  }
                })()
              }
            >
              Delete
            </button>
            <button type="button" className="button" onClick={() => setConfirming(false)}>
              Keep it
            </button>
          </div>
        </div>
      )}
      {problem && (
        <p className="form-error" role="alert">
          {problem}
        </p>
      )}
      <div className="sched-facts">
        <div>
          <p className="sched-over">SCHEDULE</p>
          <div>
            {s.says}, {zoneWords(zone)}
          </div>
        </div>
        <div>
          <p className="sched-over">MODEL</p>
          <div>{s.model || "The default for a new chat"}</div>
        </div>
        <div>
          <p className="sched-over">BUDGET</p>
          <div>{budget ? `$${budget} a run, your background task budget` : "Your background task budget, each run"}</div>
        </div>
        <div>
          <p className="sched-over">IF A RUN IS MISSED</p>
          <div>It runs once when Keystone is back, marked late</div>
        </div>
      </div>
      <div className="sched-prompt">{s.prompt}</div>
      {s.incognito && <p className="quiet sched-small">Incognito: no memory is kept, and each run's chat is deleted once you leave it or it sits idle.</p>}
      <div>
        <p className="sched-over">RUNS</p>
        {runs.length === 0 ? (
          <p className="quiet sched-small">No runs yet. Each run is a chat of its own, titled by the schedule and the date.</p>
        ) : (
          <div className="sched-table-wrap">
            <table className="sched-table">
              <thead>
                <tr>
                  <th>When</th>
                  <th>Result</th>
                  <th>Took</th>
                  <th>Steps</th>
                  <th>Cost</th>
                  <th>
                    <span className="sched-hide">Chat</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {runs.map((r) => (
                  <RunRow key={r.id} r={r} />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </section>
  );
}

type Loaded<T> = { state: "loading" } | { state: "failed"; why: string } | { state: "ready"; data: T };

export function Scheduled() {
  const [params, setParams] = useSearchParams();
  const chosen = params.get("s") ?? "";
  const [mode, setMode] = useState<"view" | "new" | "edit">("view");
  const [list, setList] = useState<Loaded<SchedulesReply>>({ state: "loading" });
  const [detail, setDetail] = useState<ScheduleReply | null>(null);
  const [budget, setBudget] = useState("");
  const [holding, setHolding] = useState(false);
  const [problem, setProblem] = useState("");
  const { refresh: refreshChats } = useChats();
  const live = useRef(true);

  const choose = useCallback(
    (id: string) => {
      setParams(id ? { s: id } : {}, { replace: true });
      setMode("view");
    },
    [setParams],
  );

  const refreshList = useCallback(async () => {
    try {
      const got = await listSchedules();
      if (live.current) setList({ state: "ready", data: got });
      return got;
    } catch (e) {
      if (live.current) setList({ state: "failed", why: failure(e, "The scheduled tasks could not be read.") });
      return null;
    }
  }, []);

  const refreshDetail = useCallback(async (id: string) => {
    if (!id) {
      setDetail(null);
      return;
    }
    try {
      const got = await getSchedule(id);
      if (live.current) setDetail(got);
    } catch (e) {
      if (live.current) {
        setDetail(null);
        if (!(e instanceof ApiError && e.kind === "missing")) setProblem(failure(e, "The scheduled task could not be read."));
      }
    }
  }, []);

  useEffect(() => {
    live.current = true;
    void refreshList();
    listSettings().then(
      ({ settings }) => {
        const b = settings.find((x) => x.key === "agents.task_budget_usd");
        if (live.current && b) setBudget(b.value);
      },
      () => {},
    );
    return () => {
      live.current = false;
    };
  }, [refreshList]);

  // The first schedule is chosen when none is, or the chosen one is gone.
  const schedules = list.state === "ready" ? list.data.schedules : [];
  useEffect(() => {
    if (list.state !== "ready" || mode === "new") return;
    if (schedules.length === 0) {
      if (chosen) choose("");
      return;
    }
    if (!schedules.some((s) => s.id === chosen)) choose(schedules[0].id);
  }, [list, schedules, chosen, choose, mode]);

  useEffect(() => {
    void refreshDetail(chosen);
  }, [chosen, refreshDetail]);

  // While a run is under way, the screen looks again until it ends.
  const watching = detail?.runs.some(open) || schedules.some((s) => s.last_run && open(s.last_run));
  useEffect(() => {
    if (!watching) return;
    const timer = window.setInterval(() => {
      void refreshList();
      void refreshDetail(chosen);
    }, watchEvery);
    return () => window.clearInterval(timer);
  }, [watching, chosen, refreshList, refreshDetail]);

  // When the soonest due time comes, the screen looks again, so a run that
  // starts shows; and every so often besides, for a change made elsewhere.
  const soonest = schedules.reduce((at, s) => {
    const t = s.next ? Date.parse(s.next) : NaN;
    return Number.isFinite(t) && (at === 0 || t < at) ? t : at;
  }, 0);
  useEffect(() => {
    const wait = soonest > 0 ? Math.min(Math.max(soonest - Date.now() + 3000, 1000), lookEvery) : lookEvery;
    const timer = window.setTimeout(() => {
      void refreshList();
      void refreshDetail(chosen);
    }, wait);
    return () => window.clearTimeout(timer);
  }, [soonest, chosen, list, refreshList, refreshDetail]);

  async function changed() {
    await Promise.all([refreshList(), refreshDetail(chosen)]);
    refreshChats();
  }

  async function hold(on: boolean) {
    setHolding(true);
    setProblem("");
    try {
      const got = await holdSchedules(on);
      setList({ state: "ready", data: got });
      await refreshDetail(chosen);
    } catch (e) {
      setProblem(failure(e, "The switch did not change. Try again."));
    }
    setHolding(false);
  }

  const paused = list.state === "ready" && list.data.paused;
  const zone = list.state === "ready" ? list.data.zone : undefined;
  const full = list.state === "ready" && list.data.schedules.length >= list.data.max;

  return (
    <div className="sched">
      <header className="sched-head">
        <h1 className="page-title">Scheduled</h1>
        <div className="sched-actions">
          {list.state === "ready" && list.data.schedules.length > 0 && (
            <button type="button" className="button" disabled={holding} onClick={() => void hold(!paused)}>
              {paused ? "Resume all" : "Pause all"}
            </button>
          )}
          <button type="button" className="button button-primary" disabled={full} title={full ? "You have the most scheduled tasks one person may have" : undefined} onClick={() => setMode("new")}>
            <Icon name="plus" size={16} />
            New scheduled task
          </button>
        </div>
      </header>
      {zone && (
        <p className="quiet sched-zone">
          Times are in {zoneWords(zone)}. <Link to="/settings/general">Change it on General</Link>, under Agents.
          {zone.problem && <span className="sched-zone-problem"> {zone.problem}</span>}
        </p>
      )}
      {paused && (
        <p className="sched-held" role="status">
          Every scheduled task is held: none starts a run until you resume them, and the times that pass meanwhile are not made up. A run under way goes on, and Run now still runs one.
        </p>
      )}
      {problem && (
        <p className="form-error sched-zone" role="alert">
          {problem}
        </p>
      )}
      {list.state === "loading" && (
        <p className="quiet sched-zone" aria-busy="true">
          Loading.
        </p>
      )}
      {list.state === "failed" && (
        <div className="sched-zone">
          <p className="quiet">{list.why}</p>
          <button type="button" className="button" onClick={() => void refreshList()}>
            <Icon name="retry" size={16} />
            Try again
          </button>
        </div>
      )}
      {list.state === "ready" && (
        <div className="sched-body">
          {schedules.length > 0 && (
            <section className="sched-list" aria-label="Scheduled tasks">
              {schedules.map((s) => (
                <Row key={s.id} s={s} chosen={mode !== "new" && s.id === chosen} onChoose={() => choose(s.id)} />
              ))}
            </section>
          )}
          <div className="sched-main">
            {schedules.length === 0 && mode === "view" && (
              <p className="quiet">
                No scheduled tasks yet. A scheduled task sends a prompt at the times you choose, in a chat of its own each time, so its runs are chats you can open. Make one with New scheduled task.
              </p>
            )}
            {mode === "new" && (
              <ScheduleForm
                zoneLabel={zoneWords(zone)}
                save={createSchedule}
                onCancel={() => setMode("view")}
                onSaved={(s) => {
                  setMode("view");
                  void refreshList().then(() => choose(s.id));
                }}
              />
            )}
            {mode === "edit" && detail && (
              <ScheduleForm
                from={detail.schedule}
                zoneLabel={zoneWords(zone)}
                save={(body) => updateSchedule(detail.schedule.id, body)}
                onCancel={() => setMode("view")}
                onSaved={() => {
                  setMode("view");
                  void changed();
                }}
              />
            )}
            {mode === "view" && detail && (
              <Detail
                reply={detail}
                budget={budget}
                onChanged={() => void changed()}
                onEdit={() => setMode("edit")}
                onDeleted={() => {
                  setDetail(null);
                  choose("");
                  void refreshList();
                }}
              />
            )}
          </div>
        </div>
      )}
    </div>
  );
}
