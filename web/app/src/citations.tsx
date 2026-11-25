// Numbered citations. web_search and web_fetch give each source they read
// a number, and the model cites it in its reply as [1] or [2]. This file
// reads a reply's sources back out of its web steps' results, and draws a
// [n] that names one as a small link to it.
//
// The form of a result is the server's, in internal/tools/web/cite.go, and
// docs/architecture/web-search.md: a first paragraph that opens with
// "[Outside content]", then blocks of "[n] title", the address on the next
// line, and a search result's text. Reading stops at the first block that
// is not a source, so a page's own text can never add a source.

import { createContext, useContext, type ReactNode } from "react";
import "./citations.css";

export type Source = { n: number; title: string; url: string };

/** The tools whose results are numbered sources. */
export const webTools = new Set(["web_search", "web_fetch"]);

const outsideMark = "[Outside content]";
const head = /^\[(\d{1,4})\] (.+)$/;
const address = /^https?:\/\/\S+$/;

/** The numbered sources in one web tool's result. */
export function sourcesOfText(result: string): Source[] {
  if (!result.startsWith(outsideMark)) return [];
  const out: Source[] = [];
  for (const block of result.replace(/\r\n/g, "\n").split("\n\n").slice(1)) {
    const lines = block.split("\n");
    const h = lines.length >= 2 ? head.exec(lines[0]) : null;
    if (!h || !address.test(lines[1])) break;
    out.push({ n: Number(h[1]), title: h[2], url: lines[1] });
  }
  return out;
}

type ToolStep = { kind: string; name?: string; result?: string; failed?: boolean };

/** Every source a reply's web steps read, by number. The first one given a number keeps it. */
export function sourcesOf(parts: { kind: string; steps?: ToolStep[] }[]): Map<number, Source> {
  const found = new Map<number, Source>();
  for (const p of parts) {
    if (p.kind !== "steps" || !p.steps) continue;
    for (const s of p.steps) {
      if (s.kind !== "tool" || !webTools.has(s.name ?? "") || s.failed || s.result === undefined) continue;
      for (const src of sourcesOfText(s.result)) if (!found.has(src.n)) found.set(src.n, src);
    }
  }
  return found;
}

const SourcesContext = createContext<Map<number, Source>>(new Map());

/** Gives the replies' text below it the sources their [n] may name. */
export function CitationSources({ sources, children }: { sources: Map<number, Source>; children: ReactNode }) {
  return <SourcesContext.Provider value={sources}>{children}</SourcesContext.Provider>;
}

function host(url: string): string {
  try {
    return new URL(url).host.replace(/^www\./, "");
  } catch {
    return url;
  }
}

/**
 * One [n] in a reply: a small link to its source, with the title and the
 * site on hover, or the text as it was when no source has that number.
 */
export function Cite({ n, raw }: { n: number; raw: string }) {
  const src = useContext(SourcesContext).get(n);
  if (!src) return <>{raw}</>;
  return (
    <a className="cite" href={src.url} target="_blank" rel="noopener noreferrer" title={`${src.title} (${host(src.url)})`} aria-label={`Source ${n}: ${src.title}`}>
      {n}
    </a>
  );
}
