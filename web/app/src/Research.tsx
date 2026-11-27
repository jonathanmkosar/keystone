// Research mode in the chat (P8-04): the Research control beside the
// composer's pickers, the line that says what a task will run with before
// it starts, the card of a task under way (its plan, a lane for each part,
// the sources found, its spend against its budget, Stop), and the note a
// task writes when it ends, drawn with its citations linked, its report's
// card and its sources.
//
// Provisional: there is no mockup for any of it (the agent mockups, P1-05,
// are not approved). It is built from the design tokens, the task card,
// the sub-agents' lanes and the artifact card, and says so in research.css.
//
// A card reads its task about once a second while it is under way, so it is
// the same after a reload. docs/architecture/research-mode.md has the
// server's side.

import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router";
import { ApiError } from "./api";
import { openArtifact, useOpenArtifact } from "./artifacts";
import { CitationSources, type Source } from "./citations";
import { Icon } from "./Icon";
import { CopyButton, Markdown } from "./Markdown";
import {
  armResearch,
  getResearch,
  keepOneResearch,
  keepResearch,
  keptResearch,
  listResearch,
  newChatKey,
  researchDollars,
  researchTerms,
  researchUnderWay,
  stopResearch,
  useResearch,
  useResearchArmed,
  useResearchProblem,
  type Research,
  type ResearchSource,
  type ResearchTerms,
} from "./researchCalls";
import { Lane } from "./Subagents";
import { onEnd } from "./turns";
import "./research.css";

function plural(n: number, one: string, many: string): string {
  return n === 1 ? `${n} ${one}` : `${n} ${many}`;
}

function host(url: string): string {
  try {
    return new URL(url).host.replace(/^www\./, "");
  } catch {
    return url;
  }
}

/** The terms a task would start with, read once for each mount. */
function useTerms(wanted: boolean): ResearchTerms | undefined {
  const [terms, setTerms] = useState<ResearchTerms | undefined>(undefined);
  useEffect(() => {
    if (!wanted) return;
    let live = true;
    researchTerms().then(
      (t) => live && setTerms(t),
      () => {}, // the hint says less
    );
    return () => {
      live = false;
    };
  }, [wanted]);
  return terms;
}

/**
 * The Research control: a switch beside the composer's pickers. On, the
 * next message sent from this composer is a research question.
 */
export function ResearchControl({ chatId, newChatFor }: { chatId?: string; newChatFor?: string }) {
  const key = chatId ?? newChatKey(newChatFor ?? "");
  const on = useResearchArmed(key);
  return (
    <button
      type="button"
      className={on ? "research-button is-on" : "research-button"}
      aria-pressed={on}
      title={on ? "Research is on: your next message starts a research task" : "Research: a long report from many sources, written in the background"}
      onClick={() => armResearch(key, !on)}
    >
      <Icon name="search" size={16} />
      <span className="research-button-label">Research</span>
    </button>
  );
}

/** What a research task will run with, shown above the composer while the control is on. */
export function ResearchHint({ chatId, newChatFor }: { chatId?: string; newChatFor?: string }) {
  const key = chatId ?? newChatKey(newChatFor ?? "");
  const on = useResearchArmed(key);
  const terms = useTerms(on);
  if (!on) return null;
  const full = terms !== undefined && terms.open >= terms.open_max;
  return (
    <p className="research-hint" role="status">
      <Icon name="search" size={16} />
      <span>
        {terms ? (
          <>
            Your next message starts a research task. It may spend up to <strong>{researchDollars(terms.budget_micros)}</strong>, splits the question into up to {plural(terms.parts, "part", "parts")}, each
            reading about {plural(terms.sources_per_part, "source", "sources")}, and writes a report of about {terms.report_words.toLocaleString()} words with its sources cited. It runs in the background. Files
            are left out.
            {terms.paused && " Every agent is paused, so it waits until they are resumed."}
            {full && ` You have ${plural(terms.open, "research task", "research tasks")} under way, the most at once, so it will not start until one ends.`}
          </>
        ) : (
          <>Your next message starts a research task, which runs in the background within its own budget.</>
        )}
      </span>
    </p>
  );
}

/** A task's state in a word or two. */
function stateOf(r: Research): string {
  if (r.waiting) return "Waiting for you";
  switch (r.status) {
    case "queued":
      return "Waiting to start";
    case "running":
      return r.stage === "plan" ? "Planning" : r.stage === "write" ? "Writing" : "Researching";
    case "paused":
      return "Paused";
    case "done":
      return "Done";
    case "stopped":
      return "Stopped";
  }
  return "Did not finish";
}

