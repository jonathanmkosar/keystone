// The person's projects (P9-01), and the project each open chat is in, read
// from the server and kept outside the screens, as attached.ts keeps a
// chat's attached chats, so the sidebar, the projects screen and a chat's
// header show the same. docs/architecture/projects.md has the routes.

import { useEffect, useSyncExternalStore } from "react";
import { ApiError, chatProject, listProjects, type Project, type ProjectLimits, type ProjectSummary } from "./api";

export type ProjectList = {
  status: "loading" | "ready" | "failed";
  /** Archived ones included, the one changed last first. */
  projects: ProjectSummary[];
  limits?: ProjectLimits;
};

let list: ProjectList = { status: "loading", projects: [] };
let asked = false;
// The project each chat is in: null for none, absent until it is read.
const byChat = new Map<string, Project | null>();
const listeners = new Set<() => void>();

function tell() {
  for (const l of listeners) l();
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** Reads the list of projects from the server again. */
export function refreshProjects(lost: () => void): Promise<void> {
  asked = true;
  return listProjects().then(
    (got) => {
      list = { status: "ready", projects: got.projects, limits: got.limits };
      tell();
    },
    (e) => {
      if (e instanceof ApiError && e.kind === "signed-out") lost();
      else if (list.status !== "ready") {
        list = { ...list, status: "failed" };
        tell();
      }
    },
  );
}

/** The person's projects. They are read the first time a screen asks. */
export function useProjects(lost: () => void): ProjectList {
  const value = useSyncExternalStore(subscribe, () => list);
  useEffect(() => {
    if (!asked) void refreshProjects(lost);
  }, [lost]);
  return value;
}

/** Keeps what the server said of the project a chat is in. */
export function keepChatProject(chatId: string, project: Project | undefined) {
  byChat.set(chatId, project ?? null);
  tell();
}

/** The project a chat is in: null for none, undefined until it is read. */
export function useChatProject(chatId: string, lost: () => void): Project | null | undefined {
  const value = useSyncExternalStore(subscribe, () => byChat.get(chatId));
  useEffect(() => {
    if (!chatId) return;
    let live = true;
    chatProject(chatId).then(
      (got) => {
        if (live) keepChatProject(chatId, got.project);
      },
      (e) => {
        if (e instanceof ApiError && e.kind === "signed-out") lost();
        // Otherwise nothing is drawn for it.
      },
    );
    return () => {
      live = false;
    };
  }, [chatId, lost]);
  return value;
}

/** When a project was changed, as the screens say it. */
export function changedOn(at: string): string {
  const d = new Date(at);
  if (Number.isNaN(d.getTime())) return "";
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return d.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  const year = d.getFullYear() === now.getFullYear() ? undefined : "numeric";
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric", year });
}

/** A number of bytes for a person. */
export function sizeOf(n: number): string {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${Math.round(n / (1 << 10))} KB`;
  return `${n} bytes`;
}
