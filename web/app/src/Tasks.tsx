// Background tasks in the chat (P8-01): a card for each task that is under
// way, started from this chat or working in it, with where it is, a link to
// the other chat, and Stop; and the note a task writes when it ends, drawn
// as a reply of its own with the task's mark. Built from the design tokens:
// there is no mockup for either yet (P1-05).
//
// A card follows its task's stream, which sends the task whenever it
// changes. When the task ends the chat is read again, and the note is in
// the record. The tasks a page has seen are kept here, by chat, so a note
// can link to the task's own chat. docs/architecture/jobs.md has the
// server's side.

import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { Link } from "react-router";
import { ApiError, followTask, listTasks, stopTask, type Task } from "./api";
import { Icon } from "./Icon";
import { CopyButton, Markdown } from "./Markdown";
import { onEnd } from "./turns";
import "./tasks.css";

const byChat = new Map<string, Task[]>();
const listeners = new Set<() => void>();
const none: Task[] = [];

function keep(chatId: string, tasks: Task[]) {
  byChat.set(chatId, tasks);
  for (const l of listeners) l();
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

function useKept(chatId: string): Task[] {
  return useSyncExternalStore(subscribe, () => byChat.get(chatId) ?? none);
}

/** A task has not ended, or has and its note is not yet written. */
function underWay(t: Task): boolean {
  return t.status === "queued" || t.status === "running" || !t.note_message_id;
}

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((done) => {
    const timer = window.setTimeout(done, ms);
    signal.addEventListener("abort", () => {
      window.clearTimeout(timer);
      done();
    });
  });
}

/** Follows one task's stream until it ends, coming back after a dropped connection. */
async function follow(chatId: string, task: Task, signal: AbortSignal, onTask: (t: Task, end: boolean) => void): Promise<void> {
  let last = "";
  while (!signal.aborted) {
    try {
      const ended = await followTask(chatId, task.id, last, (t, id, end) => {
        last = id;
        onTask(t, end);
      }, signal);
      if (ended) return;
    } catch (e) {
      if (e instanceof ApiError && (e.kind === "missing" || e.kind === "signed-out")) return;
    }
    await sleep(1500, signal);
  }
}

function where(t: Task, own: boolean): string {
  if (t.waiting) return own ? "Waiting for your approval, above" : "Waiting for your approval, in its chat";
  if (t.status === "queued") return "Waiting to start";
  if (t.status !== "running") return "Writing its result in this chat";
  const done = t.steps === 0 ? "" : t.steps === 1 ? " · 1 step done" : ` · ${t.steps} steps done`;
  return `${t.progress || "Working"}${done}`;
}

function TaskCard({ chatId, task }: { chatId: string; task: Task }) {
  const [stopping, setStopping] = useState(false);
  const [problem, setProblem] = useState("");
  const own = chatId === task.work_chat_id;
  const running = task.status === "running" && !task.waiting;

  async function stop() {
    setStopping(true);
    setProblem("");
    try {
      await stopTask(chatId, task.id);
    } catch (e) {
      setStopping(false);
      setProblem(e instanceof ApiError && e.kind === "signed-out" ? "You are signed out. Sign in, then stop it again." : "The stop did not reach Keystone. Try again.");
    }
  }

  return (
    <section className={task.waiting ? "task is-waiting" : "task"} aria-label="Background task">
      <div className="task-head">
        <Icon name="agents" size={16} />
        <span className="task-kind">Background task</span>
        <span className="task-title">{task.title}</span>
        {(task.status === "queued" || task.status === "running") && (
          <button type="button" className="task-stop" disabled={stopping} onClick={() => void stop()}>
            <Icon name="stop" size={14} />
            {stopping ? "Stopping" : "Stop"}
          </button>
        )}
      </div>
      <p className={running ? "task-where is-running" : "task-where"} role="status">
        {where(task, own)}
      </p>
      <p className="task-link">
        {task.chat_id === task.work_chat_id ? (
          // A task in its own chat, as a scheduled run is (P8-03).
          <>This chat is the task's own: its steps are here, and its last reply will be its result.</>
        ) : own ? (
          <>
            This chat is the task's own. <Link to={`/chats/${task.chat_id}`}>Open the chat it was started from</Link>, where its result will be written.
          </>
        ) : (
          <>
            It works in a chat of its own, and its result will be written here. <Link to={`/chats/${task.work_chat_id}`}>Open its chat</Link>
            {task.waiting ? " to answer it." : " to watch its steps."}
          </>
        )}
      </p>
      {problem && (
        <p className="task-problem" role="alert">
          {problem}
        </p>
      )}
    </section>
  );
}

/**
 * The cards of the chat's tasks that are under way. `onEnded` is called when
 * one ends, so the chat is read again and its note shows. Nothing when there
 * are none.
 */
export function Tasks({ chatId, onEnded }: { chatId: string; onEnded: () => void }) {
  const tasks = useKept(chatId);
  const ended = useRef(onEnded);
  ended.current = onEnded;
  const [listed, setListed] = useState(0);

  // The list, when the chat opens and whenever one of its turns ends: a
  // turn may have started a task with the tool.
  useEffect(() => {
    let live = true;
    listTasks(chatId).then(
      (got) => live && keep(chatId, got),
      () => {}, // the chat screen says what is wrong with the chat
    );
    const off = onEnd((id) => {
      if (id === chatId) setListed((n) => n + 1);
    });
    return () => {
      live = false;
      off();
    };
  }, [chatId, listed]);

  // One stream for each task under way.
  const open = tasks.filter(underWay).map((t) => t.id).join(" ");
  useEffect(() => {
    if (open === "") return;
    const abort = new AbortController();
    for (const id of open.split(" ")) {
      const task = (byChat.get(chatId) ?? []).find((t) => t.id === id);
      if (!task) continue;
      void follow(chatId, task, abort.signal, (t, end) => {
        keep(chatId, (byChat.get(chatId) ?? []).map((x) => (x.id === t.id ? t : x)));
        if (end) ended.current();
      });
    }
    return () => abort.abort();
  }, [chatId, open]);

  const shown = tasks.filter(underWay);
  if (shown.length === 0) return null;
  return (
    <div className="tasks">
      {shown.map((t) => (
        <TaskCard key={t.id} chatId={chatId} task={t} />
      ))}
    </div>
  );
}

/** The note a task wrote in the chat it was started from: its result, with the task's mark. */
export function TaskNote({ chatId, messageId, text }: { chatId: string; messageId: number; text: string }) {
  const task = useKept(chatId).find((t) => t.note_message_id === messageId);
  return (
    <div className="task-note">
      <div className="task-note-head">
        <Icon name="agents" size={16} />
        <span>From a background task</span>
        {task && task.work_chat_id !== chatId && (
          <Link className="task-note-link" to={`/chats/${task.work_chat_id}`}>
            Open its chat
          </Link>
        )}
      </div>
      <div className="reply-text">
        <Markdown text={text} />
      </div>
      <div className="reply-actions">
        <CopyButton text={text} />
      </div>
    </div>
  );
}
