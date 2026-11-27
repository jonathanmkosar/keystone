// The Chats screen (P4-06): a table of every chat, with keyword search,
// filters, sorting, and select and delete. Provisional on the mockups,
// which draw no such screen; built from the tokens. The server does the
// searching, the sorting and the deleting (docs/architecture/manage-chats.md);
// this file draws what it answers.
//
// On a phone: no box takes the focus by itself, since that raises the
// keyboard and halves the page; the text box is 16px, so the browser does
// not zoom in on it; and the confirmation is placed in the part of the
// screen that can be seen, which the keyboard moves and CSS cannot tell.

import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from "react";
import { Link } from "react-router";
import { ApiError } from "./api";
import { useChats } from "./chats";
import { Icon } from "./Icon";
import { importedFacts } from "./Imported";
import { chatTable, deleteChats, matchingIds, type Filter, type Order, type Part, type Skipped, type Sort, type TableAsk, type TableRow } from "./manageCalls";
import "./manage-chats.css";

/** How long the box waits after the last key before it asks the server. */
const typingPause = 250;

const filterNames: { key: Filter; label: string; title: string }[] = [
  { key: "starred", label: "Starred", title: "Chats you starred" },
  { key: "imported", label: "Imported", title: "Chats brought in from another app" },
  { key: "project", label: "In a project", title: "Chats that are in a project" },
  { key: "task", label: "Tasks", title: "The chats background tasks and scheduled runs worked in" },
  { key: "subagent", label: "Sub-agents", title: "The chats sub-agents worked in" },
];

const sortNames: { key: Sort; label: string }[] = [
  { key: "active", label: "Last activity" },
  { key: "title", label: "Title" },
  { key: "messages", label: "Size" },
  { key: "match", label: "Best match" },
];

/** The order a column starts in when it is picked: newest, A to Z, largest, best. */
function firstOrder(sort: Sort): Order {
  return sort === "title" || sort === "match" ? "asc" : "desc";
}

function count(n: number, one: string, many: string): string {
  return n === 1 ? `1 ${one}` : `${n.toLocaleString()} ${many}`;
}

/** When a chat's last message was written: the time if that was today, the date if not. */
function when(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return "";
  const now = new Date();
  if (at.toDateString() === now.toDateString()) return at.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  const year = at.getFullYear() === now.getFullYear() ? undefined : "numeric";
  return at.toLocaleDateString(undefined, { month: "short", day: "numeric", year });
}

function Parts({ parts }: { parts: Part[] }) {
  return (
    <>
      {parts.map((p, i) =>
        p.match ? (
          <mark key={i} className="manage-hit">
            {p.text}
          </mark>
        ) : (
          <span key={i}>{p.text}</span>
        ),
      )}
    </>
  );
}

/** The small marks a chat wears in the table. */
function Marks({ row }: { row: TableRow }) {
  const marks: ReactNode[] = [];
  if (row.project) {
    marks.push(
      <span key="project" className="manage-mark is-project" title={`In the project ${row.project.name}`}>
        <Icon name="projects" size={12} />
        <span className="manage-mark-text">{row.project.name}</span>
      </span>,
    );
  }
  if (row.imported) {
    marks.push(
      <span key="imported" className="manage-mark" title={`Imported from ${row.imported.from}. ${importedFacts(row.imported)}`}>
        imported
      </span>,
    );
  }
  if (row.task) {
    marks.push(
      <span key="task" className="manage-mark" title="A background task or a scheduled run worked in this chat">
        task
      </span>,
    );
  }
  if (row.subagent) {
    marks.push(
      <span key="subagent" className="manage-mark" title="A sub-agent worked in this chat">
        sub-agent
      </span>,
    );
  }
  if (row.held) {
    marks.push(
      <span key="held" className="manage-mark is-held" title="It was sent content from a sensitive space, so only a local or trusted model answers in it">
        sensitive
      </span>,
    );
  }
  if (row.shared) {
    marks.push(
      <span key="shared" className="manage-mark" title={`${count(row.shared, "share link is", "share links are")} open. Deleting the chat turns ${row.shared === 1 ? "it" : "them"} off.`}>
        <Icon name="link" size={12} />
        shared
      </span>,
    );
  }
  if (row.incognito) {
    marks.push(
      <span key="incognito" className="manage-mark" title="An incognito chat: nothing is remembered from it">
        incognito
      </span>,
    );
  }
  if (row.busy || row.working) {
    marks.push(
      <span key="busy" className="manage-mark is-busy" title="Something is still at work in this chat, so a delete leaves it until that ends">
        {row.busy ? "answering" : "working"}
      </span>,
    );
  }
  if (marks.length === 0) return null;
  return <span className="manage-marks">{marks}</span>;
}

