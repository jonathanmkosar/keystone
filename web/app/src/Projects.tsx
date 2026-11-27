// Projects (P9-01): the projects screen, a project's page, the projects in
// the sidebar, and the project chip in a chat's header that moves the chat
// into a project or out of one. There is no mockup for any of it (the
// workspace mockups, P1-06, are not approved), so it is built from the
// tokens and the patterns of the chat and settings screens, and is
// provisional. docs/architecture/projects.md has the server's side.

import { useEffect, useRef, useState, type ChangeEvent, type FormEvent } from "react";
import { Link, NavLink, useNavigate, useParams } from "react-router";
import {
  addProjectFile,
  ApiError,
  changeProject,
  createProject,
  deleteProject,
  markProjectFile,
  moveChat,
  readProject,
  removeProjectFile,
  startProjectChat,
  type Chat,
  type ProjectChange,
  type ProjectFile,
  type ProjectView,
} from "./api";
import { titleOf, useChats } from "./chats";
import { Composer } from "./Composer";
import { projectState } from "./composerSheet";
import { Icon } from "./Icon";
import { SheetRow, useSheet } from "./PhoneSheet";
import { changedOn, keepChatProject, refreshProjects, sizeOf, useChatProject, useProjects } from "./projectStore";
import { send } from "./turns";
import "./projects.css";

/** The server's words for a refusal, or the fallback. */
function reason(e: unknown, fallback: string): string {
  if (e instanceof ApiError && (e.status === 400 || e.status === 409 || e.status === 413) && e.said) return e.said;
  return fallback;
}

/** How many sidebar rows the projects take. The rest are on the projects screen. */
const sidebarShown = 8;

/** The projects in the sidebar: the ones changed last, archived ones left out. */
export function SidebarProjects() {
  const { lost } = useChats();
  const { projects } = useProjects(lost);
  const open = projects.filter((p) => !p.archived);
  if (open.length === 0) return null;
  return (
    <div className="nav-group">
      <div className="nav-heading">PROJECTS</div>
      {open.slice(0, sidebarShown).map((p) => (
        <NavLink key={p.id} to={`/projects/${p.id}`} className="nav" title={p.name}>
          <Icon name="projects" size={16} />
          <span className="nav-text">{p.name}</span>
        </NavLink>
      ))}
      {open.length > sidebarShown && (
        <Link to="/projects" className="nav nav-more">
          All projects
        </Link>
      )}
    </div>
  );
}