function SourceList({ sources, cited }: { sources: ResearchSource[]; cited: boolean }) {
  return (
    <ol className="research-sources">
      {sources.map((s) => (
        <li key={`${s.n}-${s.url}`} className="research-source">
          <span className="research-source-n" aria-hidden="true">
            {s.n}
          </span>
          <a className="research-source-link" href={s.url} target="_blank" rel="noopener noreferrer" title={s.url}>
            {s.title}
          </a>
          <span className="research-source-meta">
            {host(s.url)}
            {cited ? "" : ` · part ${s.part}`}
            {s.fetched ? "" : " · search extract"}
          </span>
        </li>
      ))}
    </ol>
  );
}

/** The parts: the plan's lines until the sub-agents are there, then a lane for each. */
function Parts({ task, onChange }: { task: Research; onChange: () => void }) {
  if (task.parts.length > 0) {
    return (
      <ol className="lanes research-lanes" aria-label="The parts of the research">
        {task.parts.map((p) => (
          <Lane key={p.id} chatId={task.work_chat_id} sub={p} onChange={onChange} />
        ))}
      </ol>
    );
  }
  if (task.plan.length === 0) return null;
  return (
    <ol className="research-plan" aria-label="The plan">
      {task.plan.map((p, i) => (
        <li key={i}>
          <strong>{p.title}</strong>: {p.task}
        </li>
      ))}
    </ol>
  );
}

