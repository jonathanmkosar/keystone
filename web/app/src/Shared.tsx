// The shared page (P10-04): /share/<token> shows one chat, read-only, to
// someone who has not signed in. main.tsx draws it in place of the app, so
// nothing asks who is signed in and no other call is made. It reads the
// link with credentials omitted, so the browser sends no session cookie
// with it, and the server's door reads none and sets none.
//
// Everything is drawn as text: a reply's Markdown through Markdown.tsx,
// which builds React elements and never sets HTML, and an artifact as its
// source in a code block, never run. It imports nothing from api.ts.
// Built from the design tokens and the chat's classes; there is no mockup
// of it, so it is provisional. docs/architecture/share.md.

import { useEffect, useState } from "react";
import { Icon, type IconName } from "./Icon";
import { Mark } from "./Mark";
import { CopyButton, Markdown } from "./Markdown";
import "./share.css";

const sharedPath = /^\/share\/([^/]+)\/?$/;

/** Whether an address is a share link's page. */
export function isSharedPath(path: string): boolean {
  return sharedPath.test(path);
}

type Part =
  | { kind: "text"; text: string }
  | { kind: "note"; text: string }
  | { kind: "image"; image?: string; media_type?: string }
  | { kind: "file"; name: string }
  | { kind: "tool"; name: string; failed?: boolean }
  | { kind: "artifact"; name: string; artifact: { title: string; type: string; language?: string; version: number; content: string } };

type Entry = { role: "person" | "reply"; parts: Part[] };

type View = { title: string; view: "live" | "snapshot"; shared: string; as_of?: string; entries: Entry[] };

type Page =
  | { kind: "loading" }
  | { kind: "ready"; view: View }
  | { kind: "refused"; status: number; code: string; said: string }
  | { kind: "unreachable" };

// Words for the tools a reply used. The page is never told what a tool was
// sent or gave back.
const toolWords: Record<string, [IconName, string]> = {
  web_search: ["web", "Searched the web"],
  web_fetch: ["web", "Read a web page"],
  search_chats: ["search", "Searched earlier chats"],
  read_chat: ["chats", "Read an earlier chat"],
  open_attached_chat: ["chatlink", "Read an attached chat"],
  current_time: ["scheduled", "Checked the date and time"],
  knowledge_search: ["memory", "Searched what it remembers"],
  knowledge_read_page: ["memory", "Read a page of what it remembers"],
  knowledge_read_source: ["memory", "Read a source of what it remembers"],
  read_artifact: ["artifacts", "Read an artifact"],
  create_artifact: ["artifacts", "Made an artifact"],
  update_artifact: ["artifacts", "Changed an artifact"],
  start_background_task: ["agents", "Started a background task"],
  load_tools: ["plug", "Loaded tools"],
  gmail_search: ["mail", "Searched email"],
  gmail_read: ["mail", "Read an email"],
  gmail_draft: ["mail", "Wrote a draft email"],
  gmail_send: ["mail", "Sent an email"],
  calendar_list_events: ["calendar", "Read a calendar"],
  calendar_get_event: ["calendar", "Read an event"],
  calendar_create_event: ["calendar", "Made an event"],
  calendar_update_event: ["calendar", "Changed an event"],
  calendar_delete_event: ["calendar", "Deleted an event"],
};

const when = new Intl.DateTimeFormat(undefined, { day: "numeric", month: "long", year: "numeric", hour: "numeric", minute: "2-digit" });

function at(iso: string | undefined): string {
  if (!iso) return "";
  const t = new Date(iso);
  return Number.isNaN(t.getTime()) ? "" : when.format(t);
}

const refusals: Record<string, string> = {
  revoked: "Its owner turned this link off.",
  expired: "This link has expired.",
  sensitive: "This chat now holds content from a sensitive space, and such a chat is never shown through a link.",
  incognito: "This chat is incognito, and an incognito chat is never shown through a link.",
  not_found: "This link opens nothing. It may have been typed wrong, or its chat deleted.",
};

/** The shared page, alone. */
export function SharedPage() {
  const token = sharedPath.exec(window.location.pathname)?.[1] ?? "";
  const [page, setPage] = useState<Page>({ kind: "loading" });

  useEffect(() => {
    let live = true;
    (async () => {
      let res: Response;
      try {
        res = await fetch(`/api/v1/shared/${encodeURIComponent(token)}`, { method: "GET", credentials: "omit", headers: { Accept: "application/json" } });
      } catch {
        if (live) setPage({ kind: "unreachable" });
        return;
      }
      let body: unknown = undefined;
      try {
        if ((res.headers.get("Content-Type") ?? "").includes("application/json")) body = await res.json();
      } catch {
        body = undefined;
      }
      if (!live) return;
      if (res.ok && body && typeof body === "object" && Array.isArray((body as View).entries)) {
        setPage({ kind: "ready", view: body as View });
        return;
      }
      const err = (body ?? {}) as { code?: unknown; error?: unknown };
      if (res.status >= 400 && res.status < 500 && typeof err.code === "string") {
        setPage({ kind: "refused", status: res.status, code: err.code, said: typeof err.error === "string" ? err.error : "" });
        return;
      }
      setPage({ kind: "unreachable" });
    })();
    return () => {
      live = false;
    };
  }, [token]);

  useEffect(() => {
    document.title = page.kind === "ready" && page.view.title ? `${page.view.title} · Keystone` : "A shared chat · Keystone";
  }, [page]);

  return (
    <div className="shared-page">
      <header className="shared-head">
        <span className="shared-brand">
          <Mark size={22} />
          <span className="wordmark">Keystone</span>
        </span>
        <span className="shared-note">A read-only copy of one chat, shared by the person it belongs to.</span>
      </header>
      <main className="shared-body">
        {page.kind === "loading" && (
          <p className="quiet" aria-busy="true">
            Loading.
          </p>
        )}
        {page.kind === "unreachable" && <p className="quiet">The chat could not be read. Keystone may not be running; try again later.</p>}
        {page.kind === "refused" && (
          <div className="shared-refused" role="alert">
            <h1 className="shared-title">{page.status === 410 ? "This link is closed" : page.status === 403 ? "This chat is not shown" : "Nothing here"}</h1>
            <p className="quiet">{refusals[page.code] ?? page.said ?? refusals.not_found}</p>
          </div>
        )}
        {page.kind === "ready" && <Chat token={token} view={page.view} />}
      </main>
    </div>
  );
}