/** The projects screen: every project, a new one, and the archived ones apart. */
export function Projects() {
  const { lost } = useChats();
  const navigate = useNavigate();
  const { status, projects, limits } = useProjects(lost);
  const [making, setMaking] = useState(false);
  const [name, setName] = useState("");
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState("");
  const [showArchived, setShowArchived] = useState(false);
  const open = projects.filter((p) => !p.archived);
  const archived = projects.filter((p) => p.archived);

  async function make(e: FormEvent) {
    e.preventDefault();
    if (name.trim() === "") return;
    setBusy(true);
    setProblem("");
    try {
      const made = await createProject(name.trim(), text);
      await refreshProjects(lost);
      navigate(`/projects/${made.project.id}`);
    } catch (err) {
      if (err instanceof ApiError && err.kind === "signed-out") lost();
      else setProblem(reason(err, "The project could not be made. Check that the server is running."));
      setBusy(false);
    }
  }

  return (
    <div className="list-page">
      <div className="list-head">
        <h1 className="page-title">Projects</h1>
        {!making && (
          <button type="button" className="button button-primary" onClick={() => setMaking(true)}>
            <Icon name="plus" size={16} />
            New project
          </button>
        )}
      </div>
      {making && (
        <form className="project-form" onSubmit={make}>
          <div className="field">
            <label className="field-label" htmlFor="project-name">
              Name
            </label>
            <input id="project-name" className="input" value={name} maxLength={limits?.name_max_chars || undefined} autoFocus disabled={busy} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="field">
            <label className="field-label" htmlFor="project-instructions">
              Instructions <span className="project-optional">optional</span>
            </label>
            <textarea
              id="project-instructions"
              className="textarea"
              value={text}
              disabled={busy}
              placeholder="What every chat in this project should know, or how it should answer."
              onChange={(e) => setText(e.target.value)}
            />
          </div>
          {problem && (
            <p className="form-error" role="alert">
              {problem}
            </p>
          )}
          <div className="project-actions">
            <button type="submit" className="button button-primary" disabled={busy || name.trim() === ""}>
              {busy ? "Making it…" : "Make the project"}
            </button>
            <button type="button" className="button" disabled={busy} onClick={() => setMaking(false)}>
              Cancel
            </button>
          </div>
        </form>
      )}
      {status === "loading" && (
        <p className="quiet" aria-busy="true">
          Loading.
        </p>
      )}
      {status === "failed" && <p className="quiet">The projects could not be read. Check that the server is running.</p>}
      {status === "ready" && open.length === 0 && !making && (
        <p className="quiet">No projects yet. A project keeps instructions and files that every chat in it starts with.</p>
      )}
      {open.length > 0 && (
        <div className="project-grid">
          {open.map((p) => (
            <ProjectCard key={p.id} id={p.id} name={p.name} instructions={p.instructions} chats={p.chats} files={p.files} changed={p.changed} archived={false} />
          ))}
        </div>
      )}
      {archived.length > 0 && (
        <section className="list-section" aria-label="Archived projects">
          <button type="button" className="project-toggle" aria-expanded={showArchived} onClick={() => setShowArchived((s) => !s)}>
            <Icon name={showArchived ? "chevron" : "caret"} size={14} />
            Archived ({archived.length})
          </button>
          {showArchived && (
            <div className="project-grid">
              {archived.map((p) => (
                <ProjectCard key={p.id} id={p.id} name={p.name} instructions={p.instructions} chats={p.chats} files={p.files} changed={p.changed} archived />
              ))}
            </div>
          )}
        </section>
      )}
    </div>
  );
}

function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

function ProjectCard(p: { id: string; name: string; instructions: string; chats: number; files: number; changed: string; archived: boolean }) {
  return (
    <Link to={`/projects/${p.id}`} className={p.archived ? "project-card is-archived" : "project-card"}>
      <span className="project-card-name">
        <Icon name="projects" size={16} />
        {p.name}
      </span>
      <span className="project-card-text">{p.instructions || "No instructions."}</span>
      <span className="project-card-meta">
        {plural(p.chats, "chat", "chats")} · {plural(p.files, "file", "files")} · {changedOn(p.changed)}
      </span>
    </Link>
  );
}

type Loaded = { state: "loading" } | { state: "ready"; view: ProjectView } | { state: "missing" } | { state: "failed" };

/** A project's page at this address. Each project gets a page of its own, so nothing of the last one shows. */
export function ProjectPage() {
  const { id = "" } = useParams();
  return <OneProject key={id} id={id} />;
}