function ResearchCard({ chatId, task, onChange }: { chatId: string; task: Research; onChange: () => void }) {
  const [stopping, setStopping] = useState(false);
  const [problem, setProblem] = useState("");
  const own = chatId === task.work_chat_id;
  const open = task.status === "queued" || task.status === "running" || task.status === "paused";
  const working = task.status === "running" && !task.waiting;

  async function stop() {
    setStopping(true);
    setProblem("");
    try {
      keepOneResearch(chatId, await stopResearch(chatId, task.id));
    } catch (e) {
      setProblem(e instanceof ApiError && e.kind === "signed-out" ? "You are signed out. Sign in, then stop it again." : "The stop did not reach Keystone. Try again.");
    } finally {
      setStopping(false);
    }
  }

  const where = open ? task.progress || stateOf(task) : "Writing its report in this chat";
  return (
    <section className={task.waiting ? "research is-waiting" : task.status === "paused" ? "research is-paused" : "research"} aria-label="Research task">
      <div className="research-head">
        <Icon name="search" size={16} />
        <span className="research-kind">Research</span>
        <span className="research-title">{task.title}</span>
        <span className="research-state">{stateOf(task)}</span>
        {open && (
          <button type="button" className="task-stop" disabled={stopping} onClick={() => void stop()}>
            <Icon name="stop" size={14} />
            {stopping ? "Stopping" : "Stop"}
          </button>
        )}
      </div>
      <p className="research-question">{task.question}</p>
      <p className={working ? "research-where is-running" : "research-where"} role="status">
        {where} · {researchDollars(task.spent_micros)} of {researchDollars(task.budget_micros)}
      </p>
      {(task.plan.length > 0 || task.parts.length > 0) && (
        <>
          <div className="research-heading">{task.parts.length > 0 ? plural(task.parts.length, "part", "parts") : "The plan"}</div>
          <Parts task={task} onChange={onChange} />
        </>
      )}
      {task.sources.length > 0 && (
        <>
          <div className="research-heading">{plural(task.sources.length, "source", "sources")} found</div>
          <SourceList sources={task.sources} cited={false} />
        </>
      )}
      <p className="task-link">
        {own ? (
          <>
            This chat is the research's own: its plan and its report are written here. <Link to={`/chats/${task.chat_id}`}>Open the chat it was started from</Link>, where the report will be saved.
          </>
        ) : (
          <>
            It works in chats of its own, and its report will be saved here. <Link to={`/chats/${task.work_chat_id}`}>Open its chat</Link> to watch it.
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
 * The cards of the chat's research tasks that are under way. `onEnded` is
 * called when one ends, so the chat is read again and its note shows.
 * Nothing when there are none.
 */
export function ResearchCards({ chatId, onEnded }: { chatId: string; onEnded: () => void }) {
  const tasks = useResearch(chatId);
  const problem = useResearchProblem(chatId);
  const ended = useRef(onEnded);
  ended.current = onEnded;
  const [listed, setListed] = useState(0);

  // The list, when the chat opens and whenever one of its turns ends.
  useEffect(() => {
    let live = true;
    listResearch(chatId).then(
      (got) => {
        if (!live) return;
        // A task this page has just started may not be in a list asked for a moment before it was written.
        const mine = keptResearch(chatId).filter((t) => researchUnderWay(t) && !got.some((g) => g.id === t.id));
        keepResearch(chatId, [...got, ...mine]);
      },
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

  // Each task under way is read about once a second.
  const open = tasks.filter(researchUnderWay).map((t) => t.id).join(" ");
  useEffect(() => {
    if (open === "") return;
    let live = true;
    let timer: number | undefined;
    const look = async () => {
      for (const id of open.split(" ")) {
        try {
          const got = await getResearch(chatId, id);
          if (!live) return;
          keepOneResearch(chatId, got);
          if (!researchUnderWay(got)) ended.current();
        } catch (e) {
          if (e instanceof ApiError && (e.kind === "missing" || e.kind === "signed-out")) return;
        }
      }
      if (live) timer = window.setTimeout(() => void look(), 1000);
    };
    timer = window.setTimeout(() => void look(), 400);
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
  }, [chatId, open]);

  const shown = tasks.filter(researchUnderWay);
  if (shown.length === 0 && problem === "") return null;
  return (
    <div className="tasks research-cards">
      {problem !== "" && (
        <p className="task-problem" role="alert">
          {problem}
        </p>
      )}
      {shown.map((t) => (
        <ResearchCard key={t.id} chatId={chatId} task={t} onChange={() => setListed((n) => n + 1)} />
      ))}
    </div>
  );
}

/** The research task whose note is this message of the chat, if the page has seen one. */
export function useResearchOfNote(chatId: string, messageId: number): Research | undefined {
  return useResearch(chatId).find((t) => t.note_message_id === messageId);
}

/**
 * The note a research task wrote in the chat it was started from: its
 * summary with each citation a link to its source, the report's card, the
 * sources the report cites, and the parts.
 */
export function ResearchNote({ chatId, task, text }: { chatId: string; task: Research; text: string }) {
  const [showParts, setShowParts] = useState(false);
  const openNow = useOpenArtifact(chatId);
  const sources = useMemo(() => {
    const m = new Map<number, Source>();
    for (const s of task.sources) m.set(s.n, { n: s.n, title: s.title, url: s.url });
    return m;
  }, [task.sources]);
  const here = task.artifact_id !== undefined && task.artifact_id !== "" && task.artifact_chat_id === chatId;
  const isOpen = here && openNow?.artifactId === task.artifact_id;
  const facts = [
    task.status === "done" ? plural(task.sources.length, "source", "sources") + " cited" : "",
    task.status === "done" && task.dropped_citations > 0 ? plural(task.dropped_citations, "citation", "citations") + " taken out" : "",
    `${researchDollars(task.spent_micros)} of ${researchDollars(task.budget_micros)}`,
  ].filter(Boolean);
  return (
    <div className="task-note research-note">
      <div className="task-note-head">
        <Icon name="search" size={16} />
        <span>{task.status === "done" ? "Research report" : "Research"}</span>
        {task.at_budget && <span className="research-pill">stopped at its budget</span>}
        <Link className="task-note-link" to={`/chats/${task.work_chat_id}`}>
          Open its chat
        </Link>
      </div>
      <div className="reply-text">
        <CitationSources sources={sources}>
          <Markdown text={text} />
        </CitationSources>
      </div>
      {task.status === "done" &&
        (here ? (
          <button type="button" className={isOpen ? "artifact-card is-open" : "artifact-card"} aria-pressed={isOpen} onClick={() => openArtifact(chatId, task.artifact_id ?? "")}>
            <span className="artifact-card-icon">
              <Icon name="artifacts" size={20} />
            </span>
            <span className="artifact-card-text">
              <span className="artifact-card-title">{task.title}</span>
              <span className="artifact-card-meta">Research report · {facts.join(" · ")}</span>
            </span>
            <span className="artifact-card-open">{isOpen ? "Open" : "Open beside the chat"}</span>
          </button>
        ) : task.artifact_id ? (
          <p className="task-link">
            The report is in the research's own chat. <Link to={`/chats/${task.work_chat_id}`}>Open it there.</Link>
          </p>
        ) : (
          <p className="task-link">The report is being saved.</p>
        ))}
      {task.status === "done" && task.sources.length > 0 && (
        <>
          <div className="research-heading">Sources</div>
          <SourceList sources={task.sources} cited />
        </>
      )}
      {task.parts.length > 0 && (
        <>
          <button type="button" className="lane-toggle research-toggle" aria-expanded={showParts} onClick={() => setShowParts((o) => !o)}>
            {showParts ? "Hide the parts" : `Show the ${plural(task.parts.length, "part", "parts")}`}
          </button>
          {showParts && <Parts task={task} onChange={() => {}} />}
        </>
      )}
      <div className="reply-actions">
        <CopyButton text={text} />
      </div>
    </div>
  );
}
