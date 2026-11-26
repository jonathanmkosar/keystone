// The Mermaid renderer of the sandbox (P9-03): draws a Mermaid artifact's
// diagram as SVG, with Mermaid's strict security level, so the diagram's
// text cannot add script or links that run. Decision 0421.

import mermaid from "mermaid";
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

void run();