/** One project: its name, a composer that starts a chat in it, its chats, instructions, files and memory. */
function OneProject({ id }: { id: string }) {
  const { put, lost } = useChats();
  const navigate = useNavigate();
  const [loaded, setLoaded] = useState<Loaded>({ state: "loading" });
  const [starting, setStarting] = useState(false);
  const [problem, setProblem] = useState("");
  const [unsent, setUnsent] = useState({ text: "", tries: 0 });

  function failed(e: unknown, fallback: string) {
    if (e instanceof ApiError && e.kind === "signed-out") lost();
    else setProblem(reason(e, fallback));
  }

  useEffect(() => {
    let live = true;
    readProject(id).then(
      (view) => {
        if (live) setLoaded({ state: "ready", view });
      },
      (e) => {
        if (!live) return;
        if (e instanceof ApiError && e.kind === "signed-out") lost();
        else setLoaded({ state: e instanceof ApiError && e.kind === "missing" ? "missing" : "failed" });
      },
    );
    return () => {
      live = false;
    };
  }, [id, lost]);

  // A change the server answered: the page shows it, and the lists follow.
  function shown(view: ProjectView) {
    setLoaded({ state: "ready", view });
    void refreshProjects(lost);
  }

  async function change(c: ProjectChange, fallback: string): Promise<boolean> {
    setProblem("");
    try {
      shown(await changeProject(id, c));
      return true;
    } catch (e) {
      failed(e, fallback);
      return false;
    }
  }

  async function begin(text: string) {
    setStarting(true);
    setProblem("");
    try {
      const made = await startProjectChat(id);
      put(made.chat, true);
      if (loaded.state === "ready") keepChatProject(made.chat.id, loaded.view.project);
      send(made.chat.id, text);
      void refreshProjects(lost);
      navigate(`/chats/${made.chat.id}`);
    } catch (e) {
      setStarting(false);
      setUnsent((u) => ({ text, tries: u.tries + 1 }));
      failed(e, "The chat could not be started. Check that the server is running, then send the message again.");
    }
  }

  if (loaded.state === "loading") {
    return (
      <div className="list-page" aria-busy="true">
        <p className="quiet">Opening the project.</p>
      </div>
    );
  }
  if (loaded.state === "missing" || loaded.state === "failed") {
    return (
      <div className="home">
        <h1 className="page-title">{loaded.state === "missing" ? "This project is not here" : "The project could not be opened"}</h1>
        <p className="quiet">
          {loaded.state === "missing" ? "There is no project at this address in your record. It may have been deleted." : "Keystone did not answer. Check that the server is running."}
        </p>
        <Link to="/projects" className="button">
          All projects
        </Link>
      </div>
    );
  }

  const { project, files, chats, limits } = loaded.view;
  return (
    <div className="project-page">
      <ProjectHead
        view={loaded.view}
        onRename={(name) => change({ name }, "The project was not renamed")}
        onArchive={(archived) => void change({ archived }, archived ? "The project was not archived" : "The project was not brought back")}
        onDelete={async () => {
          setProblem("");
          try {
            await deleteProject(id);
            await refreshProjects(lost);
            navigate("/projects");
          } catch (e) {
            failed(e, "The project was not deleted. Check that the server is running.");
          }
        }}
      />
      {project.archived && <p className="project-note">This project is archived. Its chats still carry its instructions and files. Bring it back from the menu by its name.</p>}
      {problem && (
        <p className="form-error project-problem" role="alert">
          {problem}
        </p>
      )}
      <div className="project-main">
        <Composer key={unsent.tries} placeholder={`Start a chat in ${project.name}…`} busy={starting} focusKey={id} initial={unsent.text} onSend={begin} />
        <section className="list-section" aria-label="Chats in this project">
          <div className="nav-heading">CHATS</div>
          {chats.length === 0 ? (
            <p className="quiet project-empty">No chats in this project yet. Start one above, or move a chat here from the project button in its header.</p>
          ) : (
            chats.map((c) => <ChatRow key={c.id} chat={c} />)
          )}
        </section>
      </div>
      <aside className="project-side" aria-label="What every chat in this project carries">
        <Instructions text={project.instructions} max={limits.instructions_max_chars} onSave={(instructions) => change({ instructions }, "The instructions were not saved")} />
        <Files projectId={id} files={files} limits={limits} onChanged={(next) => setLoaded({ state: "ready", view: { ...loaded.view, files: next } })} onProblem={failed} onRead={() => readProject(id).then(shown, (e) => failed(e, "The project could not be read again."))} />
        <Memory space={project.memory_space} onSave={(memory_space) => change({ memory_space }, "The memory space was not changed")} />
      </aside>
    </div>
  );
}

function ChatRow({ chat }: { chat: Chat }) {
  return (
    <Link to={`/chats/${chat.id}`} className="list-row">
      <span className={chat.title ? "list-title" : "list-title nav-untitled"}>{titleOf(chat)}</span>
      <span className="list-when">{chat.busy ? "answering" : changedOn(chat.active)}</span>
    </Link>
  );
}