function Chat({ token, view }: { token: string; view: View }) {
  const asOf = at(view.as_of);
  return (
    <>
      <h1 className="shared-title">{view.title || "An untitled chat"}</h1>
      <p className="shared-when">
        {view.view === "snapshot" ? `As it was on ${asOf || at(view.shared)}.` : asOf ? `As it is now. The last message was written on ${asOf}.` : "As it is now."}
      </p>
      {view.entries.length === 0 && <p className="quiet">The chat has no messages.</p>}
      <div className="conversation shared-conversation">
        {view.entries.map((e, i) => (e.role === "person" ? <Said key={i} token={token} parts={e.parts} /> : <Reply key={i} parts={e.parts} />))}
      </div>
      <p className="shared-foot">Only this chat is shown. Nothing here can be changed.</p>
    </>
  );
}

function Said({ token, parts }: { token: string; parts: Part[] }) {
  const text = parts
    .filter((p): p is Extract<Part, { kind: "text" }> => p.kind === "text")
    .map((p) => p.text)
    .join("\n\n");
  return (
    <div className="said-wrap">
      <div className="said">
        {parts.some((p) => p.kind === "image" || p.kind === "file") && (
          <div className="said-files">
            {parts.map((p, i) => {
              if (p.kind === "image" && p.image) {
                return <img key={i} className="shared-image" src={`/api/v1/shared/${encodeURIComponent(token)}/images/${encodeURIComponent(p.image)}`} alt="An image from the chat" />;
              }
              if (p.kind === "image") {
                return (
                  <span key={i} className="said-file">
                    An image, not shown through this link
                  </span>
                );
              }
              if (p.kind === "file") {
                return (
                  <span key={i} className="said-file" title="Its contents are not shown through this link">
                    {p.name}
                  </span>
                );
              }
              return null;
            })}
          </div>
        )}
        {text}
      </div>
    </div>
  );
}

function Reply({ parts }: { parts: Part[] }) {
  // Tool steps in a row are drawn as one group, as the chat draws them.
  const groups: (Part | Extract<Part, { kind: "tool" }>[])[] = [];
  for (const p of parts) {
    const last = groups.at(-1);
    if (p.kind === "tool" && Array.isArray(last)) last.push(p);
    else groups.push(p.kind === "tool" ? [p] : p);
  }
  const text = parts
    .filter((p): p is Extract<Part, { kind: "text" }> => p.kind === "text")
    .map((p) => p.text)
    .join("\n\n");
  return (
    <div className="reply">
      {groups.map((g, i) => {
        if (Array.isArray(g)) {
          return (
            <div key={i} className="steps">
              {g.map((t, j) => {
                const [icon, words] = toolWords[t.name] ?? ["tool", `Used ${t.name}`];
                return (
                  <div key={j} className="step step-tool">
                    <Icon name={icon} size={16} />
                    <span className="step-label" title={t.name}>
                      {words}
                    </span>
                    {t.failed && <span className="step-tag">failed</span>}
                  </div>
                );
              })}
            </div>
          );
        }
        if (g.kind === "text") {
          return (
            <div key={i} className="reply-text">
              <Markdown text={g.text} />
            </div>
          );
        }
        if (g.kind === "note") {
          return (
            <p key={i} className="reply-note">
              {g.text}
            </p>
          );
        }
        if (g.kind === "artifact") {
          const a = g.artifact;
          return (
            <div key={i} className="shared-artifact">
              <div className="shared-artifact-head">
                <Icon name="artifacts" size={16} />
                <span className="shared-artifact-title">{a.title}</span>
                <span className="shared-artifact-kind">{`${a.language || a.type} · version ${a.version} · its source, not run`}</span>
                <CopyButton text={a.content} />
              </div>
              <pre className="shared-artifact-source">
                <code>{a.content}</code>
              </pre>
            </div>
          );
        }
        return null;
      })}
      {text !== "" && (
        <div className="reply-actions">
          <CopyButton text={text} />
        </div>
      )}
    </div>
  );
}
