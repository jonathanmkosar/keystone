// Sub-agents' lanes in the chat (P8-02). A run_subagents step is drawn as
// the approved row of design/screens/MainDark.html, "Ran 3 sub-agents in
// parallel", and under it a lane for each sub-agent: its place and title,
// its state, where it is, its spend against its budget, its steps as they
// happen, its result, Stop, a link to its own chat, and its approval card
// when a call of its waits for the person.
//
// Provisional: the lanes have no mockup (the agent mockups, P1-05, are not
// approved). They are built from the design tokens and the step rows and
// approval card around them, and say so in sub-agents.css.
//
// The lanes are read from the server while the step runs or a sub-agent is
// under way, once a second, so they are the same after a reload: the rows
// are kept, and the steps are the sub-agent's own chat's.
// docs/architecture/sub-agents.md has the server's side.

import { useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router";
import { answerApproval, ApiError, openChat, type Approval, type Message } from "./api";
import { Icon, type IconName } from "./Icon";
import { layout } from "./layout";
import { CopyButton, Markdown } from "./Markdown";
import { errorLabel, toolLine } from "./steps";
import { dollars, listSubagents, stopSubagent, subagentApprovals, underWay, type Subagent } from "./subagentCalls";
import "./subagents.css";

type ToolStep = { id: string; name: string; input: string; running: boolean; result?: string; failed?: boolean };

/** Whether a tool step is drawn as sub-agents' lanes. */
export function isSubagentsStep(step: { name: string }): boolean {
  return step.name === "run_subagents";
}

/** How many sub-agents a call asked for, from its input, before the server lists them. */
function asked(input: string): number {
  try {
    const v = JSON.parse(input || "{}") as { agents?: unknown };
    return Array.isArray(v.agents) ? v.agents.length : 0;
  } catch {
    return 0;
  }
}

function plural(n: number, one: string, many: string): string {
  return n === 1 ? `${n} ${one}` : `${n} ${many}`;
}

/** The step's row and its lanes. */
export function SubagentLanes({ step }: { step: ToolStep }) {
  const { id: chatId = "" } = useParams();
  const [subs, setSubs] = useState<Subagent[]>([]);
  const [open, setOpen] = useState(false);
  const running = useRef(step.running);
  running.current = step.running;

  useEffect(() => {
    if (chatId === "" || step.id === "") return;
    let live = true;
    let timer: number | undefined;
    const look = async () => {
      let more = false;
      try {
        const got = await listSubagents(chatId, step.id);
        if (!live) return;
        setSubs(got);
        more = got.some(underWay);
      } catch {
        // The chat screen says what is wrong with the chat.
      }
      if (live && (more || running.current)) timer = window.setTimeout(() => void look(), 1000);
    };
    void look();
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
  }, [chatId, step.id, step.running]);

  const n = subs.length || asked(step.input);
  const ended = subs.filter((s) => !underWay(s)).length;
  const working = step.running || subs.some(underWay);
  const many = n === 1 ? "a sub-agent" : `${n} sub-agents in parallel`;
  // A call that started none: the tool refused it, and says why.
  if (subs.length === 0 && !step.running && step.result !== undefined) {
    // The tool's own refusals (the switch, the limits, no nesting) are Keystone choosing, not the tool failing.
    const refused = /^(No sub-agent was started|A sub-agent cannot start)/.test(step.result);
    const error = refused ? { text: "none started", broke: false } : step.failed ? errorLabel(step.result) : undefined;
    return (
      <>
        <button type="button" className="step step-tool step-button" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          <Icon name="agents" size={16} />
          <span className="step-label">{`Run ${many}`}</span>
          {error ? <span className={error.broke ? "step-tag step-tag-error" : "step-tag"}>{error.text}</span> : <span className="step-meta">none started</span>}
          <span className={open ? "step-caret is-open" : "step-caret"}>
            <Icon name="caret" size={16} />
          </span>
        </button>
        {open && (
          <div className="step-body">
            <div className="step-heading">Came back</div>
            <pre className="step-pre">{step.result}</pre>
          </div>
        )}
      </>
    );
  }
  const label = working ? `Running ${many}` : `Ran ${many}`;
  const meta = n === 0 ? "" : working ? `${ended} of ${n} done` : summary(subs);
  return (
    <div className="subagents">
      <div className={working ? "step step-tool is-running" : "step step-tool"} role={working ? "status" : undefined}>
        <Icon name="agents" size={16} />
        <span className="step-label">{label}</span>
        {meta !== "" && <span className="step-meta">{meta}</span>}
      </div>
      {subs.length > 0 && (
        <ol className="lanes" aria-label="Sub-agents">
          {subs.map((s) => (
            <Lane key={s.id} chatId={chatId} sub={s} onChange={(next) => setSubs((all) => all.map((x) => (x.id === next.id ? next : x)))} />
          ))}
        </ol>
      )}
    </div>
  );
}

/** A few words for how a call's sub-agents ended, on the right of its row. */
function summary(subs: Subagent[]): string {
  const done = subs.filter((s) => s.status === "done").length;
  if (done === subs.length) return "all done";
  const parts = [`${done} done`];
  const other = subs.length - done;
  if (other > 0) parts.push(`${other} not`);
  return parts.join(", ");
}

/** A sub-agent's state, in a word or two, and how it is drawn. */
function stateOf(s: Subagent): { text: string; tone: string } {
  if (s.waiting) return { text: "Waiting for you", tone: "is-waiting" };
  switch (s.status) {
    case "queued":
      return { text: "Waiting for a lane", tone: "" };
    case "running":
      return { text: "Working", tone: "is-working" };
    case "done":
      return { text: "Done", tone: "is-done" };
    case "stopped":
      return { text: "Stopped", tone: "" };
    case "paused":
      return { text: "Paused", tone: "is-paused" };
  }
  return { text: "Did not finish", tone: "is-failed" };
}

/** The first lines of a result, for the lane while it is closed. */
function preview(text: string): string {
  const one = text.replace(/\s+/g, " ").trim();
  return one.length > 220 ? one.slice(0, 219).trimEnd() + "…" : one;
}

function Lane({ chatId, sub, onChange }: { chatId: string; sub: Subagent; onChange: (s: Subagent) => void }) {
  const [open, setOpen] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [problem, setProblem] = useState("");
  const state = stateOf(sub);
  const working = underWay(sub);
  // An ended lane's state says how it ended, so its line gives only the facts.
  const where = [working ? sub.progress : "", sub.steps > 0 ? plural(sub.steps, "step", "steps") : "", `${dollars(sub.spent_micros)} of ${dollars(sub.budget_micros)}`].filter(Boolean).join(" · ");

  async function stop() {
    setStopping(true);
    setProblem("");
    try {
      onChange(await stopSubagent(chatId, sub.id));
    } catch (e) {
      setProblem(e instanceof ApiError && e.kind === "signed-out" ? "You are signed out. Sign in, then stop it again." : "The stop did not reach Keystone. Try again.");
    } finally {
      setStopping(false);
    }
  }

  return (
    <li className={["lane", state.tone].filter(Boolean).join(" ")}>
      <div className="lane-head">
        <span className="lane-place" aria-hidden="true">
          {sub.position}
        </span>
        <span className="lane-title">{sub.title}</span>
        <span className="lane-state">{state.text}</span>
      </div>
      <p className={working && !sub.waiting ? "lane-where is-running" : "lane-where"}>{where}</p>
      {sub.error && <p className="lane-error">{sub.error.message}</p>}
      {sub.notice && <p className="lane-note">{sub.notice}</p>}
      {!working && !sub.passed && sub.sensitive && <p className="lane-note">It was sent content from a sensitive space, so its result stayed in its own chat and was not passed to this one.</p>}
      {sub.waiting && <LaneApprovals sub={sub} />}
      {!open && sub.result && <p className="lane-preview">{preview(sub.result)}</p>}
      {open && <LaneSteps sub={sub} />}
      {open && sub.result && (
        <div className="lane-result">
          <div className="step-heading">{sub.status === "done" ? "Its result" : "What it had written"}</div>
          <div className="reply-text">
            <Markdown text={sub.result} />
          </div>
          <div className="reply-actions">
            <CopyButton text={sub.result} />
          </div>
        </div>
      )}
      <div className="lane-actions">
        <button type="button" className="lane-toggle" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          {open ? "Hide its steps" : "Show its steps"}
        </button>
        <Link className="lane-link" to={`/chats/${sub.work_chat_id}`}>
          Open its chat
        </Link>
        {working && (
          <button type="button" className="lane-stop" disabled={stopping} onClick={() => void stop()}>
            <Icon name="stop" size={14} />
            {stopping ? "Stopping" : "Stop"}
          </button>
        )}
      </div>
      {problem && (
        <p className="lane-error" role="alert">
          {problem}
        </p>
      )}
    </li>
  );
}

/** A sub-agent's steps, from its own chat, read again as it moves on. */
function LaneSteps({ sub }: { sub: Subagent }) {
  const [record, setRecord] = useState<Message[] | undefined>(undefined);
  const [problem, setProblem] = useState("");
  const working = underWay(sub);

  useEffect(() => {
    let live = true;
    openChat(sub.work_chat_id).then(
      (got) => {
        if (live) {
          setRecord(got.messages);
          setProblem("");
        }
      },
      () => live && setProblem("Its chat could not be read."),
    );
    return () => {
      live = false;
    };
  }, [sub.work_chat_id, sub.steps, sub.status, sub.waiting, sub.progress]);

  if (problem) return <p className="lane-error">{problem}</p>;
  if (!record) return <p className="lane-where">Reading its steps</p>;
  const rows: { key: string; icon: IconName; text: string; meta: string; tag?: { text: string; broke: boolean }; running: boolean }[] = [];
  for (const item of layout(record, undefined)) {
    if (item.kind !== "reply") continue;
    for (const part of item.parts) {
      if (part.kind === "limit") rows.push({ key: item.key + "limit", icon: "stop", text: "Reached its step limit", meta: "", running: false });
      if (part.kind !== "steps") continue;
      part.steps.forEach((s, i) => {
        if (s.kind === "thinking") {
          rows.push({ key: `${item.key}t${i}`, icon: "caret", text: "Thought", meta: "", running: false });
          return;
        }
        const line = toolLine(s.name, s.input, s.result);
        if (s.result === undefined) {
          rows.push({ key: s.id || `${item.key}${i}`, icon: line.icon, text: working ? line.running : line.asked, meta: working ? "running" : "", running: working });
        } else if (s.failed) {
          rows.push({ key: s.id || `${item.key}${i}`, icon: line.icon, text: line.asked, meta: "", tag: errorLabel(s.result), running: false });
        } else {
          rows.push({ key: s.id || `${item.key}${i}`, icon: line.icon, text: line.done, meta: line.meta, running: false });
        }
      });
    }
  }
  if (rows.length === 0) return <p className="lane-where">{working ? "No steps yet: it is writing its answer." : "It answered without a step."}</p>;
  return (
    <ul className="lane-steps">
      {rows.map((r) => (
        <li key={r.key} className={r.running ? "step step-tool is-running" : "step step-tool"}>
          <Icon name={r.icon} size={16} />
          <span className="step-label">{r.text}</span>
          {r.tag ? <span className={r.tag.broke ? "step-tag step-tag-error" : "step-tag"}>{r.tag.text}</span> : r.meta !== "" && <span className="step-meta">{r.meta}</span>}
        </li>
      ))}
    </ul>
  );
}

/** The card of each call of a sub-agent's turn that waits for the person, answered here in the chat it was started from. */
function LaneApprovals({ sub }: { sub: Subagent }) {
  const [list, setList] = useState<Approval[]>([]);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState("");

  useEffect(() => {
    let live = true;
    subagentApprovals(sub).then(
      (got) => live && setList(got.filter((a) => a.status === "pending")),
      () => {},
    );
    return () => {
      live = false;
    };
  }, [sub.id, sub.work_turn_id, sub.progress]);

  async function answer(a: Approval, how: "approve" | "decline") {
    setBusy(true);
    setProblem("");
    try {
      await answerApproval(a, how);
      setList((all) => all.filter((x) => x.id !== a.id));
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) setProblem("This call was already answered, or the sub-agent has ended.");
      else setProblem("The answer did not reach Keystone. Try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      {list.map((a) => (
        <section key={a.id} className="approval lane-approval" aria-label="Approval needed">
          <div className="approval-head">
            <span className="approval-title">{toolLine(a.tool, JSON.stringify(a.input ?? {}), undefined).asked}</span>
            <span className="approval-why">{a.why}</span>
          </div>
          <p className="approval-summary">{a.summary}</p>
          <div className="approval-actions">
            <button type="button" className="button button-primary approval-button" disabled={busy} onClick={() => void answer(a, "approve")}>
              Approve
            </button>
            <button type="button" className="approval-later" disabled={busy} onClick={() => void answer(a, "decline")}>
              Not now
            </button>
          </div>
        </section>
      ))}
      {problem && (
        <p className="lane-error" role="alert">
          {problem}
        </p>
      )}
    </>
  );
}
