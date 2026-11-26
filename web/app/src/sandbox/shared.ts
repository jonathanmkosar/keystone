// What both of the sandbox's renderers share. They run inside an
// artifact's sandboxed frame, a page of no origin that may fetch nothing
// (internal/artifacts/sandbox.go), and are built as classic scripts into
// web/dist/sandbox/ by vite.sandbox.config.ts, never into the main page.

export type Source = { type: string; title: string; content: string; theme: "dark" | "light" };

/** The artifact the server put in the page, as JSON. */
export function readSource(): Source | undefined {
  const el = document.getElementById("ks-artifact");
  if (!el?.textContent) return undefined;
  try {
    return JSON.parse(el.textContent) as Source;
  } catch {
    return undefined;
  }
}

/** The element the artifact is drawn in. */
export function root(): HTMLElement {
  let el = document.getElementById("ks-root");
  if (!el) {
    el = document.createElement("div");
    el.id = "ks-root";
    document.body.appendChild(el);
  }
  return el;
}

/** Shows why the artifact could not be drawn, in place of it. */
export function showProblem(title: string, detail: string) {
  const box = document.createElement("div");
  box.className = "ks-note";
  const head = document.createElement("strong");
  head.textContent = title;
  const body = document.createElement("pre");
  body.className = "ks-text";
  body.style.padding = "8px 0 0";
  body.textContent = detail;
  box.append(head, body);
  const el = root();
  el.replaceChildren(box);
}

/** The text of anything thrown. */
export function messageOf(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}
