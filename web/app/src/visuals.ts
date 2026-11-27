// Inline visuals (P9-04): which fenced blocks of a reply are drawn, what
// the settings allow, and the few messages the page and a diagram's
// sandboxed frame send each other. chart.ts has the chart format.
//
// A `chart` block is drawn by the app from checked numbers and labels. A
// `mermaid` block is drawn by Mermaid, which never runs in the app's own
// page: it runs in a frame of no origin (internal/visuals), and the page
// hands it the block's text and the theme's colours and hears back only
// "ready", "drawn" or "it could not be drawn, because…". Decisions 0540 to
// 0543.
//
// This file imports nothing, so Node's own test runner runs it as it is
// (web/app/test/visuals.test.ts, decision 0501), and the frame's renderer
// (sandbox/mermaid.ts) reads the same messages with the same code.

/** The settings a reply's visuals are drawn with, from settings/visuals.json. */
export type VisualSettings = {
  /** visuals.draw */
  draw: boolean;
  /** visuals.max_block_bytes */
  maxBlockBytes: number;
  /** visuals.chart_max_series, visuals.chart_max_points, visuals.chart_label_max_chars */
  chart: { maxSeries: number; maxPoints: number; labelMaxChars: number };
  /** visuals.chart_height, in pixels */
  chartHeight: number;
  /** visuals.diagram_height, in pixels */
  diagramHeight: number;
  /** visuals.diagram_wait, in milliseconds */
  diagramWaitMs: number;
};

const durationPart = /(\d+(?:\.\d+)?)(ms|s|m|h)/g;
const unitMs: Record<string, number> = { ms: 1, s: 1000, m: 60000, h: 3600000 };

/** A Go duration, such as 20s or 1m30s, in milliseconds. Undefined when it does not read. */
export function durationMs(text: string): number | undefined {
  const t = text.trim();
  if (t === "") return undefined;
  if (t === "0") return 0;
  let total = 0;
  let read = 0;
  for (const m of t.matchAll(durationPart)) {
    if (m.index !== read) return undefined;
    total += Number(m[1]) * unitMs[m[2]];
    read = m.index + m[0].length;
  }
  return read === t.length ? total : undefined;
}

/**
 * The settings, read from the settings route's list. Undefined when one is
 * missing or does not read as its type: the page then draws nothing and
 * shows every block as its source, since the values live in the registry
 * and never here.
 */
export function readVisualSettings(list: readonly { key: string; value: string }[]): VisualSettings | undefined {
  const value = (key: string) => list.find((s) => s.key === key)?.value;
  const whole = (key: string, least: number) => {
    const v = value(key);
    if (v === undefined || !/^\d+$/.test(v.trim())) return undefined;
    const n = Number(v);
    return Number.isSafeInteger(n) && n >= least ? n : undefined;
  };
  const drawText = value("visuals.draw");
  const draw = drawText === "true" ? true : drawText === "false" ? false : undefined;
  const maxBlockBytes = whole("visuals.max_block_bytes", 1);
  const maxSeries = whole("visuals.chart_max_series", 1);
  const maxPoints = whole("visuals.chart_max_points", 1);
  const labelMaxChars = whole("visuals.chart_label_max_chars", 1);
  const chartHeight = whole("visuals.chart_height", 120);
  const diagramHeight = whole("visuals.diagram_height", 120);
  const diagramWaitMs = durationMs(value("visuals.diagram_wait") ?? "");
  if (
    draw === undefined ||
    maxBlockBytes === undefined ||
    maxSeries === undefined ||
    maxPoints === undefined ||
    labelMaxChars === undefined ||
    chartHeight === undefined ||
    diagramHeight === undefined ||
    diagramWaitMs === undefined ||
    diagramWaitMs <= 0
  ) {
    return undefined;
  }
  return { draw, maxBlockBytes, chart: { maxSeries, maxPoints, labelMaxChars }, chartHeight, diagramHeight, diagramWaitMs };
}

export type VisualKind = "chart" | "diagram";

