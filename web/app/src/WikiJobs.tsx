// The wiki upkeep jobs (P8-06), a section of the Scheduled screen: each job
// with its state, its last and next run, what Keystone wrote for it, its
// switch and Run now. Built from the design tokens after
// design/screens/Scheduled.html; there is no mockup for it yet (P1-05).
// docs/architecture/wiki-jobs.md has the server's side.

import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router";
import { ApiError, call, type ScheduleRun } from "./api";
import { lastSaid, open, when, type Tone } from "./schedules";
import "./wiki-jobs.css";

type Written = { what: string; id: string; detail: string };

/** One wiki job as the server says it stands. */
export type WikiJob = {
  name: string;
  title: string;
  store: string;
  secret?: string;
  principal?: string;
  grant?: string;
  on: boolean;
  problem?: string;
  when: string;
  says?: string;
  model?: string;
  budget_usd: number;
  has_token: boolean;
  schedule_id?: string;
  held: boolean;
  next?: string;
  last_run?: ScheduleRun;
  last_written?: { run_id: string; state: string; summary: string; written: Written[]; at: string };
};

const base = "/api/v1/wiki-jobs";

async function listJobs(): Promise<WikiJob[]> {
  const got = (await call("GET", base)) as { jobs?: WikiJob[] } | undefined;
  if (!got || !Array.isArray(got.jobs)) throw new ApiError("not-ready", 200);
  return got.jobs;
}

function failure(e: unknown, fallback: string): string {
  if (e instanceof ApiError) {
    if (e.kind === "signed-out") return "You are signed out. Sign in, then try again.";
    if (e.said && e.status >= 400 && e.status < 500) {
      const s = e.said.charAt(0).toUpperCase() + e.said.slice(1);
      return /[.!?]$/.test(s) ? s : `${s}.`;
    }
  }
  return fallback;
}

function state(j: WikiJob): { label: string; tone: Tone } {
  if (j.problem) return { label: "Cannot run", tone: j.on ? "bad" : "idle" };
  if (!j.on) return { label: "Off", tone: "idle" };
  if (j.held) return { label: "Held", tone: "warn" };
  return { label: "On", tone: "ok" };
}

function written(j: WikiJob): string {
  const w = j.last_written;
  if (!w) return "";
  switch (w.state) {
    case "applying":
      return "Writing what its last run asked for.";
    case "applied": {
      const pages = w.written.filter((x) => x.what === "page").length;
      const issues = w.written.filter((x) => x.what === "raise" || x.what === "note" || x.what === "close").length;
      if (pages === 0 && issues === 0) return "Its last run changed nothing.";
      return `Its last run wrote ${pages} ${pages === 1 ? "page" : "pages"}${issues ? ` and ${issues} ${issues === 1 ? "issue change" : "issue changes"}` : ""}.`;
    }
    case "refused":
      return "Its last run's answer asked for more than its grant, so nothing was written.";
    case "failed":
      return "A write of its last run was refused; what came before it stays.";
    case "nothing":
      return "Its last run did not finish, so nothing was written.";
  }
  return "";
}