/** The line under a title: what the search found, or the chat's summary. */
function Line({ row }: { row: TableRow }) {
  const m = row.match;
  if (m?.snippet && m.snippet.length > 0) {
    return (
      <span className="manage-line">
        <span className="manage-who">{m.role === "user" ? "You: " : "Reply: "}</span>
        <Parts parts={m.snippet} />
        {m.messages > 1 && <span className="manage-more-hits"> (and {count(m.messages - 1, "more message", "more messages")})</span>}
      </span>
    );
  }
  if (row.summary) return <span className="manage-line">{row.summary}</span>;
  return null;
}

function ChatRow({ row, picked, onPick }: { row: TableRow; picked: boolean; onPick: (id: string, on: boolean) => void }) {
  const title = row.title || "New chat";
  const m = row.match;
  return (
    <div className={picked ? "manage-row is-picked" : "manage-row"} role="row">
      <span className="manage-check-cell" role="cell">
        <label className="manage-check">
          <input type="checkbox" checked={picked} aria-label={`Select ${title}`} onChange={(e) => onPick(row.id, e.target.checked)} />
        </label>
      </span>
      <Link to={`/chats/${row.id}`} className="manage-open" role="cell">
        <span className="manage-title-line">
          {row.starred && (
            <span className="star-mark" title="Starred">
              <Icon name="star" size={14} />
            </span>
          )}
          <span className={row.title ? "manage-title" : "manage-title nav-untitled"}>{m?.title && m.title_parts ? <Parts parts={m.title_parts} /> : title}</span>
        </span>
        <Marks row={row} />
        <Line row={row} />
        <span className="manage-facts">
          {count(row.messages, "message", "messages")} · {when(row.active)}
        </span>
      </Link>
      <span className="manage-size" role="cell">
        {row.messages.toLocaleString()}
      </span>
      <span className="manage-when" role="cell">
        {when(row.active)}
      </span>
    </div>
  );
}

/**
 * Keeps an overlay inside the part of the screen that can be seen. The
 * on-screen keyboard shrinks and moves that part, and CSS cannot tell, so
 * the overlay is told where it starts and how tall it is.
 */
function useSeen(ref: RefObject<HTMLElement | null>, on: boolean) {
  useEffect(() => {
    const vv = window.visualViewport;
    const el = ref.current;
    if (!on || !vv || !el) return;
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
  }, [ref, on]);
}

type Outcome = { deleted: number; skipped: Skipped[]; failed: boolean };

