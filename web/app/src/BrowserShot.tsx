// Browser use (P10-03): the screenshot the agent's own browser took, shown
// under its step. The server names it in the result's first lines, which it
// writes itself, before any of the page's words: "Screenshot: <id>" right
// after the outside-content line (internal/tools/browser). A line further
// down, in a page's own text, is never read. The image is the person's own,
// served by GET /api/v1/browser/shots/{id}.

import "./browser.css";

/** The tools whose results may name a screenshot. */
export const browserTools = new Set(["browser_open", "browser_read", "browser_click", "browser_type", "browser_screenshot", "browser_back"]);

const shotLine = /^Screenshot: ([0-9a-f]{32})$/;

/** The id of the screenshot a browser step's result names, or "" for none. */
export function shotOf(name: string, result: string | undefined, failed?: boolean): string {
  if (!browserTools.has(name) || failed || result === undefined) return "";
  // Only the header: the mark's line, then the server's own lines, up to
  // the first blank line.
  const head = result.split("\n\n", 1)[0].split("\n");
  if (head.length < 2 || !head[0].startsWith("[Outside content]")) return "";
  const m = shotLine.exec(head[1]);
  return m ? m[1] : "";
}

/** The address and title the header names, for the image's words. */
function pageOf(result: string): string {
  const m = /The browser shows (?:the page “([^”]*)”|a page with no title) at (\S+)\./.exec(result.split("\n", 1)[0]);
  if (!m) return "the agent's browser";
  return m[1] ? `“${m[1]}”` : m[2];
}

export function BrowserShot({ name, result, failed }: { name: string; result?: string; failed?: boolean }) {
  const id = shotOf(name, result, failed);
  if (!id || result === undefined) return null;
  const src = `/api/v1/browser/shots/${id}`;
  const what = pageOf(result);
  return (
    <a className="browser-shot" href={src} target="_blank" rel="noopener noreferrer" title="Open the screenshot at full size">
      <img src={src} alt={`Screenshot of ${what} in the agent's browser`} loading="lazy" decoding="async" />
    </a>
  );
}
