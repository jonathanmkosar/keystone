// Inline visuals on the screen (P9-04): a reply's fenced chart block drawn
// as a chart, and its fenced mermaid block drawn as a diagram.
//
// Nothing a model writes runs in this page (decision 0541):
//
// - A chart is drawn here, as SVG elements built by React from the numbers
//   and labels chart.ts has checked. No markup is ever set from text, so a
//   label is only ever a text node.
// - A diagram is drawn by Mermaid inside a sandboxed frame of no origin,
//   whose page the server serves under a policy of its own
//   (internal/visuals). This file gives the frame the block's source and
//   the theme's colours with postMessage, and reads back one of three
//   states. The frame may run scripts and nothing else: never same-origin.
//
// The conversation alone wraps a reply's text in InlineVisuals. A shared
// page, a task's note, a sub-agent's result and an artifact do not, so
// there a chart or a diagram stays a code block.
//
// There is no mockup of a chart or a diagram in a reply, so the look is
// built from the tokens and is provisional (P1-06). Sizes are set as SVG
// and frame attributes, since the page's policy refuses a style attribute.

import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode, type RefObject } from "react";
import { useParams } from "react-router";
import { ApiError, call, listSettings } from "./api";
import { openArtifact, usePageTheme } from "./artifacts";
import { chartKind, chartSummary, chartTable, layoutChart, parseChart, tipFor, type Chart, type ChartType, type Scene } from "./chart";
import { Icon } from "./Icon";
import { CodeBlock, FenceDrawer, type FencedBlock } from "./Markdown";
import { diagramColours, diagramTitle, drawMessage, planBlock, readFrameMessage, readVisualSettings, type VisualKind, type VisualSettings } from "./visuals";
import "./visuals.css";

// ---------------------------------------------------------------------------
// The settings, read through the settings route when a chat is opened and
// kept between chats, so a chat's visuals draw at once.
// ---------------------------------------------------------------------------

let known: VisualSettings | undefined;
let readFor: string | undefined;
const watchers = new Set<() => void>();

function watch(fn: () => void): () => void {
  watchers.add(fn);
  return () => watchers.delete(fn);
}

function readSettings(chatId: string) {
  if (readFor === chatId) return;
  readFor = chatId;
  listSettings().then(
    (r) => {
      known = readVisualSettings(r.settings);
      for (const w of watchers) w();
    },
    () => {
      // No settings: what was read before stands, or nothing is drawn.
      readFor = undefined;
    },
  );
}

function useVisualSettings(chatId: string): VisualSettings | undefined {
  useEffect(() => readSettings(chatId), [chatId]);
  return useSyncExternalStore(watch, () => known);
}

/**
 * A reply's text, with its charts and diagrams drawn. streaming says the
 * text is still arriving, so a block with no closing fence is waited for.
 */
export function InlineVisuals({ streaming, children }: { streaming: boolean; children: ReactNode }) {
  const { id: chatId = "" } = useParams();
  const settings = useVisualSettings(chatId);
  const draw = useCallback(
    (block: FencedBlock): ReactNode | undefined => {
      const plan = planBlock(block, settings, streaming);
      if (!settings || plan.draw === "code") return undefined;
      switch (plan.draw) {
        case "pending":
          return <Pending what={plan.what} height={plan.what === "chart" ? settings.chartHeight : settings.diagramHeight} />;
        case "chart":
          return <ChartBlock source={plan.source} settings={settings} />;
        case "diagram":
          return <DiagramFigure source={plan.source} settings={settings} chatId={chatId} />;
        case "problem":
          return <Problem what={plan.what} reason={plan.reason} source={block.text} />;
      }
    },
    [settings, streaming, chatId],
  );
  return <FenceDrawer.Provider value={draw}>{children}</FenceDrawer.Provider>;
}

// ---------------------------------------------------------------------------
// The frame every visual sits in: a heading, what is drawn, and its source.
// ---------------------------------------------------------------------------

const lang: Record<VisualKind, string> = { chart: "chart", diagram: "mermaid" };

