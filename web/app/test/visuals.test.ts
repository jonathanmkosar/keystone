// The proof of P9-04's page side: which fenced blocks are drawn, what an
// unfinished or oversized one becomes, and that the page and a diagram's
// frame read nothing of each other's messages but what they should. Run by
// Node's own test runner (decision 0501).

import assert from "node:assert/strict";
import { test } from "node:test";
import {
  byteLength,
  diagramChannel,
  diagramColours,
  diagramTitle,
  diagramTokens,
  drawMessage,
  durationMs,
  frameMessage,
  isColour,
  mermaidTheme,
  planBlock,
  readDrawMessage,
  readFrameMessage,
  readVisualSettings,
  visualKind,
  type VisualSettings,
} from "../src/visuals.ts";

const shipped = [
  { key: "visuals.draw", value: "true" },
  { key: "visuals.max_block_bytes", value: "20000" },
  { key: "visuals.chart_max_series", value: "6" },
  { key: "visuals.chart_max_points", value: "200" },
  { key: "visuals.chart_label_max_chars", value: "80" },
  { key: "visuals.chart_height", value: "320" },
  { key: "visuals.diagram_height", value: "360" },
  { key: "visuals.diagram_wait", value: "20s" },
];

const settings = readVisualSettings(shipped) as VisualSettings;

const dark: Record<string, string> = {
  page: "#15191a",
  raised: "#1d2324",
  sunk: "#0f1213",
  line: "#2e3637",
  strong: "#737e7f",
  ink: "#f1ece0",
  muted: "#a8aba3",
  accent: "#e0915a",
  soft: "#3b2412",
  softink: "#e8a878",
  ok: "#8fc7a1",
  link: "#86bfe6",
  err: "#f0907f",
};

test("the settings are read from the registry's values, and one that does not read draws nothing", () => {
  assert.deepEqual(settings, {
    draw: true,
    maxBlockBytes: 20000,
    chart: { maxSeries: 6, maxPoints: 200, labelMaxChars: 80 },
    chartHeight: 320,
    diagramHeight: 360,
    diagramWaitMs: 20000,
  });
  for (const s of shipped) {
    assert.equal(readVisualSettings(shipped.filter((o) => o.key !== s.key)), undefined, `without ${s.key}`);
    assert.equal(readVisualSettings(shipped.map((o) => (o.key === s.key ? { ...o, value: "many" } : o))), undefined, `${s.key} = many`);
  }
  assert.equal(readVisualSettings(shipped.map((o) => (o.key === "visuals.chart_height" ? { ...o, value: "10" } : o))), undefined, "a height too small to draw in");
  assert.equal(readVisualSettings(shipped.map((o) => (o.key === "visuals.diagram_wait" ? { ...o, value: "0s" } : o))), undefined, "no wait at all");
  assert.equal(durationMs("1m30s"), 90000);
  assert.equal(durationMs("soon"), undefined);
});

test("only chart and mermaid blocks are visuals", () => {
  assert.equal(visualKind("chart"), "chart");
  assert.equal(visualKind("Mermaid"), "diagram");
  for (const lang of ["", "json", "js", "charts", "svg", "html"]) assert.equal(visualKind(lang), undefined, lang);
});

test("a whole block is drawn, and anything else is a code block", () => {
  assert.deepEqual(planBlock({ lang: "chart", text: '{"type":"bar"}', closed: true }, settings, false), { draw: "chart", source: '{"type":"bar"}' });
  assert.deepEqual(planBlock({ lang: "mermaid", text: "graph LR\n A-->B", closed: true }, settings, false), { draw: "diagram", source: "graph LR\n A-->B" });
  assert.deepEqual(planBlock({ lang: "python", text: "print(1)", closed: true }, settings, false), { draw: "code" });
  assert.deepEqual(planBlock({ lang: "", text: "{}", closed: true }, settings, true), { draw: "code" });
});

test("switched off, or with no settings, every block is a code block", () => {
  const off = { ...settings, draw: false };
  for (const lang of ["chart", "mermaid"]) {
    assert.deepEqual(planBlock({ lang, text: "x", closed: true }, off, false), { draw: "code" });
    assert.deepEqual(planBlock({ lang, text: "x", closed: true }, undefined, false), { draw: "code" });
    assert.deepEqual(planBlock({ lang, text: "x", closed: false }, off, true), { draw: "code" });
  }
});

test("a block still being written is a placeholder, and draws once it is closed", () => {
  const half = '{"type":"bar","title":"Kayak hi';
  assert.deepEqual(planBlock({ lang: "chart", text: half, closed: false }, settings, true), { draw: "pending", what: "chart" });
  assert.deepEqual(planBlock({ lang: "mermaid", text: "graph LR\n A--", closed: false }, settings, true), { draw: "pending", what: "diagram" });
  // Closed while the reply still streams: it is whole, so it draws.
  assert.equal(planBlock({ lang: "chart", text: half + 'res"}', closed: true }, settings, true).draw, "chart");
  // The reply ended and the block never closed: its source, with a note.
  assert.deepEqual(planBlock({ lang: "chart", text: half, closed: false }, settings, false), { draw: "problem", what: "chart", reason: "The reply ended before this chart was finished." });
});