/** Asks before anything is deleted, and says what goes and what stays. */
function Confirm({ n, busy, progress, onDelete, onKeep }: { n: number; busy: boolean; progress: string; onDelete: () => void; onKeep: () => void }) {
  const overlay = useRef<HTMLDivElement>(null);
  const keep = useRef<HTMLButtonElement>(null);
  useSeen(overlay, true);
  // The focus goes to Keep, a button: nothing here raises a keyboard.
  useEffect(() => keep.current?.focus({ preventScroll: true }), []);
  useEffect(() => {
    function escape(e: KeyboardEvent) {
      if (e.key === "Escape" && !busy) onKeep();
    }
    document.addEventListener("keydown", escape);
    return () => document.removeEventListener("keydown", escape);
  }, [busy, onKeep]);
  const what = count(n, "chat", "chats");
  return (
    <div className="manage-overlay" ref={overlay}>
      <div className="manage-dialog" role="alertdialog" aria-modal="true" aria-labelledby="manage-confirm-title" aria-describedby="manage-confirm-text">
        <h2 id="manage-confirm-title" className="manage-dialog-title">
          Delete {what}?
        </h2>
        <div id="manage-confirm-text" className="manage-dialog-text">
          <p>
            {n === 1 ? "Its" : "Their"} messages on every branch go, with {n === 1 ? "its" : "their"} uploaded files, artifacts and share links. A share link stops working at once.
          </p>
          <p>A file a project holds stays in the project. What Keystone remembered from {n === 1 ? "it" : "them"} stays in Memory until you forget it there.</p>
          <p>A chat that is still answering, or has a task at work, is left and named afterwards.</p>
          <p className="manage-dialog-strong">There is no undo and no trash: a deleted chat cannot be brought back.</p>
        </div>
        {progress && (
          <p className="manage-dialog-progress" role="status">
            {progress}
          </p>
        )}
        <div className="manage-dialog-row">
          <button type="button" className="button manage-danger" disabled={busy} onClick={onDelete}>
            <Icon name="trash" size={16} />
            Delete {what}
          </button>
          <button type="button" className="button" ref={keep} disabled={busy} onClick={onKeep}>
            Keep {n === 1 ? "it" : "them"}
          </button>
        </div>
      </div>
    </div>
  );
}

/** What a delete did, above the table until it is closed. */
function Report({ outcome, onClose }: { outcome: Outcome; onClose: () => void }) {
  return (
    <div className="manage-report" role="status">
      <div className="manage-report-text">
        <p>
          <strong>{outcome.deleted === 0 ? "No chat was deleted." : `${count(outcome.deleted, "chat was", "chats were")} deleted.`}</strong>
          {outcome.skipped.length > 0 && ` ${count(outcome.skipped.length, "was", "were")} left:`}
        </p>
        {outcome.skipped.length > 0 && (
          <ul className="manage-report-list">
            {outcome.skipped.map((s) => (
              <li key={s.id}>
                <span className="manage-report-title">{s.title || (s.reason === "not_found" ? "A chat that is no longer here" : "New chat")}</span>: {s.why}
              </li>
            ))}
          </ul>
        )}
        {outcome.failed && <p>The rest were not tried, because the server stopped answering. Check that it is running, then select them again.</p>}
      </div>
      <button type="button" className="icon-button" aria-label="Close this note" title="Close" onClick={onClose}>
        <Icon name="close" size={16} />
      </button>
    </div>
  );
}