/** What a fenced block's language makes it: a chart, a diagram, or neither. */
export function visualKind(lang: string): VisualKind | undefined {
  const l = lang.trim().toLowerCase();
  return l === "chart" ? "chart" : l === "mermaid" ? "diagram" : undefined;
}

/** A text's size in bytes, as UTF-8. */
export function byteLength(text: string): number {
  let n = 0;
  for (const ch of text) {
    const c = ch.codePointAt(0) ?? 0;
    n += c < 0x80 ? 1 : c < 0x800 ? 2 : c < 0x10000 ? 3 : 4;
  }
  return n;
}

/** A fenced block as the Markdown reader found it. */
export type Fenced = { lang: string; text: string; closed: boolean };

/** What the page does with a fenced block. */
export type Plan =
  /** An ordinary code block. */
  | { draw: "code" }
  /** Still being written: a placeholder of the height it will draw at. */
  | { draw: "pending"; what: VisualKind }
  /** A whole chart block, to be checked by chart.ts and drawn by the app. */
  | { draw: "chart"; source: string }
  /** A whole diagram block, to be drawn in the sandboxed frame. */
  | { draw: "diagram"; source: string }
  /** Not drawn: its source is shown with this sentence. */
  | { draw: "problem"; what: VisualKind; reason: string };

/**
 * Decides what a fenced block is drawn as. settings is undefined while they
 * are being read, or when they could not be; streaming says the reply the
 * block is in is still arriving.
 */
export function planBlock(block: Fenced, settings: VisualSettings | undefined, streaming: boolean): Plan {
  const what = visualKind(block.lang);
  if (!what || !settings || !settings.draw) return { draw: "code" };
  if (!block.closed) {
    if (streaming) return { draw: "pending", what };
    return { draw: "problem", what, reason: `The reply ended before this ${what} was finished.` };
  }
  const size = byteLength(block.text);
  if (size > settings.maxBlockBytes) {
    return { draw: "problem", what, reason: `This ${what} is ${size} bytes, and Keystone draws one of up to ${settings.maxBlockBytes}.` };
  }
  if (block.text.trim() === "") return { draw: "problem", what, reason: `This ${what} is empty.` };
  return what === "chart" ? { draw: "chart", source: block.text } : { draw: "diagram", source: block.text };
}

