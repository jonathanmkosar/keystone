// The Mermaid renderer of the sandbox (P9-03): draws a Mermaid artifact's
// diagram as SVG, with Mermaid's strict security level, so the diagram's
// text cannot add script or links that run. Decision 0421.
//
// It also draws a diagram of a reply (P9-04, decision 0541). That page
// (internal/visuals) holds this script and no diagram: its body says
// data-ks-inline, and the script then waits for the page that frames it to
// hand it a diagram's source and the theme's colours, and answers "ready",
// "drawn" or why it could not. It reads nothing else of a message, and
// tells the page nothing else.

import mermaid from "mermaid";
import { frameMessage, mermaidTheme, readDrawMessage, type FrameMessage } from "../visuals";
import { messageOf, readSource, root, showProblem } from "./shared";

async function run() {
  const source = readSource();
  if (!source) {
    showProblem("There is nothing to draw.", "The page holds no artifact.");
    return;
  }
  mermaid.initialize({ startOnLoad: false, securityLevel: "strict", theme: source.theme === "dark" ? "dark" : "default" });
  try {
    const { svg } = await mermaid.render("ks-diagram", source.content.trim());
    const el = root();
    el.innerHTML = svg;
    el.querySelector("svg")?.setAttribute("aria-label", source.title);
  } catch (e) {
    showProblem("The diagram could not be drawn.", messageOf(e));
  }
}

/** Draws what the framing page sends, each time it sends. */
function inline() {
  let asked = 0;
  const tell = (m: FrameMessage) => window.parent.postMessage(frameMessage(m), "*");
  window.addEventListener("message", (e) => {
    // Only the page that frames this one is heard.
    if (e.source !== window.parent) return;
    const ask = readDrawMessage(e.data);
    if (!ask) return;
    const n = ++asked;
    void (async () => {
      const id = `ks-diagram-${n}`;
      try {
        mermaid.initialize({ startOnLoad: false, securityLevel: "strict", theme: "base", themeVariables: mermaidTheme(ask.colours, ask.theme === "dark") });
        const { svg } = await mermaid.render(id, ask.source.trim());
        if (n !== asked) return;
        // The page's own style fits the drawing inside the frame, whole.
        root().innerHTML = svg;
        tell({ kind: "drawn" });
      } catch (err) {
        // Mermaid leaves what it was drawing in the page when it fails.
        document.getElementById(id)?.remove();
        document.getElementById("d" + id)?.remove();
        if (n !== asked) return;
        root().replaceChildren();
        tell({ kind: "problem", text: messageOf(err) });
      }
    })();
  });
  tell({ kind: "ready" });
}

if (document.body.hasAttribute("data-ks-inline")) inline();
else void run();