// Three small pictures for the buttons, drawn as the icons in Icon.tsx are.
const glyphs = {
  data: <path d="M4 5h16v14H4zM4 10h16M4 14.5h16M10 5v14" />,
  source: <path d="M9 8l-4 4 4 4M15 8l4 4-4 4" />,
  larger: <path d="M14 4h6v6M20 4l-7 7M10 20H4v-6M4 20l7-7" />,
} as const;

function Glyph({ name }: { name: keyof typeof glyphs }) {
  return (
    <svg className="ico" width={14} height={14} viewBox="0 0 24 24" aria-hidden="true">
      {glyphs[name]}
    </svg>
  );
}

/** A block that is not drawn: a plain note, and its source as a code block. */
function Problem({ what, reason, source }: { what: VisualKind; reason: string; source: string }) {
  return (
    <div className="visual-problem">
      <p className="visual-note" role="note">
        <Icon name="alert" size={16} />
        <span>
          This {what} could not be drawn. {reason} Its source is below.
        </span>
      </p>
      <CodeBlock lang={lang[what]} text={source} />
    </div>
  );
}

/** A block still being written: its heading and a place of the height it will draw at. */
function Pending({ what, height }: { what: VisualKind; height: number }) {
  const label = what === "chart" ? "A chart is being written" : "A diagram is being written";
  return (
    <figure className="visual is-pending" aria-busy="true">
      <div className="visual-box">
        <figcaption className="visual-head">
          <span className="visual-kind">{what === "chart" ? "Chart" : "Diagram"}</span>
          <span className="visual-title">being written</span>
        </figcaption>
        <div className="visual-body">
          <svg className="visual-wait" width="100%" height={height} role="img" aria-label={label} />
        </div>
      </div>
    </figure>
  );
}

type Panel = "none" | "data" | "source";

/**
 * The frame of a drawn visual. Open larger lifts the box over the page at
 * the size of the window, and a place of the same height is held in the
 * reply, so nothing under it moves. Escape, the button and the ground
 * around it put it back.
 */
function Figure({
  kind,
  title,
  height,
  buttons,
  panel,
  children,
}: {
  kind: string;
  title: string;
  /** The height of what is drawn, in the reply. */
  height: number;
  buttons: ReactNode;
  panel: ReactNode;
  children: (large: boolean) => ReactNode;
}) {
  const [large, setLarge] = useState(false);
  const toggle = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!large) return;
    const key = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      e.stopPropagation();
      setLarge(false);
    };
    document.addEventListener("keydown", key, true);
    toggle.current?.focus();
    return () => document.removeEventListener("keydown", key, true);
  }, [large]);
  return (
    <figure className={large ? "visual is-large" : "visual"}>
      {large && <div className="visual-ground" onClick={() => setLarge(false)} />}
      <div className="visual-box" role={large ? "dialog" : undefined} aria-modal={large ? true : undefined} aria-label={large ? `${kind}: ${title}` : undefined}>
        <figcaption className="visual-head">
          <span className="visual-kind">{kind}</span>
          <span className="visual-title" title={title}>
            {title}
          </span>
          <span className="visual-buttons">
            {buttons}
            <button ref={toggle} type="button" className="copy-button" aria-pressed={large} title={large ? "Back into the reply" : "Open larger"} onClick={() => setLarge((l) => !l)}>
              {large ? <Icon name="close" size={14} /> : <Glyph name="larger" />}
              <span className="visual-button-text">{large ? "Close" : "Larger"}</span>
            </button>
          </span>
        </figcaption>
        <div className="visual-body">{children(large)}</div>
        {panel}
      </div>
      {large && (
        <div className="visual-hold" aria-hidden="true">
          <svg width="100%" height={height} />
        </div>
      )}
    </figure>
  );
}

function PanelButton({ name, label, open, onClick }: { name: keyof typeof glyphs; label: string; open: boolean; onClick: () => void }) {
  return (
    <button type="button" className="copy-button" aria-expanded={open} title={open ? `Hide the ${label.toLowerCase()}` : `Show the ${label.toLowerCase()}`} onClick={onClick}>
      <Glyph name={name} />
      <span className="visual-button-text">{label}</span>
    </button>
  );
}

// ---------------------------------------------------------------------------
// Charts
// ---------------------------------------------------------------------------