/** A diagram's title: the title in its front matter, or its accTitle, or none. */
export function diagramTitle(source: string): string {
  const front = /^\s*---\s*\n([\s\S]*?)\n\s*---\s*(?:\n|$)/.exec(source);
  const inFront = front ? /^\s*title:\s*(.+?)\s*$/m.exec(front[1]) : null;
  const acc = /^\s*accTitle\s*:\s*(.+?)\s*$/m.exec(source);
  const found = (inFront?.[1] ?? acc?.[1] ?? "").replace(/^["']|["']$/g, "").trim();
  return [...found].slice(0, 80).join("");
}

// ---------------------------------------------------------------------------
// The theme's colours, as the frame is given them.
// ---------------------------------------------------------------------------

/** The tokens a diagram is drawn in (design/tokens-draft.json). */
export const diagramTokens = ["page", "raised", "sunk", "line", "strong", "ink", "muted", "accent", "soft", "softink", "ok", "link", "err"] as const;

export type DiagramColours = Record<(typeof diagramTokens)[number], string>;

/** A colour as a token holds it: #rgb, #rrggbb or #rrggbbaa. Nothing else is passed on. */
export function isColour(v: unknown): v is string {
  return typeof v === "string" && /^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$/.test(v);
}

/** The diagram's colours, read from the page's tokens. Undefined when a token is missing or is not a colour. */
export function diagramColours(token: (name: string) => string): DiagramColours | undefined {
  const out: Record<string, string> = {};
  for (const name of diagramTokens) {
    const v = token(name).trim();
    if (!isColour(v)) return undefined;
    out[name] = v;
  }
  return out as DiagramColours;
}

/** Mermaid's theme variables, for its base theme, from the tokens. */
export function mermaidTheme(c: DiagramColours, dark: boolean): Record<string, string | boolean> {
  return {
    darkMode: dark,
    background: c.raised,
    fontFamily: 'system-ui, -apple-system, "Segoe UI", sans-serif',
    fontSize: "14px",
    primaryColor: c.soft,
    primaryTextColor: c.ink,
    primaryBorderColor: c.accent,
    secondaryColor: c.sunk,
    secondaryTextColor: c.ink,
    secondaryBorderColor: c.strong,
    tertiaryColor: c.page,
    tertiaryTextColor: c.ink,
    tertiaryBorderColor: c.line,
    lineColor: c.strong,
    textColor: c.ink,
    mainBkg: c.soft,
    nodeBorder: c.accent,
    nodeTextColor: c.ink,
    clusterBkg: c.page,
    clusterBorder: c.line,
    titleColor: c.ink,
    edgeLabelBackground: c.raised,
    noteBkgColor: c.sunk,
    noteTextColor: c.ink,
    noteBorderColor: c.line,
    actorBkg: c.soft,
    actorBorder: c.accent,
    actorTextColor: c.ink,
    actorLineColor: c.strong,
    signalColor: c.ink,
    signalTextColor: c.ink,
    labelBoxBkgColor: c.soft,
    labelBoxBorderColor: c.accent,
    labelTextColor: c.ink,
    loopTextColor: c.ink,
    activationBkgColor: c.sunk,
    activationBorderColor: c.strong,
    sequenceNumberColor: c.raised,
    pie1: c.accent,
    pie2: c.link,
    pie3: c.strong,
    pie4: c.ok,
    pie5: c.ink,
    pie6: c.err,
    pieTitleTextColor: c.ink,
    pieSectionTextColor: c.raised,
    pieLegendTextColor: c.ink,
    pieStrokeColor: c.raised,
    pieOuterStrokeColor: c.line,
    errorBkgColor: c.sunk,
    errorTextColor: c.err,
  };
}

// ---------------------------------------------------------------------------
// The messages between the page and a diagram's frame.
// ---------------------------------------------------------------------------

/** Every message carries this, so neither side reads anything else. */
export const diagramChannel = "keystone-diagram";

/** The page asks the frame to draw. */
export type DrawMessage = { channel: typeof diagramChannel; kind: "draw"; source: string; theme: "dark" | "light"; colours: DiagramColours };

/** The frame tells the page where it stands. */
export type FrameMessage = { kind: "ready" } | { kind: "drawn" } | { kind: "problem"; text: string };

export function drawMessage(source: string, theme: "dark" | "light", colours: DiagramColours): DrawMessage {
  return { channel: diagramChannel, kind: "draw", source, theme, colours };
}

export function frameMessage(m: FrameMessage): FrameMessage & { channel: typeof diagramChannel } {
  return { channel: diagramChannel, ...m };
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** What the frame reads of a message from the page: a draw request whose every part is what it should be, or nothing. */
export function readDrawMessage(data: unknown): DrawMessage | undefined {
  if (!isRecord(data) || data.channel !== diagramChannel || data.kind !== "draw") return undefined;
  if (typeof data.source !== "string" || (data.theme !== "dark" && data.theme !== "light") || !isRecord(data.colours)) return undefined;
  const from = data.colours;
  const colours = diagramColours((name) => (typeof from[name] === "string" ? (from[name] as string) : ""));
  if (!colours) return undefined;
  return { channel: diagramChannel, kind: "draw", source: data.source, theme: data.theme, colours };
}

/** The longest reason a frame may give; the rest is cut. */
const problemMaxChars = 600;

/** What the page reads of a message from the frame: one of three states and, for a problem, plain text. */
export function readFrameMessage(data: unknown): FrameMessage | undefined {
  if (!isRecord(data) || data.channel !== diagramChannel) return undefined;
  if (data.kind === "ready" || data.kind === "drawn") return { kind: data.kind };
  if (data.kind === "problem") return { kind: "problem", text: typeof data.text === "string" ? [...data.text].slice(0, problemMaxChars).join("") : "" };
  return undefined;
}