/** Every chat as a table: search, filter, sort, select, delete. */
export function Chats() {
  const { refresh: refreshSidebar, lost } = useChats();
  const [typed, setTyped] = useState("");
  const [ask, setAsk] = useState<TableAsk>({ query: "", sort: "active", order: "desc", filters: [] });
  const [status, setStatus] = useState<"loading" | "ready" | "failed">("loading");
  const [rows, setRows] = useState<TableRow[]>([]);
  const [total, setTotal] = useState(0);
  const [more, setMore] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [deleteMax, setDeleteMax] = useState(1);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [allNote, setAllNote] = useState("");
  const [selecting, setSelecting] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [progress, setProgress] = useState("");
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  const [problem, setProblem] = useState("");
  const [again, setAgain] = useState(0);
  const turn = useRef(0);
  const end = useRef<HTMLDivElement>(null);

  const failed = useCallback(
    (e: unknown, words: string) => {
      if (e instanceof ApiError && e.kind === "signed-out") lost();
      else setProblem(words);
    },
    [lost],
  );

  // What is typed becomes the search a moment after the last key.
  useEffect(() => {
    const t = window.setTimeout(() => {
      setAsk((a) => {
        const query = typed.trim();
        if (a.query === query) return a;
        // A new search starts by its best match; with the box cleared, by the newest.
        if (query && !a.query) return { ...a, query, sort: "match", order: "asc" };
        if (!query && a.sort === "match") return { ...a, query, sort: "active", order: "desc" };
        return { ...a, query };
      });
    }, typingPause);
    return () => window.clearTimeout(t);
  }, [typed]);

  // The first page, whenever what is asked for changes. A selection is of
  // what was on the screen, so a new search or filter starts with none.
  const key = `${ask.query}\n${ask.filters.join(",")}`;
  useEffect(() => {
    setPicked(new Set());
    setAllNote("");
  }, [key]);
  useEffect(() => {
    const mine = ++turn.current;
    setStatus((s) => (s === "ready" ? s : "loading"));
    chatTable(ask, 0).then(
      (page) => {
        if (mine !== turn.current) return;
        setRows(page.rows);
        setTotal(page.total);
        setMore(page.more);
        setDeleteMax(page.delete_max);
        setStatus("ready");
        setProblem("");
      },
      (e) => {
        if (mine !== turn.current) return;
        if (e instanceof ApiError && e.kind === "signed-out") lost();
        else setStatus("failed");
      },
    );
  }, [ask, again, lost]);

  const loadMore = useCallback(() => {
    if (loadingMore || !more || status !== "ready") return;
    const mine = turn.current;
    setLoadingMore(true);
    chatTable(ask, rows.length).then(
      (page) => {
        setLoadingMore(false);
        if (mine !== turn.current) return;
        setRows((have) => {
          const seen = new Set(have.map((r) => r.id));
          return [...have, ...page.rows.filter((r) => !seen.has(r.id))];
        });
        setTotal(page.total);
        setMore(page.more && page.rows.length > 0);
      },
      (e) => {
        setLoadingMore(false);
        if (mine !== turn.current) return;
        setMore(false);
        failed(e, "The rest of the chats could not be read. Check that the server is running.");
      },
    );
  }, [ask, rows.length, more, loadingMore, status, failed]);

  // The next page is read as the end of the list comes into view.
  useEffect(() => {
    const el = end.current;
    if (!el || !more || typeof IntersectionObserver === "undefined") return;
    const watch = new IntersectionObserver((entries) => {
      if (entries.some((x) => x.isIntersecting)) loadMore();
    });
    watch.observe(el);
    return () => watch.disconnect();
  }, [more, loadMore]);

  const shownIds = useMemo(() => rows.map((r) => r.id), [rows]);
  const shownPicked = shownIds.filter((id) => picked.has(id)).length;
  const allShown = rows.length > 0 && shownPicked === rows.length;
  const narrowed = ask.query !== "" || ask.filters.length > 0;

  function pick(id: string, on: boolean) {
    setAllNote("");
    setPicked((have) => {
      const next = new Set(have);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });
  }

  function pickShown(on: boolean) {
    setAllNote("");
    setPicked(on ? new Set(shownIds) : new Set());
  }

  function clear() {
    setAllNote("");
    setPicked(new Set());
  }

  async function pickAllMatching() {
    setSelecting(true);
    setProblem("");
    try {
      const all = await matchingIds(ask);
      setPicked(new Set(all.ids));
      const what = narrowed ? "that match" : "you have";
      setAllNote(
        all.cut
          ? `The first ${all.ids.length.toLocaleString()} of the ${all.total.toLocaleString()} chats ${what} are selected. Delete these, then select the rest.`
          : `All ${count(all.ids.length, "chat", "chats")} ${what} ${all.ids.length === 1 ? "is" : "are"} selected.`,
      );
    } catch (e) {
      failed(e, "The chats could not be selected. Check that the server is running.");
    } finally {
      setSelecting(false);
    }
  }

  function setSort(sort: Sort) {
    setAsk((a) => (a.sort === sort ? { ...a, order: a.order === "asc" ? "desc" : "asc" } : { ...a, sort, order: firstOrder(sort) }));
  }

  function toggleFilter(f: Filter) {
    setAsk((a) => ({ ...a, filters: a.filters.includes(f) ? a.filters.filter((x) => x !== f) : [...a.filters, f] }));
  }

  // One request deletes at most deleteMax chats, so a larger selection goes
  // in several, one after another.
  async function removePicked() {
    const ids = Array.from(picked);
    const result: Outcome = { deleted: 0, skipped: [], failed: false };
    const gone = new Set<string>();
    setDeleting(true);
    setProblem("");
    const step = Math.max(1, deleteMax);
    for (let at = 0; at < ids.length; at += step) {
      if (ids.length > step) setProgress(`Deleting: ${at.toLocaleString()} of ${ids.length.toLocaleString()} done.`);
      try {
        const answer = await deleteChats(ids.slice(at, at + step));
        result.deleted += answer.deleted.length;
        result.skipped.push(...answer.skipped);
        answer.deleted.forEach((id) => gone.add(id));
      } catch (e) {
        if (e instanceof ApiError && e.kind === "signed-out") {
          lost();
          return;
        }
        result.failed = true;
        break;
      }
    }
    setDeleting(false);
    setProgress("");
    setConfirming(false);
    setOutcome(result);
    // What was left stays selected, so it can be tried again.
    setPicked(new Set(result.skipped.filter((s) => s.reason !== "not_found").map((s) => s.id)));
    setAllNote("");
    setRows((have) => have.filter((r) => !gone.has(r.id)));
    setTotal((n) => Math.max(0, n - gone.size));
    setAgain((n) => n + 1); // the table, read again from its start
    refreshSidebar();
  }

  const sortLabel = (s: Sort) => (ask.sort === s ? (ask.order === "asc" ? "ascending" : "descending") : "none");
  const arrow = (s: Sort) => (ask.sort === s ? <span className={ask.order === "asc" ? "manage-arrow is-up" : "manage-arrow"} aria-hidden="true" /> : null);
  const sortChoices = sortNames.filter((s) => s.key !== "match" || ask.query !== "");

  return (
    <div className="manage-page">
      <div className="list-head">
        <h1 className="page-title">Chats</h1>
        <Link to="/" className="button button-primary">
          <Icon name="plus" size={16} />
          New chat
        </Link>
      </div>

      <div className="manage-tools">
        <div className="manage-search-wrap">
          <Icon name="search" size={16} />
          <label className="offscreen" htmlFor="manage-search">
            Search the titles and messages of your chats
          </label>
          <input id="manage-search" type="search" className="manage-search" placeholder="Search titles and messages" value={typed} autoComplete="off" enterKeyHint="search" onChange={(e) => setTyped(e.target.value)} />
          {typed && (
            <button type="button" className="manage-search-clear" aria-label="Clear the search" title="Clear" onClick={() => setTyped("")}>
              <Icon name="close" size={14} />
            </button>
          )}
        </div>
        <div className="manage-filters" role="group" aria-label="Show only">
          {filterNames.map((f) => (
            <button key={f.key} type="button" className={ask.filters.includes(f.key) ? "manage-chip is-on" : "manage-chip"} aria-pressed={ask.filters.includes(f.key)} title={f.title} onClick={() => toggleFilter(f.key)}>
              {f.label}
            </button>
          ))}
        </div>
        <div className="manage-sort">
          <label htmlFor="manage-sort">Sort by</label>
          <select id="manage-sort" className="manage-select" value={ask.sort} onChange={(e) => setAsk((a) => ({ ...a, sort: e.target.value as Sort, order: firstOrder(e.target.value as Sort) }))}>
            {sortChoices.map((s) => (
              <option key={s.key} value={s.key}>
                {s.label}
              </option>
            ))}
          </select>
          <button type="button" className="button manage-order" title="Turn the order round" onClick={() => setAsk((a) => ({ ...a, order: a.order === "asc" ? "desc" : "asc" }))}>
            {ask.sort === "title" ? (ask.order === "asc" ? "A to Z" : "Z to A") : ask.sort === "messages" ? (ask.order === "desc" ? "Largest first" : "Smallest first") : ask.sort === "match" ? (ask.order === "asc" ? "Best first" : "Best last") : ask.order === "desc" ? "Newest first" : "Oldest first"}
          </button>
        </div>
      </div>

      {outcome && <Report outcome={outcome} onClose={() => setOutcome(null)} />}
      {problem && (
        <p className="form-error" role="alert">
          {problem}
        </p>
      )}

      {status === "loading" && (
        <p className="quiet" aria-busy="true">
          Loading.
        </p>
      )}
      {status === "failed" && (
        <>
          <p className="quiet">The chats could not be read. Check that the server is running.</p>
          <div>
            <button type="button" className="button" onClick={() => setAgain((n) => n + 1)}>
              <Icon name="retry" size={16} />
              Try again
            </button>
          </div>
        </>
      )}
      {status === "ready" && rows.length === 0 && (
        <p className="quiet">{ask.query ? "No chat holds these words in its title or in one message." : ask.filters.length > 0 ? "No chat fits these filters." : "No chats yet. Start one with New chat."}</p>
      )}

      {status === "ready" && rows.length > 0 && (
        <>
          <p className="manage-count" role="status">
            {narrowed ? `${count(total, "chat matches", "chats match")}` : `${count(total, "chat", "chats")}`}
            {ask.sort === "match" && ask.query ? (ask.order === "asc" ? ", the best match first." : ", the best match last.") : "."}
            {rows.length < total && ` ${rows.length.toLocaleString()} shown.`}
          </p>
          <div className="manage-table" role="table" aria-label="Your chats" aria-rowcount={total}>
            <div className="manage-row manage-head" role="row">
              <span className="manage-check-cell" role="columnheader">
                <label className="manage-check">
                  <input
                    type="checkbox"
                    checked={allShown}
                    aria-label="Select all shown"
                    ref={(el) => {
                      if (el) el.indeterminate = shownPicked > 0 && !allShown;
                    }}
                    onChange={(e) => pickShown(e.target.checked)}
                  />
                  <span className="manage-check-words" aria-hidden="true">
                    Select all shown
                  </span>
                </label>
              </span>
              <span role="columnheader" aria-sort={sortLabel("title")} className="manage-head-title">
                <button type="button" className="manage-head-button" onClick={() => setSort("title")}>
                  Title {arrow("title")}
                </button>
              </span>
              <span role="columnheader" aria-sort={sortLabel("messages")} className="manage-size">
                <button type="button" className="manage-head-button" onClick={() => setSort("messages")}>
                  Messages {arrow("messages")}
                </button>
              </span>
              <span role="columnheader" aria-sort={sortLabel("active")} className="manage-when">
                <button type="button" className="manage-head-button" onClick={() => setSort("active")}>
                  Last activity {arrow("active")}
                </button>
              </span>
            </div>
            {(allShown || allNote) && (
              <div className="manage-all" role="row">
                <span role="cell">
                  {allNote || `All ${rows.length.toLocaleString()} shown are selected.`}{" "}
                  {!allNote && total > rows.length && (
                    <button type="button" className="manage-link" disabled={selecting} onClick={() => void pickAllMatching()}>
                      {selecting ? "Selecting." : narrowed ? `Select all ${total.toLocaleString()} that match` : `Select all ${total.toLocaleString()} chats`}
                    </button>
                  )}
                  {allNote && (
                    <button type="button" className="manage-link" onClick={clear}>
                      Clear the selection
                    </button>
                  )}
                </span>
              </div>
            )}
            {rows.map((r) => (
              <ChatRow key={r.id} row={r} picked={picked.has(r.id)} onPick={pick} />
            ))}
          </div>
          {more && (
            <div className="manage-end" ref={end}>
              <button type="button" className="button" disabled={loadingMore} onClick={loadMore}>
                {loadingMore ? "Loading." : `Show more (${(total - rows.length).toLocaleString()} left)`}
              </button>
            </div>
          )}
        </>
      )}

      {picked.size > 0 && (
        <div className="manage-bar" role="region" aria-label="The selected chats">
          <span className="manage-bar-count">{count(picked.size, "chat", "chats")} selected</span>
          <button type="button" className="button" onClick={clear}>
            Clear
          </button>
          <button type="button" className="button manage-danger" onClick={() => setConfirming(true)}>
            <Icon name="trash" size={16} />
            Delete
          </button>
        </div>
      )}

      {confirming && <Confirm n={picked.size} busy={deleting} progress={progress} onDelete={() => void removePicked()} onKeep={() => setConfirming(false)} />}
    </div>
  );
}