function ChartBlock({ source, settings }: { source: string; settings: VisualSettings }) {
  const { maxSeries, maxPoints, labelMaxChars } = settings.chart;
  const parsed = useMemo(() => parseChart(source, { maxSeries, maxPoints, labelMaxChars }), [source, maxSeries, maxPoints, labelMaxChars]);
  if (!parsed.ok) return <Problem what="chart" reason={parsed.reason} source={source} />;
  return <ChartFigure chart={parsed.chart} source={source} height={settings.chartHeight} />;
}

function ChartFigure({ chart, source, height }: { chart: Chart; source: string; height: number }) {
  const [panel, setPanel] = useState<Panel>("none");
  const summary = useMemo(() => chartSummary(chart), [chart]);
  const table = useMemo(() => chartTable(chart), [chart]);
  const show = (p: Panel) => setPanel((now) => (now === p ? "none" : p));
  return (
    <Figure
      kind={chartKind(chart.type)}
      title={chart.title}
      height={height}
      buttons={
        <>
          <PanelButton name="data" label="Data" open={panel === "data"} onClick={() => show("data")} />
          <PanelButton name="source" label="Source" open={panel === "source"} onClick={() => show("source")} />
        </>
      }
      panel={
        panel === "data" ? (
          <div className="visual-panel">
            <p className="visual-summary">{summary}</p>
            <div className="table-wrap">
              <table>
                <caption className="offscreen">{chart.title}</caption>
                <thead>
                  <tr>
                    {table.head.map((h, i) => (
                      <th key={i} scope="col">
                        {h}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {table.rows.map((row, r) => (
                    <tr key={r}>
                      {row.map((cell, i) =>
                        i === 0 ? (
                          <th key={i} scope="row">
                            {cell}
                          </th>
                        ) : (
                          <td key={i}>{cell}</td>
                        ),
                      )}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        ) : panel === "source" ? (
          <div className="visual-panel">
            <CodeBlock lang="chart" text={source} />
          </div>
        ) : null
      }
    >
      {(large) => <ChartDrawing chart={chart} height={height} fill={large} label={`${chart.title}. ${summary}`} />}
    </Figure>
  );
}

/** The box a drawing has: its width, and its height when it fills its place. */
function useBox(): [RefObject<HTMLDivElement | null>, { w: number; h: number }] {
  const ref = useRef<HTMLDivElement>(null);
  const [box, setBox] = useState({ w: 0, h: 0 });
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => {
      const r = el.getBoundingClientRect();
      setBox((b) => (Math.abs(b.w - r.width) < 0.5 && Math.abs(b.h - r.height) < 0.5 ? b : { w: r.width, h: r.height }));
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const watchBox = new ResizeObserver(measure);
    watchBox.observe(el);
    return () => watchBox.disconnect();
  }, []);
  return [ref, box];
}

const tones = 6;

function tone(n: number): string {
  return n < 0 ? "" : ` tone-${n % tones}`;
}

/** A point's mark, a shape for each series, so colour is not the only thing that tells them apart. */
function PointMark({ n, x, y, r, className }: { n: number; x: number; y: number; r: number; className: string }) {
  const cls = className + tone(n);
  switch (n % tones) {
    case 1:
      return <rect className={cls} x={x - r * 0.9} y={y - r * 0.9} width={r * 1.8} height={r * 1.8} />;
    case 2:
      return <path className={cls} d={`M${x},${y - r * 1.25} L${x + r * 1.25},${y} L${x},${y + r * 1.25} L${x - r * 1.25},${y} Z`} />;
    case 3:
      return <path className={cls} d={`M${x},${y - r * 1.2} L${x + r * 1.15},${y + r * 0.9} L${x - r * 1.15},${y + r * 0.9} Z`} />;
    case 4:
      return <path className={cls} d={`M${x},${y + r * 1.2} L${x + r * 1.15},${y - r * 0.9} L${x - r * 1.15},${y - r * 0.9} Z`} />;
    case 5:
      return <path className={cls} d={`M${x - r},${y - r * 0.45} H${x + r} V${y + r * 0.45} H${x - r} Z`} />;
  }
  return <circle className={cls} cx={x} cy={y} r={r} />;
}

/** The mark beside a series' name in the legend: what its marks look like. */
function Key({ type, n, x, y }: { type: ChartType; n: number; x: number; y: number }) {
  if (type === "line") {
    return <path className={`chart-line dash-${n % tones}${tone(n)}`} d={`M${x},${y} H${x + 14}`} />;
  }
  if (type === "scatter") return <PointMark n={n} x={x + 6} y={y} r={4} className="chart-key" />;
  return <rect className={"chart-key" + tone(n)} x={x} y={y - 5} width={10} height={10} rx={2} />;
}

function ChartDrawing({ chart, height, fill, label }: { chart: Chart; height: number; fill: boolean; label: string }) {
  const [ref, box] = useBox();
  const [over, setOver] = useState(-1);
  const h = fill && box.h > 0 ? box.h : height;
  const scene: Scene | undefined = useMemo(() => (box.w > 0 ? layoutChart(chart, box.w, h) : undefined), [chart, box.w, h]);
  const at = scene && over >= 0 ? scene.targets[over] : undefined;
  const tip = scene && at ? tipFor(at, scene) : undefined;
  return (
    <div className="chart-box" ref={ref}>
      {!scene ? (
        <svg className="chart" width="100%" height={height} role="img" aria-label={label} />
      ) : (
        <svg className={`chart is-${scene.type}`} width={scene.width} height={scene.height} viewBox={`0 0 ${scene.width} ${scene.height}`} role="img" aria-label={label} onPointerLeave={() => setOver(-1)}>
          <g aria-hidden="true">
            {scene.yTicks.map((t, i) => (
              <g key={"y" + i}>
                <line className="chart-grid" x1={scene.plot.x} x2={scene.plot.x + scene.plot.w} y1={t.y} y2={t.y} />
                <text className="chart-tick" x={scene.plot.x - 8} y={t.y} textAnchor="end" dominantBaseline="middle">
                  {t.text}
                </text>
              </g>
            ))}
            {scene.xLines.map((x, i) => (
              <line key={"v" + i} className="chart-grid" x1={x} x2={x} y1={scene.plot.y} y2={scene.plot.y + scene.plot.h} />
            ))}
            {scene.xTicks.map((t, i) => (
              <text key={"x" + i} className="chart-tick" x={t.x} y={scene.plot.y + scene.plot.h + 15} textAnchor="middle">
                {t.text}
              </text>
            ))}
            {scene.xTitle && (
              <text className="chart-axis" x={scene.xTitle.x} y={scene.xTitle.y} textAnchor="middle">
                {scene.xTitle.text}
              </text>
            )}
            {scene.yTitle && (
              <text className="chart-axis" x={scene.yTitle.x} y={scene.yTitle.y} textAnchor="middle" transform={`rotate(-90 ${scene.yTitle.x} ${scene.yTitle.y})`}>
                {scene.yTitle.text}
              </text>
            )}
            {scene.areas.map((a, i) => (
              <path key={"a" + i} className={"chart-area" + tone(a.tone)} d={a.d} />
            ))}
            {scene.bars.map((b, i) => (
              <path key={"b" + i} className={"chart-bar" + tone(b.tone)} d={b.d} />
            ))}
            {scene.zero >= 0 && <line className="chart-zero" x1={scene.plot.x} x2={scene.plot.x + scene.plot.w} y1={scene.zero} y2={scene.zero} />}
            {tip && at && at.guide >= 0 && <line className="chart-guide" x1={at.guide} x2={at.guide} y1={scene.plot.y} y2={scene.plot.y + scene.plot.h} />}
            {scene.lines.map((l, i) => (
              <path key={"l" + i} className={`chart-line dash-${l.tone % tones}${tone(l.tone)}`} d={l.d} />
            ))}
            {scene.slices.map((s, i) => (
              <path key={"s" + i} className={"chart-slice" + tone(s.tone)} d={s.d} />
            ))}
            {scene.dots.map((d, i) => (scene.type === "scatter" ? <PointMark key={"d" + i} n={d.tone} x={d.x} y={d.y} r={4.5} className="chart-dot" /> : <circle key={"d" + i} className={"chart-dot" + tone(d.tone)} cx={d.x} cy={d.y} r={4} />))}
            {scene.legend.map((l, i) => (
              <g key={"k" + i}>
                <Key type={scene.type} n={l.tone} x={l.x} y={l.y} />
                <text className="chart-legend" x={l.x + 18} y={l.y} dominantBaseline="middle">
                  {l.text}
                </text>
              </g>
            ))}
            {scene.targets.map((t, i) =>
              t.shape === "rect" ? (
                <rect key={"t" + i} className="chart-hit" x={t.x} y={t.y} width={t.w} height={t.h} onPointerEnter={() => setOver(i)} onPointerDown={() => setOver(i)} />
              ) : t.shape === "circle" ? (
                <circle key={"t" + i} className="chart-hit" cx={t.x} cy={t.y} r={t.r} onPointerEnter={() => setOver(i)} onPointerDown={() => setOver(i)} />
              ) : (
                <path key={"t" + i} className="chart-hit" d={t.d} onPointerEnter={() => setOver(i)} onPointerDown={() => setOver(i)} />
              ),
            )}
            {tip && (
              <g className="chart-tip">
                <rect x={tip.x} y={tip.y} width={tip.w} height={tip.h} rx={6} />
                {tip.title !== "" && (
                  <text className="chart-tip-title" x={tip.x + 10} y={tip.y + 19}>
                    {tip.title}
                  </text>
                )}
                {tip.rows.map((r, i) => (
                  <g key={i}>
                    {r.tone >= 0 && <rect className={"chart-key" + tone(r.tone)} x={tip.x + 10} y={r.y - 9} width={10} height={10} rx={2} />}
                    <text className="chart-tip-row" x={tip.x + 10 + (r.tone >= 0 ? 16 : 0)} y={r.y}>
                      {r.text}
                    </text>
                  </g>
                ))}
              </g>
            )}
          </g>
        </svg>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Diagrams
// ---------------------------------------------------------------------------

/** The diagram frame's permissions: its one script, and nothing else. The server's policy says the same. */
const frameSandbox = "allow-scripts";

const framePath = "/api/v1/visuals/diagram";

type Drawn = { state: "loading" } | { state: "drawn" } | { state: "problem"; reason: string };

/** Why Mermaid could not draw, as a sentence: the first line of what it said, which names the line it stopped at. */
function problemWords(said: string): string {
  const first = (said.split("\n")[0] ?? "").trim().replace(/[:.]+$/, "");
  if (first === "") return "Mermaid could not read it.";
  return `Mermaid could not read it (${first.length > 120 ? first.slice(0, 119) + "…" : first}).`;
}

function token(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue("--" + name);
}

function DiagramFigure({ source, settings, chatId }: { source: string; settings: VisualSettings; chatId: string }) {
  const theme = usePageTheme();
  const [panel, setPanel] = useState<Panel>("none");
  const [near, setNear] = useState(false);
  const [drawn, setDrawn] = useState<Drawn>({ state: "loading" });
  const place = useRef<HTMLDivElement>(null);
  const frame = useRef<HTMLIFrameElement>(null);
  const ready = useRef(false);
  const title = diagramTitle(source);

  // The frame is megabytes of renderer to read, so it is made when the
  // diagram is near the window, not for every diagram of a long chat.
  useEffect(() => {
    const el = place.current;
    if (near || !el) return;
    if (typeof IntersectionObserver === "undefined") {
      setNear(true);
      return;
    }
    const seen = new IntersectionObserver((entries) => entries.some((e) => e.isIntersecting) && setNear(true), { rootMargin: "600px" });
    seen.observe(el);
    return () => seen.disconnect();
  }, [near]);

  // A new theme is a new frame, which says "ready" again.
  useEffect(() => {
    ready.current = false;
    setDrawn((d) => (d.state === "problem" ? d : { state: "loading" }));
  }, [theme]);

  useEffect(() => {
    if (!near || drawn.state === "problem") return;
    const send = () => {
      const colours = diagramColours(token);
      if (!colours) {
        setDrawn({ state: "problem", reason: "The theme's colours could not be read." });
        return;
      }
      // The frame has no origin to name, so the message is addressed to
      // whatever is in it; the page's own policy lets only Keystone's
      // pages be framed.
      frame.current?.contentWindow?.postMessage(drawMessage(source, theme, colours), "*");
    };
    const hear = (e: MessageEvent) => {
      // Only this diagram's own frame is heard, and only three words of it.
      if (!frame.current || e.source !== frame.current.contentWindow) return;
      const m = readFrameMessage(e.data);
      if (!m) return;
      if (m.kind === "ready") {
        ready.current = true;
        send();
      } else if (m.kind === "drawn") setDrawn({ state: "drawn" });
      else setDrawn({ state: "problem", reason: problemWords(m.text) });
    };
    window.addEventListener("message", hear);
    if (ready.current) send();
    return () => window.removeEventListener("message", hear);
  }, [near, source, theme, drawn.state === "problem"]);

  // A frame that does not answer: the source, with a note.
  useEffect(() => {
    if (!near || drawn.state !== "loading") return;
    const timer = window.setTimeout(() => setDrawn({ state: "problem", reason: "Its frame did not answer in time." }), settings.diagramWaitMs);
    return () => window.clearTimeout(timer);
  }, [near, drawn.state, theme, settings.diagramWaitMs]);

  if (drawn.state === "problem") return <Problem what="diagram" reason={drawn.reason} source={source} />;
  return (
    <Figure
      kind="Diagram"
      title={title}
      height={settings.diagramHeight}
      buttons={
        <>
          <PanelButton name="source" label="Source" open={panel === "source"} onClick={() => setPanel((p) => (p === "source" ? "none" : "source"))} />
          {chatId !== "" && <SaveButton chatId={chatId} title={title} source={source} />}
        </>
      }
      panel={
        panel === "source" ? (
          <div className="visual-panel">
            <CodeBlock lang="mermaid" text={source} />
          </div>
        ) : null
      }
    >
      {() => (
        <div className="diagram-box" ref={place}>
          {near && (
            <iframe
              key={theme}
              ref={frame}
              className="diagram-frame"
              title={title === "" ? "Diagram" : `Diagram: ${title}`}
              src={`${framePath}?theme=${theme}`}
              sandbox={frameSandbox}
              referrerPolicy="no-referrer"
              width="100%"
              height={settings.diagramHeight}
            />
          )}
          {!near && <svg className="visual-wait" width="100%" height={settings.diagramHeight} aria-hidden="true" />}
          {drawn.state === "loading" && (
            <p className="diagram-wait" role="status">
              Drawing the diagram
            </p>
          )}
        </div>
      )}
    </Figure>
  );
}

type Saved = { state: "idle" } | { state: "saving" } | { state: "saved"; id: string } | { state: "failed"; why: string };

/** Keeps the diagram as a Mermaid artifact of this chat, and opens it beside the chat. */
function SaveButton({ chatId, title, source }: { chatId: string; title: string; source: string }) {
  const [saved, setSaved] = useState<Saved>({ state: "idle" });
  async function save() {
    if (saved.state === "saving") return;
    if (saved.state === "saved") {
      openArtifact(chatId, saved.id);
      return;
    }
    setSaved({ state: "saving" });
    try {
      const made = (await call("POST", `/api/v1/chats/${encodeURIComponent(chatId)}/visuals/diagrams`, { title, content: source })) as { artifact?: { id?: unknown } };
      const id = made.artifact?.id;
      if (typeof id !== "string") throw new ApiError("not-ready", 200);
      setSaved({ state: "saved", id });
      openArtifact(chatId, id);
    } catch (e) {
      setSaved({ state: "failed", why: e instanceof ApiError && e.said !== "" ? e.said : "It could not be saved." });
    }
  }
  const text = saved.state === "saved" ? "Open artifact" : saved.state === "saving" ? "Saving" : saved.state === "failed" ? "Not saved" : "Save";
  const hint = saved.state === "failed" ? saved.why : saved.state === "saved" ? "Open the saved artifact beside the chat" : "Save as an artifact of this chat";
  return (
    <button type="button" className="copy-button" title={hint} onClick={save} aria-live="polite">
      <Icon name="artifacts" size={14} />
      <span className="visual-button-text">{text}</span>
    </button>
  );
}