type HeadMode = "closed" | "menu" | "rename" | "delete";

/** The project's name, with its menu: rename, archive or bring back, delete. */
function ProjectHead({
  view,
  onRename,
  onArchive,
  onDelete,
}: {
  view: ProjectView;
  onRename: (name: string) => Promise<boolean>;
  onArchive: (archived: boolean) => void;
  onDelete: () => Promise<void>;
}) {
  const { project, chats, limits } = view;
  const [mode, setMode] = useState<HeadMode>("closed");
  const [draft, setDraft] = useState(project.name);
  const [busy, setBusy] = useState(false);
  const wrap = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (mode !== "menu" && mode !== "delete") return;
    function down(e: MouseEvent) {
      if (wrap.current && !wrap.current.contains(e.target as Node)) setMode("closed");
    }
    function key(e: KeyboardEvent) {
      if (e.key === "Escape") setMode("closed");
    }
    document.addEventListener("mousedown", down);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("mousedown", down);
      document.removeEventListener("keydown", key);
    };
  }, [mode]);

  async function rename(e: FormEvent) {
    e.preventDefault();
    const name = draft.trim();
    if (name === "" || name === project.name) {
      setMode("closed");
      return;
    }
    setBusy(true);
    if (await onRename(name)) setMode("closed");
    setBusy(false);
  }

  return (
    <div className="project-head">
      <Link to="/projects" className="project-crumb">
        Projects
      </Link>
      <span className="project-crumb" aria-hidden="true">
        /
      </span>
      {mode === "rename" ? (
        <form className="rename-form" onSubmit={rename}>
          <label className="offscreen" htmlFor="project-rename">
            The project's name
          </label>
          <input id="project-rename" className="input" value={draft} maxLength={limits.name_max_chars || undefined} autoFocus disabled={busy} onChange={(e) => setDraft(e.target.value)} onFocus={(e) => e.target.select()} />
          <button type="submit" className="button button-primary" disabled={busy}>
            Save
          </button>
          <button type="button" className="button" disabled={busy} onClick={() => setMode("closed")}>
            Cancel
          </button>
        </form>
      ) : (
        <div className="title-wrap" ref={wrap}>
          <h1 className="page-title project-name">
            <button type="button" className="project-title" aria-haspopup="menu" aria-expanded={mode !== "closed"} onClick={() => setMode((m) => (m === "closed" ? "menu" : "closed"))}>
              <span className="project-title-text">{project.name}</span>
              {project.archived && <span className="project-tag">archived</span>}
              <Icon name="chevron" size={18} />
            </button>
          </h1>
          {mode === "menu" && (
            <div className="menu" role="menu">
              <button
                type="button"
                className="menu-item"
                role="menuitem"
                onClick={() => {
                  setDraft(project.name);
                  setMode("rename");
                }}
              >
                <Icon name="pencil" size={16} />
                Rename
              </button>
              <button
                type="button"
                className="menu-item"
                role="menuitem"
                onClick={() => {
                  setMode("closed");
                  onArchive(!project.archived);
                }}
              >
                <Icon name="projects" size={16} />
                {project.archived ? "Bring back" : "Archive"}
              </button>
              <button type="button" className="menu-item menu-item-danger" role="menuitem" onClick={() => setMode("delete")}>
                <Icon name="trash" size={16} />
                Delete
              </button>
            </div>
          )}
          {mode === "delete" && (
            <div className="menu" role="alertdialog" aria-label="Delete this project">
              <p className="menu-note">
                Delete this project, its instructions and its files?{" "}
                {chats.length === 1 ? "Its chat is kept, as an ordinary chat. " : chats.length > 1 ? `Its ${chats.length} chats are kept, as ordinary chats. ` : ""}
                It cannot be brought back.
              </p>
              <div className="menu-row">
                <button
                  type="button"
                  className="button button-danger project-danger"
                  disabled={busy}
                  onClick={async () => {
                    setBusy(true);
                    await onDelete();
                    setBusy(false);
                    setMode("closed");
                  }}
                >
                  Delete
                </button>
                <button type="button" className="button" disabled={busy} onClick={() => setMode("closed")}>
                  Keep
                </button>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

/** The project's instructions, which go at the start of every call of its chats. */
function Instructions({ text, max, onSave }: { text: string; max: number; onSave: (text: string) => Promise<boolean> }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(text);
  const [busy, setBusy] = useState(false);

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    if (await onSave(draft)) setEditing(false);
    setBusy(false);
  }

  return (
    <section className="project-panel" aria-labelledby="project-instructions-title">
      <div className="project-panel-head">
        <h2 className="project-panel-title" id="project-instructions-title">
          Instructions
        </h2>
        {!editing && (
          <button
            type="button"
            className="button project-small"
            onClick={() => {
              setDraft(text);
              setEditing(true);
            }}
          >
            <Icon name="pencil" size={14} />
            {text ? "Edit" : "Add"}
          </button>
        )}
      </div>
      {editing ? (
        <form className="project-stack" onSubmit={save}>
          <label className="offscreen" htmlFor="project-instructions-edit">
            The project's instructions
          </label>
          <textarea id="project-instructions-edit" className="textarea textarea-tall" value={draft} autoFocus disabled={busy} onChange={(e) => setDraft(e.target.value)} />
          <span className={max > 0 && draft.length > max ? "project-count is-over" : "project-count"}>
            {draft.length} of {max} characters
          </span>
          <div className="project-actions">
            <button type="submit" className="button button-primary project-small" disabled={busy || (max > 0 && draft.length > max)}>
              Save
            </button>
            <button type="button" className="button project-small" disabled={busy} onClick={() => setEditing(false)}>
              Cancel
            </button>
          </div>
        </form>
      ) : text ? (
        <p className="project-instructions">{text}</p>
      ) : (
        <p className="quiet project-hint">Every chat in this project starts with these. Tell it who it is for, what to keep in mind, or how to answer.</p>
      )}
    </section>
  );
}

/** The project's knowledge files: added, marked sensitive and taken off. */
function Files({
  projectId,
  files,
  limits,
  onChanged,
  onProblem,
  onRead,
}: {
  projectId: string;
  files: ProjectFile[];
  limits: ProjectView["limits"];
  onChanged: (files: ProjectFile[]) => void;
  onProblem: (e: unknown, fallback: string) => void;
  onRead: () => void;
}) {
  const picker = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [notes, setNotes] = useState<string[]>([]);
  const full = limits.max_files > 0 && files.length >= limits.max_files;

  async function add(e: ChangeEvent<HTMLInputElement>) {
    const chosen = Array.from(e.target.files ?? []);
    e.target.value = "";
    if (chosen.length === 0) return;
    setBusy(true);
    const said: string[] = [];
    for (const f of chosen) {
      if (limits.file_max_bytes > 0 && f.size > limits.file_max_bytes) {
        said.push(`${f.name} is ${sizeOf(f.size)}, and the most a project file may be is ${sizeOf(limits.file_max_bytes)}.`);
        continue;
      }
      try {
        const got = await addProjectFile(projectId, f);
        if (!got.isNew) said.push(`${f.name} is in the project already, as ${got.file.name}.`);
      } catch (err) {
        if (err instanceof ApiError && err.kind === "signed-out") {
          onProblem(err, "");
          break;
        }
        said.push(reason(err, `${f.name} could not be added. Check that the server is running.`));
      }
    }
    setNotes(said);
    setBusy(false);
    onRead();
  }

  async function mark(f: ProjectFile) {
    try {
      const got = await markProjectFile(projectId, f.id, !f.sensitive);
      onChanged(files.map((x) => (x.id === got.id ? { ...x, sensitive: got.sensitive } : x)));
    } catch (err) {
      onProblem(err, "The file's mark was not changed.");
    }
  }

  async function remove(f: ProjectFile) {
    try {
      await removeProjectFile(projectId, f.id);
      onChanged(files.filter((x) => x.id !== f.id));
      onRead();
    } catch (err) {
      onProblem(err, "The file was not taken off.");
    }
  }

  return (
    <section className="project-panel" aria-labelledby="project-files-title">
      <div className="project-panel-head">
        <h2 className="project-panel-title" id="project-files-title">
          Files
          <span className="project-count">
            {files.length} of {limits.max_files}
          </span>
        </h2>
        <button type="button" className="button project-small" disabled={busy || full} onClick={() => picker.current?.click()}>
          <Icon name="plus" size={14} />
          {busy ? "Adding…" : "Add files"}
        </button>
        <input ref={picker} type="file" multiple className="offscreen" tabIndex={-1} aria-hidden="true" accept={limits.text_extensions.join(",")} onChange={(e) => void add(e)} />
      </div>
      {files.length === 0 ? (
        <p className="quiet project-hint">Text files every chat in this project can read: notes, Markdown, code, CSV or JSON. They go into each call with the attachments.</p>
      ) : (
        <ul className="project-files">
          {files.map((f) => (
            <li key={f.id} className="project-file">
              <Icon name="file" size={16} />
              <span className="project-file-main">
                <span className="project-file-name" title={f.name}>
                  {f.name}
                </span>
                <span className="project-file-meta">
                  {sizeOf(f.size)} · {f.chars.toLocaleString()} characters
                </span>
              </span>
              <button
                type="button"
                className={f.sensitive ? "project-tag is-on" : "project-tag"}
                aria-pressed={f.sensitive}
                aria-label={`Sensitive: ${f.name}`}
                title={f.sensitive ? "Sensitive: only a local or trusted model reads it, and a chat that is given it stays with such models. Press to make it ordinary." : "Press to mark it sensitive: only a local or trusted model will read it."}
                onClick={() => void mark(f)}
              >
                sensitive
              </button>
              <button type="button" className="icon-button project-remove" aria-label={`Take ${f.name} off the project`} title="Take it off the project" onClick={() => void remove(f)}>
                <Icon name="trash" size={16} />
              </button>
            </li>
          ))}
        </ul>
      )}
      {notes.map((n) => (
        <p key={n} className="form-error project-hint" role="alert">
          {n}
        </p>
      ))}
      {limits.file_max_bytes > 0 && (
        <p className="quiet project-fine" title={`Files ending in ${limits.text_extensions.join(" ")}`}>
          Text files up to {sizeOf(limits.file_max_bytes)} each. PDFs and images are not taken yet.
        </p>
      )}
    </section>
  );
}

/** Where the project's chats keep their facts. */
function Memory({ space, onSave }: { space: string; onSave: (space: string) => Promise<boolean> }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(space);
  const [busy, setBusy] = useState(false);

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    if (await onSave(draft.trim())) setEditing(false);
    setBusy(false);
  }

  return (
    <section className="project-panel" aria-labelledby="project-memory-title">
      <div className="project-panel-head">
        <h2 className="project-panel-title" id="project-memory-title">
          Memory
        </h2>
        {!editing && (
          <button
            type="button"
            className="button project-small"
            onClick={() => {
              setDraft(space);
              setEditing(true);
            }}
          >
            <Icon name="pencil" size={14} />
            Change
          </button>
        )}
      </div>
      {editing ? (
        <form className="project-stack" onSubmit={save}>
          <label className="field-label" htmlFor="project-space">
            Space in your knowledge store
          </label>
          <input id="project-space" className="input" value={draft} autoFocus disabled={busy} placeholder="Empty keeps them with the rest" onChange={(e) => setDraft(e.target.value)} />
          <div className="project-actions">
            <button type="submit" className="button button-primary project-small" disabled={busy}>
              Save
            </button>
            <button type="button" className="button project-small" disabled={busy} onClick={() => setEditing(false)}>
              Cancel
            </button>
          </div>
        </form>
      ) : space ? (
        <p className="quiet project-hint">
          Facts from these chats are kept in the space <code className="project-code">{space}</code> of your knowledge store, and recall in them looks there first. A chat given sensitive content keeps its facts only in your sensitive space.
        </p>
      ) : (
        <p className="quiet project-hint">Facts from these chats are kept with the rest of your memory.</p>
      )}
    </section>
  );
}

/**
 * The project button in a chat's header: the project the chat is in, or a
 * button to put it in one. Its menu moves the chat into a project, out of
 * one, or opens the project.
 */
export function ChatProjectChip({ chatId }: { chatId: string }) {
  const { lost } = useChats();
  const sheet = useSheet();
  const project = useChatProject(chatId, lost);
  const { projects } = useProjects(lost);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState("");
  const wrap = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function down(e: MouseEvent) {
      if (wrap.current && !wrap.current.contains(e.target as Node)) setOpen(false);
    }
    function key(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    document.addEventListener("mousedown", down);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("mousedown", down);
      document.removeEventListener("keydown", key);
    };
  }, [open]);

  if (project === undefined) return null;

  async function move(projectId: string, into: boolean) {
    setBusy(true);
    setProblem("");
    try {
      const got = await moveChat(projectId, chatId, into);
      keepChatProject(chatId, got.project);
      void refreshProjects(lost);
      setOpen(false);
      sheet?.done();
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") lost();
      else setProblem("The chat was not moved. Check that the server is running.");
    } finally {
      setBusy(false);
    }
  }

  const choices = projects.filter((p) => !p.archived || p.id === project?.id);
  return (
    <div className={sheet ? "project-chip-wrap sheet-item" : "project-chip-wrap"} ref={wrap}>
      {sheet ? (
        // In the phone's ... sheet it is a row, and its menu opens under it (decision 0590).
        <SheetRow icon="projects" name="Project" state={projectState(project?.name)} expanded={open} onClick={() => setOpen((o) => !o)} />
      ) : (
        <button
          type="button"
          className={project ? "project-chip" : "project-chip project-chip-empty"}
          aria-haspopup="menu"
          aria-expanded={open}
          aria-label={project ? `In the project ${project.name}. Move this chat` : "Move this chat into a project"}
          title={project ? `This chat is in ${project.name}: every call carries its instructions and files` : "Move this chat into a project"}
          onClick={() => setOpen((o) => !o)}
        >
          <Icon name="projects" size={14} />
          <span className="project-chip-name">{project ? project.name : "Add to project"}</span>
        </button>
      )}
      {problem && (
        <span className="header-note" role="alert">
          {problem}
        </span>
      )}
      {open && (
        <div className="menu project-menu" role="menu">
          <p className="menu-note">{project ? "Move this chat to another project, or take it out." : "Put this chat in a project. Its next message carries the project's instructions and files."}</p>
          {choices.length === 0 && <p className="menu-note">You have no projects yet.</p>}
          {choices.map((p) => (
            <button key={p.id} type="button" className="menu-item" role="menuitemradio" aria-checked={p.id === project?.id} disabled={busy || p.id === project?.id} onClick={() => void move(p.id, true)}>
              <Icon name={p.id === project?.id ? "check" : "projects"} size={16} />
              <span className="project-menu-name">{p.name}</span>
            </button>
          ))}
          {project && (
            <>
              <Link to={`/projects/${project.id}`} className="menu-item" role="menuitem" onClick={() => setOpen(false)}>
                <Icon name="caret" size={16} />
                Open {project.name}
              </Link>
              <button type="button" className="menu-item menu-item-danger" role="menuitem" disabled={busy} onClick={() => void move(project.id, false)}>
                <Icon name="close" size={16} />
                Take it out of the project
              </button>
            </>
          )}
          <Link to="/projects" className="menu-item" role="menuitem" onClick={() => setOpen(false)}>
            <Icon name="plus" size={16} />
            {choices.length === 0 ? "Make a project" : "All projects"}
          </Link>
        </div>
      )}
    </div>
  );
}