function Job({ j, onChanged }: { j: WikiJob; onChanged: () => void }) {
  const [busy, setBusy] = useState("");
  const [problem, setProblem] = useState("");
  const s = state(j);
  const last = lastSaid(j.last_run);
  const underWay = j.last_run ? open(j.last_run) : false;

  async function act(what: string, path: string, fallback: string) {
    setBusy(what);
    setProblem("");
    try {
      await call("POST", `${base}/${j.name}/${path}`);
      onChanged();
    } catch (e) {
      setProblem(failure(e, fallback));
    }
    setBusy("");
  }

  let next = "";
  if (j.problem) next = "";
  else if (!j.on) next = "Off: it runs only when you run it now.";
  else if (j.held) next = "Held, with every schedule.";
  else if (j.next) next = `Next run ${when(j.next)}.`;

  return (
    <li className="wiki-job">
      <div className="wiki-job-head">
        <div className="wiki-job-name">
          <span className="wiki-job-title">{j.title}</span>
          <span className="wiki-job-facts sched-mono">
            {j.store === "waymarks" ? `Waymarks, as ${j.principal}` : "The built-in store"} · {j.says ?? j.when}
            {j.model ? ` · ${j.model}` : ""} · ${j.budget_usd.toFixed(2)} a run
          </span>
        </div>
        <div className="wiki-job-tools">
          <span className={`sched-pill is-${s.tone}`}>{s.label}</span>
          <button
            type="button"
            role="switch"
            aria-checked={j.on}
            aria-label={j.on ? `${j.title} is on` : `${j.title} is off`}
            className={j.on ? "set-switch is-on" : "set-switch"}
            disabled={busy !== ""}
            onClick={() => void act("switch", j.on ? "off" : "on", "It did not change. Try again.")}
          >
            <span className="set-switch-knob" />
            <span className="set-switch-text">{j.on ? "On" : "Off"}</span>
          </button>
          <button
            type="button"
            className="button"
            disabled={busy !== "" || underWay || Boolean(j.problem)}
            title={j.problem ? "It cannot run yet" : underWay ? "A run is under way" : undefined}
            onClick={() => void act("run", "run", "It did not start. Try again.")}
          >
            {busy === "run" ? "Starting" : "Run now"}
          </button>
        </div>
      </div>
      {j.problem && (
        <p className="wiki-job-problem">
          It cannot run: {j.problem}.{" "}
          {j.secret && !j.has_token && j.problem.includes(j.secret) && (
            <>
              Make its principal in Waymarks with the grant <code>{j.grant}</code>, and store its token as <code>{j.secret}</code>: the steps are in docs/runbooks/wiki-jobs.md.
            </>
          )}
        </p>
      )}
      {(next || last || j.last_written) && (
        <p className="wiki-job-line quiet">
          {last && j.last_run && (
            <>
              Last run {when(j.last_run.started)}: <span className={`sched-pill is-${last.tone}`}>{last.label}</span>{" "}
              {j.last_run.chat_id && j.last_run.status !== "gone" && (
                <Link to={`/chats/${j.last_run.chat_id}`} className="sched-open">
                  Open
                </Link>
              )}{" "}
            </>
          )}
          {written(j)} {next}
        </p>
      )}
      {problem && (
        <p className="form-error" role="alert">
          {problem}
        </p>
      )}
    </li>
  );
}

/**
 * The section. `onChange` is told after a job changed here, so the list of
 * schedules above it, which holds each job's schedule, is read again.
 */
export function WikiJobs({ onChange }: { onChange?: () => void }) {
  const [jobs, setJobs] = useState<WikiJob[] | null>(null);
  const [problem, setProblem] = useState("");

  const refresh = useCallback(async () => {
    try {
      setJobs(await listJobs());
      setProblem("");
    } catch (e) {
      setProblem(failure(e, "The wiki upkeep jobs could not be read."));
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // While a run is under way, or its answer is being written, look again.
  const watching = jobs?.some((j) => (j.last_run && open(j.last_run)) || j.last_written?.state === "applying") ?? false;
  useEffect(() => {
    const timer = window.setInterval(() => void refresh(), watching ? 2000 : 30000);
    return () => window.clearInterval(timer);
  }, [watching, refresh]);

  return (
    <section className="wiki-jobs" aria-label="Wiki upkeep">
      <h2 className="wiki-jobs-title">Wiki upkeep</h2>
      <p className="quiet wiki-jobs-words">
        The memory pass adds what you tell Keystone to your knowledge store as sources. These jobs, from Waymarks' agent kit, file them into pages that cite them. Each runs on a local or trusted model only, with a token and a grant of its own, and writes only to your knowledge store. All are off until you switch them on; the daily round (upkeep) does the work of the other three.
      </p>
      {problem && (
        <p className="form-error" role="alert">
          {problem}
        </p>
      )}
      {jobs && (
        <ul className="wiki-jobs-list">
          {jobs.map((j) => (
            <Job
              key={j.name}
              j={j}
              onChanged={() => {
                void refresh();
                onChange?.();
              }}
            />
          ))}
        </ul>
      )}
    </section>
  );
}
