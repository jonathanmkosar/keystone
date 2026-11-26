// Artifacts (P9-03): the server's calls, which artifact is open beside a
// chat, and the words the screen reads out of the artifact tools' results.
// The routes are in docs/architecture/artifacts.md. Which artifact is open
// is kept here, outside the screens, as Inspector.tsx keeps its turn.

import { useEffect, useState, useSyncExternalStore } from "react";
import { call } from "./api";

export type ArtifactType = "code" | "html" | "react" | "svg" | "mermaid" | "markdown";

export type Artifact = {
  id: string;
  chat_id: string;
  type: ArtifactType;
  language?: string;
  title: string;
  latest: number;
  created: string;
  updated: string;
  sensitive: boolean;
  link: string;
};

export type ArtifactVersion = {
  artifact_id: string;
  version: number;
  title: string;
  size: number;
  sha256: string;
  origin: "model" | "restore";
  restored_from?: number;
  sensitive: boolean;
  created: string;
};

export type ArtifactOne = { artifact: Artifact; versions: ArtifactVersion[]; sandbox: string; notice?: string };
export type VersionRead = { artifact: Artifact; version: ArtifactVersion; content: string };
export type DiffLine = { op: "same" | "add" | "remove"; text: string; from?: number; to?: number };
export type Comparison = { from: number; to: number; lines: DiffLine[]; added: number; removed: number };

/** The tools a model makes, changes and reads artifacts with. */
export const artifactTools = new Set(["create_artifact", "update_artifact", "read_artifact"]);

const base = "/api/v1/artifacts";
const onePath = (id: string) => `${base}/${encodeURIComponent(id)}`;
const versionPath = (id: string, n: number) => `${onePath(id)}/versions/${n}`;

export async function chatArtifacts(chatId: string): Promise<Artifact[]> {
  const got = (await call("GET", `/api/v1/chats/${encodeURIComponent(chatId)}/artifacts`)) as { artifacts: Artifact[] };
  return got.artifacts;
}

export async function allArtifacts(): Promise<Artifact[]> {
  const got = (await call("GET", base)) as { artifacts: Artifact[] };
  return got.artifacts;
}

export async function readArtifact(id: string): Promise<ArtifactOne> {
  return (await call("GET", onePath(id))) as ArtifactOne;
}

export async function readVersion(id: string, n: number): Promise<VersionRead> {
  return (await call("GET", versionPath(id, n))) as VersionRead;
}

export async function compareVersions(id: string, from: number, to: number): Promise<Comparison> {
  return (await call("GET", `${onePath(id)}/compare?from=${from}&to=${to}`)) as Comparison;
}

export async function restoreVersion(id: string, n: number): Promise<VersionRead> {
  return (await call("POST", `${versionPath(id, n)}/restore`)) as VersionRead;
}

/** The page one version is shown in, inside the sandboxed frame. */
export function sandboxURL(id: string, n: number, theme: "dark" | "light"): string {
  return `${versionPath(id, n)}/sandbox?theme=${theme}`;
}

/** One version as a file to save. */
export function downloadURL(id: string, n: number): string {
  return `${versionPath(id, n)}/download`;
}

/** The publish link as a whole address on this Keystone. */
export function publishLink(a: Artifact): string {
  return new URL(a.link, window.location.origin).toString();
}

/** What an artifact is, in a few words: "HTML page", "Python code". */
export function typeLabel(type: ArtifactType | string, language?: string): string {
  switch (type) {
    case "code":
      return language && language !== "text" ? `${language[0].toUpperCase()}${language.slice(1)} code` : "Code";
    case "html":
      return "HTML page";
    case "react":
      return "React component";
    case "svg":
      return "SVG picture";
    case "mermaid":
      return "Mermaid diagram";
    case "markdown":
      return "Document";
  }
  return "Artifact";
}

/** Reads the artifact and version out of a create or update tool's result: "(artifact <id>, version <n>". */
export function artifactOfResult(result: string | undefined): { id: string; version: number } | undefined {
  const m = /\(artifact ([0-9a-f]{16}), version (\d+)/.exec(result ?? "");
  return m ? { id: m[1], version: Number(m[2]) } : undefined;
}

// ---------------------------------------------------------------------------
// Which artifact is open, outside the screens.
// ---------------------------------------------------------------------------

type Open = { chatId: string; artifactId: string; version?: number; asked: number };
let open: Open | undefined;
let asks = 0;
const listeners = new Set<() => void>();

function tell() {
  for (const l of listeners) l();
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** Opens an artifact in the panel beside its chat, at a version or the newest. */
export function openArtifact(chatId: string, artifactId: string, version?: number) {
  open = { chatId, artifactId, version, asked: ++asks };
  tell();
}

/** Closes the panel. */
export function closeArtifact() {
  open = undefined;
  tell();
}

/** The artifact open beside this chat, if any. */
export function useOpenArtifact(chatId: string): Open | undefined {
  const o = useSyncExternalStore(subscribe, () => open);
  return o && o.chatId === chatId ? o : undefined;
}

/** The theme the page wears now: the person's choice, or the system's. */
function currentTheme(): "dark" | "light" {
  const set = document.documentElement.getAttribute("data-theme");
  if (set === "dark" || set === "light") return set;
  return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

/** The page's theme, followed as it changes. */
export function usePageTheme(): "dark" | "light" {
  const [theme, setTheme] = useState(currentTheme);
  useEffect(() => {
    const again = () => setTheme(currentTheme());
    const watch = new MutationObserver(again);
    watch.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    const media = window.matchMedia("(prefers-color-scheme: light)");
    media.addEventListener("change", again);
    return () => {
      watch.disconnect();
      media.removeEventListener("change", again);
    };
  }, []);
  return theme;
}

/** "3.2 kB" */
export function size(n: number): string {
  if (n < 1000) return `${n} bytes`;
  if (n < 1e6) return `${(n / 1000).toFixed(1).replace(/\.0$/, "")} kB`;
  return `${(n / 1e6).toFixed(1).replace(/\.0$/, "")} MB`;
}

/** When a version was written, for a person. */
export function when(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}