test("a block larger than the setting, or empty, is shown as its source with a note", () => {
  const small = { ...settings, maxBlockBytes: 10 };
  assert.deepEqual(planBlock({ lang: "mermaid", text: "graph LR\n A-->B", closed: true }, small, false), {
    draw: "problem",
    what: "diagram",
    reason: "This diagram is 15 bytes, and Keystone draws one of up to 10.",
  });
  assert.equal(planBlock({ lang: "chart", text: "ééééé", closed: true }, small, false).draw, "chart", "ten bytes is within ten");
  assert.equal(planBlock({ lang: "chart", text: "éééééé", closed: true }, small, false).draw, "problem");
  assert.equal(byteLength("aé€😀"), 1 + 2 + 3 + 4);
  assert.deepEqual(planBlock({ lang: "chart", text: " \n ", closed: true }, settings, false), { draw: "problem", what: "chart", reason: "This chart is empty." });
});

test("a diagram's title is read from its front matter or its accTitle", () => {
  assert.equal(diagramTitle("---\ntitle: How a hire goes\n---\nflowchart LR\n A-->B"), "How a hire goes");
  assert.equal(diagramTitle("flowchart LR\n accTitle: The hire\n A-->B"), "The hire");
  assert.equal(diagramTitle("flowchart LR\n A-->B"), "");
  assert.equal(diagramTitle("---\ntitle: " + "x".repeat(200) + "\n---\ngraph LR").length, 80);
});

test("the frame is given the tokens' colours and nothing that is not a colour", () => {
  const colours = diagramColours((name) => dark[name] ?? "");
  assert.ok(colours);
  assert.deepEqual(Object.keys(colours).sort(), [...diagramTokens].sort());
  assert.equal(diagramColours((name) => (name === "ink" ? "" : dark[name])), undefined, "a missing token");
  assert.equal(diagramColours((name) => (name === "ink" ? "red; background: url(x)" : dark[name])), undefined, "a token that is not a colour");
  assert.equal(diagramColours((name) => " " + dark[name] + " ")?.accent, "#e0915a", "space around a token is trimmed");
  for (const bad of ["red", "#12", "#12345", "rgb(1,2,3)", "#fff;", "url(x)", 7, null]) assert.equal(isColour(bad), false, String(bad));
  for (const good of ["#fff", "#1d2324", "#1d232480"]) assert.equal(isColour(good), true, good);
  const theme = mermaidTheme(colours, true);
  assert.equal(theme.darkMode, true);
  assert.equal(theme.primaryTextColor, "#f1ece0");
  assert.equal(theme.lineColor, "#737e7f");
  for (const [name, v] of Object.entries(theme)) {
    if (name === "darkMode" || name === "fontFamily" || name === "fontSize") continue;
    assert.equal(isColour(v), true, `${name} is ${v}`);
  }
});

test("the frame reads a draw request and nothing else", () => {
  const colours = diagramColours((name) => dark[name])!;
  const sent = drawMessage("graph LR\n A-->B", "dark", colours);
  assert.deepEqual(readDrawMessage(sent), sent);
  assert.deepEqual(readDrawMessage(JSON.parse(JSON.stringify(sent))), sent, "as it arrives, copied");
  assert.equal(readDrawMessage({ ...sent, channel: "something-else" }), undefined);
  assert.equal(readDrawMessage({ ...sent, kind: "run" }), undefined);
  assert.equal(readDrawMessage({ ...sent, source: 7 }), undefined);
  assert.equal(readDrawMessage({ ...sent, theme: "sepia" }), undefined);
  assert.equal(readDrawMessage({ ...sent, colours: { ...colours, ink: "red;}</style>" } }), undefined);
  assert.equal(readDrawMessage({ ...sent, colours: undefined }), undefined);
  for (const other of [undefined, null, "draw", 7, [], { kind: "draw" }]) assert.equal(readDrawMessage(other), undefined);
  // What it reads holds only the parts it knows.
  assert.deepEqual(Object.keys(readDrawMessage({ ...sent, extra: "x", colours: { ...colours, extra: "#fff" } })!.colours).sort(), [...diagramTokens].sort());
});

test("the page reads three states from a frame, and a problem only as text of a bounded length", () => {
  assert.deepEqual(readFrameMessage(frameMessage({ kind: "ready" })), { kind: "ready" });
  assert.deepEqual(readFrameMessage(frameMessage({ kind: "drawn" })), { kind: "drawn" });
  assert.deepEqual(readFrameMessage(frameMessage({ kind: "problem", text: "Parse error on line 2" })), { kind: "problem", text: "Parse error on line 2" });
  assert.deepEqual(readFrameMessage({ channel: diagramChannel, kind: "problem", text: { html: "<b>" } }), { kind: "problem", text: "" });
  assert.equal((readFrameMessage({ channel: diagramChannel, kind: "problem", text: "x".repeat(5000) }) as { text: string }).text.length, 600);
  // A frame cannot ask the page for anything: no other kind is read, and nothing but kind and text is kept.
  for (const kind of ["draw", "navigate", "open", "resize", "", undefined]) assert.equal(readFrameMessage({ channel: diagramChannel, kind }), undefined, String(kind));
  assert.deepEqual(readFrameMessage({ channel: diagramChannel, kind: "drawn", height: 99999, href: "https://example.invalid" }), { kind: "drawn" });
  assert.equal(readFrameMessage({ kind: "ready" }), undefined, "no channel");
  for (const other of [undefined, null, "ready", 7, []]) assert.equal(readFrameMessage(other), undefined);
});
