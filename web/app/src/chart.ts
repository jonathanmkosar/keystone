// The chart format of a reply (P9-04, decision 0540), and what the page
// builds from it. A model writes a chart as a fenced `chart` block of JSON;
// this file reads it, checks it, and lays it out as plain data: bars, lines,
// areas, slices, points, ticks and a legend, each a number or a short text.
// InlineVisuals.tsx draws that as SVG. Nothing here is markup, and nothing a model
// writes is ever more than a number or a label on its way to the page.
//
// This file imports nothing, so Node's own test runner runs it as it is
// (web/app/test/chart.test.ts, decision 0501), and so it can reach nothing
// but what it is given. The limits are settings (settings/visuals.json),
// passed in.

export type ChartType = "bar" | "line" | "area" | "pie" | "scatter";

/** The kinds of chart, in the order the format's description names them. */
export const chartTypes: readonly ChartType[] = ["bar", "line", "area", "pie", "scatter"];

/** The limits a chart is checked against: settings, never constants here. */
export type ChartLimits = {
  /** visuals.chart_max_series: the most series, and the most slices of a pie. */
  maxSeries: number;
  /** visuals.chart_max_points: the most points of one series. */
  maxPoints: number;
  /** visuals.chart_label_max_chars: the longest title, label or name. */
  labelMaxChars: number;
};

/** One series: values for bar, line, area and pie; points for scatter. */
export type Series = { name: string; values: number[]; points: [number, number][] };

/** A chart that passed every check. */
export type Chart = {
  type: ChartType;
  title: string;
  /** The horizontal axis' title, or "". */
  xTitle: string;
  /** The vertical axis' title, or "". */
  yTitle: string;
  /** The horizontal axis' labels, or a pie's slices. Empty for scatter. */
  labels: string[];
  series: Series[];
};

export type Parsed = { ok: true; chart: Chart } | { ok: false; reason: string };

class Refusal extends Error {}

function refuse(reason: string): never {
  throw new Refusal(reason);
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** A value as a refusal names it: short, and never more than text. */
function shown(v: unknown): string {
  if (v === undefined) return "missing";
  const s = typeof v === "string" ? `"${v}"` : JSON.stringify(v) ?? String(v);
  return s.length > 40 ? s.slice(0, 39) + "…" : s;
}

/** A title, label or name: a short text. A number is read as its digits. */
function words(v: unknown, what: string, limits: ChartLimits, needed: boolean): string {
  if (v === undefined || v === null) {
    if (needed) refuse(`It has no ${what}.`);
    return "";
  }
  if (typeof v === "number" && Number.isFinite(v)) v = String(v);
  if (typeof v !== "string") refuse(`Its ${what} is ${shown(v)}, not text.`);
  const text = (v as string).replace(/\s+/g, " ").trim();
  if (text === "" && needed) refuse(`Its ${what} is empty.`);
  if ([...text].length > limits.labelMaxChars) refuse(`Its ${what} is longer than ${limits.labelMaxChars} characters.`);
  return text;
}

function number(v: unknown, what: string): number {
  if (typeof v !== "number" || !Number.isFinite(v)) refuse(`${what} is ${shown(v)}, not a number.`);
  return v as number;
}

function axis(v: unknown, name: string): Record<string, unknown> {
  if (v === undefined || v === null) return {};
  if (!isObject(v)) refuse(`Its ${name} is ${shown(v)}, not an object with a title.`);
  return v as Record<string, unknown>;
}

/**
 * Reads a chart block and checks it. Anything that is not exactly a chart
 * of the format is refused with a plain sentence, and the page then shows
 * the block's source with that sentence. Keys the format does not name are
 * ignored.
 */
export function parseChart(text: string, limits: ChartLimits): Parsed {
  try {
    return { ok: true, chart: read(text, limits) };
  } catch (e) {
    if (e instanceof Refusal) return { ok: false, reason: e.message };
    return { ok: false, reason: "It could not be read." };
  }
}

function read(text: string, limits: ChartLimits): Chart {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch {
    refuse("It is not JSON.");
  }
  if (!isObject(raw)) refuse("It is not a JSON object.");
  const o = raw as Record<string, unknown>;
  if (typeof o.type !== "string" || !chartTypes.includes(o.type as ChartType)) {
    refuse(`Its type is ${shown(o.type)}; a chart is one of ${chartTypes.join(", ")}.`);
  }
  const type = o.type as ChartType;
  const title = words(o.title, "title", limits, true);
  const x = axis(o.x, "x");
  const y = axis(o.y, "y");
  const xTitle = words(x.title, "x title", limits, false);
  const yTitle = words(y.title, "y title", limits, false);

  let labels: string[] = [];
  if (type !== "scatter") {
    if (!Array.isArray(x.labels)) refuse("It has no x.labels, the list of names along the horizontal axis.");
    const list = x.labels as unknown[];
    const most = type === "pie" ? limits.maxSeries : limits.maxPoints;
    if (list.length === 0) refuse("Its x.labels are empty.");
    if (list.length > most) refuse(type === "pie" ? `It has ${list.length} slices; the most is ${most}.` : `It has ${list.length} labels; the most is ${most}.`);
    labels = list.map((l, i) => words(l, `label ${i + 1}`, limits, true));
  }

  if (!Array.isArray(o.series)) refuse("It has no series.");
  const list = o.series as unknown[];
  if (list.length === 0) refuse("Its series are empty.");
  if (type === "pie" && list.length !== 1) refuse(`A pie has one series, and this has ${list.length}.`);
  if (list.length > limits.maxSeries) refuse(`It has ${list.length} series; the most is ${limits.maxSeries}.`);
  const names = new Set<string>();
  const series = list.map((s, i): Series => {
    if (!isObject(s)) refuse(`Series ${i + 1} is ${shown(s)}, not an object with a name.`);
    const one = s as Record<string, unknown>;
    const name = words(one.name, `series ${i + 1}'s name`, limits, true);
    if (names.has(name)) refuse(`Two series are named "${name}".`);
    names.add(name);
    if (type === "scatter") {
      if (!Array.isArray(one.points)) refuse(`The series "${name}" has no points, the list of [x, y] pairs.`);
      const points = one.points as unknown[];
      if (points.length === 0) refuse(`The series "${name}" has no points.`);
      if (points.length > limits.maxPoints) refuse(`The series "${name}" has ${points.length} points; the most is ${limits.maxPoints}.`);
      return {
        name,
        values: [],
        points: points.map((p, n): [number, number] => {
          if (!Array.isArray(p) || p.length !== 2) refuse(`Point ${n + 1} of "${name}" is ${shown(p)}, not an [x, y] pair.`);
          const pair = p as unknown[];
          return [number(pair[0], `Point ${n + 1} of "${name}"`), number(pair[1], `Point ${n + 1} of "${name}"`)];
        }),
      };
    }
    if (!Array.isArray(one.values)) refuse(`The series "${name}" has no values.`);
    const values = one.values as unknown[];
    if (values.length !== labels.length) refuse(`The series "${name}" has ${values.length} values for ${labels.length} labels.`);
    return { name, values: values.map((v, n) => number(v, `Value ${n + 1} of "${name}"`)), points: [] };
  });

  if (type === "pie") {
    const values = series[0].values;
    if (values.some((v) => v < 0)) refuse("A pie cannot show a value below zero.");
    if (values.reduce((a, b) => a + b, 0) <= 0) refuse("A pie's values add up to nothing.");
  }
  return { type, title, xTitle, yTitle, labels, series };
}

// ---------------------------------------------------------------------------
// Numbers as a person reads them.
// ---------------------------------------------------------------------------

/** A value in full: 1,284.5 */
export function formatNumber(v: number, locale?: string): string {
  return new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(v);
}

/** A share of a whole: 46%, 0.4% */
export function formatShare(part: number, whole: number): string {
  const p = whole > 0 ? (part / whole) * 100 : 0;
  const shownAs = p >= 10 || p === 0 ? Math.round(p) : Math.round(p * 10) / 10;
  return `${shownAs}%`;
}

/** An axis' numbers: in full below ten thousand, compact (10K) on an axis that reaches it. */
function tickText(v: number, step: number, reach: number, locale?: string): string {
  if (reach >= 10000) return new Intl.NumberFormat(locale, { notation: "compact", maximumFractionDigits: 1 }).format(v);
  const digits = step >= 1 ? 0 : Math.min(6, Math.ceil(-Math.log10(step)));
  return new Intl.NumberFormat(locale, { minimumFractionDigits: digits, maximumFractionDigits: digits }).format(v);
}

/** A scale with round ends and round steps that holds lo to hi. */
export function niceScale(lo: number, hi: number, about = 5): { lo: number; hi: number; step: number; ticks: number[] } {
  if (!(hi > lo)) {
    // One value, or none: a little room either side of it.
    const room = lo === 0 ? 1 : Math.abs(lo) * 0.5;
    hi = lo + room;
    lo = lo - (lo === 0 ? 0 : room);
  }
  const rough = (hi - lo) / Math.max(1, about);
  const power = Math.pow(10, Math.floor(Math.log10(rough)));
  // Of the round steps near that size, the one that gives the count of
  // steps nearest the one asked for; the larger of two that tie.
  let step = power;
  let off = Infinity;
  for (const unit of [1, 2, 5, 10]) {
    const s = unit * power;
    const steps = Math.ceil(hi / s - 1e-9) - Math.floor(lo / s + 1e-9);
    if (Math.abs(steps - about) <= off) {
      off = Math.abs(steps - about);
      step = s;
    }
  }
  const first = Math.floor(lo / step + 1e-9) * step;
  const last = Math.ceil(hi / step - 1e-9) * step;
  const ticks: number[] = [];
  const count = Math.round((last - first) / step);
  for (let i = 0; i <= count; i++) ticks.push(Number((first + i * step).toPrecision(12)));
  return { lo: first, hi: last, step, ticks };
}

// ---------------------------------------------------------------------------
// The layout: a chart as plain shapes at a given size.
// ---------------------------------------------------------------------------

/** One line of what a pointer over a mark is told. tone is the series' colour, or -1 for none. */
export type Row = { tone: number; name: string; value: string };

/** A place the pointer can rest, and what it is told there. */
export type Target = {
  shape: "rect" | "circle" | "path";
  x: number;
  y: number;
  w: number;
  h: number;
  /** For a circle, its radius; for a path, its outline. */
  r: number;
  d: string;
  /** Where the note points. */
  at: { x: number; y: number };
  /** A vertical guide through the plot at this x, for a line or an area. -1 for none. */
  guide: number;
  title: string;
  rows: Row[];
};

export type Scene = {
  type: ChartType;
  width: number;
  height: number;
  plot: { x: number; y: number; w: number; h: number };
  /** The series' names with their colours, for two series or more, and a pie's slices. */
  legend: { x: number; y: number; tone: number; text: string }[];
  yTicks: { y: number; text: string }[];
  xTicks: { x: number; text: string }[];
  /** Vertical grid lines, for a scatter chart. */
  xLines: number[];
  xTitle: { x: number; y: number; text: string } | undefined;
  /** Drawn turned a quarter, reading upward, about its point. */
  yTitle: { x: number; y: number; text: string } | undefined;
  /** The y of the line values grow from, or -1 when the plot has none. */
  zero: number;
  bars: { tone: number; d: string }[];
  areas: { tone: number; d: string }[];
  lines: { tone: number; d: string }[];
  dots: { tone: number; x: number; y: number }[];
  slices: { tone: number; d: string }[];
  targets: Target[];
};

// The geometry of the drawing. These follow the type size in visuals.css
// (12px sans) and are not values a person would set.
const pad = 12;
const charWidth = 6.6;
const rowHeight = 20;
const swatch = 18;

function px(v: number): number {
  return Math.round(v * 10) / 10;
}

function short(text: string, most: number): string {
  const chars = [...text];
  return chars.length > most ? chars.slice(0, most - 1).join("") + "…" : text;
}

function widthOf(text: string): number {
  return [...text].length * charWidth;
}

/** Legend entries set in rows across the width, from the top. Returns them and the height they take. */
function setLegend(entries: { tone: number; text: string }[], width: number): { legend: Scene["legend"]; height: number } {
  const legend: Scene["legend"] = [];
  let x = pad;
  let y = pad + rowHeight / 2;
  for (const e of entries) {
    const w = swatch + widthOf(e.text) + 16;
    if (x > pad && x + w > width - pad) {
      x = pad;
      y += rowHeight;
    }
    legend.push({ x: px(x), y: px(y), tone: e.tone, text: e.text });
    x += w;
  }
  return { legend, height: entries.length === 0 ? 0 : y + rowHeight / 2 - pad + 6 };
}

function target(part: Partial<Target> & Pick<Target, "shape" | "at" | "title" | "rows">): Target {
  return { x: 0, y: 0, w: 0, h: 0, r: 0, d: "", guide: -1, ...part };
}

function empty(chart: Chart, width: number, height: number): Scene {
  return {
    type: chart.type,
    width,
    height,
    plot: { x: 0, y: 0, w: 0, h: 0 },
    legend: [],
    yTicks: [],
    xTicks: [],
    xLines: [],
    xTitle: undefined,
    yTitle: undefined,
    zero: -1,
    bars: [],
    areas: [],
    lines: [],
    dots: [],
    slices: [],
    targets: [],
  };
}

/**
 * Lays a chart out in a box of width by height. Every number in the result
 * is a place in that box; every text is a label, a name or a value from the
 * checked chart.
 */
export function layoutChart(chart: Chart, width: number, height: number, locale?: string): Scene {
  width = Math.max(120, Math.floor(width));
  height = Math.max(120, Math.floor(height));
  return chart.type === "pie" ? layoutPie(chart, width, height, locale) : layoutPlot(chart, width, height, locale);
}

function layoutPie(chart: Chart, width: number, height: number, locale?: string): Scene {
  const scene = empty(chart, width, height);
  const one = chart.series[0];
  const whole = one.values.reduce((a, b) => a + b, 0);
  const set = setLegend(
    chart.labels.map((l, i) => ({ tone: i, text: `${short(l, 24)} ${formatShare(one.values[i], whole)}` })),
    width,
  );
  scene.legend = set.legend;
  const top = pad + set.height;
  const r = Math.max(10, Math.min(width - 2 * pad, height - top - pad) / 2);
  const cx = width / 2;
  const cy = top + (height - top - pad) / 2;
  scene.plot = { x: px(cx - r), y: px(cy - r), w: px(2 * r), h: px(2 * r) };
  let from = -Math.PI / 2;
  one.values.forEach((v, i) => {
    if (v <= 0) return;
    const sweep = (v / whole) * 2 * Math.PI;
    const to = from + sweep;
    const at = (a: number, d: number) => `${px(cx + d * Math.cos(a))},${px(cy + d * Math.sin(a))}`;
    // A slice that is the whole pie is two half circles: an arc cannot end where it starts.
    const d =
      sweep >= 2 * Math.PI - 1e-9
        ? `M${at(from, r)} A${px(r)},${px(r)} 0 1 1 ${at(from + Math.PI, r)} A${px(r)},${px(r)} 0 1 1 ${at(from, r)} Z`
        : `M${px(cx)},${px(cy)} L${at(from, r)} A${px(r)},${px(r)} 0 ${sweep > Math.PI ? 1 : 0} 1 ${at(to, r)} Z`;
    scene.slices.push({ tone: i, d });
    const mid = from + sweep / 2;
    scene.targets.push(
      target({
        shape: "path",
        d,
        at: { x: px(cx + r * 0.6 * Math.cos(mid)), y: px(cy + r * 0.6 * Math.sin(mid)) },
        title: chart.labels[i],
        rows: [{ tone: i, name: one.name, value: `${formatNumber(v, locale)} (${formatShare(v, whole)})` }],
      }),
    );
    from = to;
  });
  return scene;
}

function layoutPlot(chart: Chart, width: number, height: number, locale?: string): Scene {
  const scene = empty(chart, width, height);
  const scatter = chart.type === "scatter";
  const set = setLegend(chart.series.length > 1 ? chart.series.map((s, i) => ({ tone: i, text: short(s.name, 24) })) : [], width);
  scene.legend = set.legend;

  // The vertical scale. Bars and areas grow from zero, so zero is on it.
  const ys = scatter ? chart.series.flatMap((s) => s.points.map((p) => p[1])) : chart.series.flatMap((s) => s.values);
  let lo = Math.min(...ys);
  let hi = Math.max(...ys);
  if (chart.type === "bar" || chart.type === "area") {
    lo = Math.min(0, lo);
    hi = Math.max(0, hi);
  }
  const yScale = niceScale(lo, hi);
  const yTexts = yScale.ticks.map((t) => tickText(t, yScale.step, Math.max(Math.abs(yScale.lo), Math.abs(yScale.hi)), locale));
  const gutter = Math.max(...yTexts.map(widthOf)) + 8;

  const left = pad + (chart.yTitle ? 18 : 0) + gutter;
  const top = pad + set.height + 6;
  const bottom = height - pad - (chart.xTitle ? 18 : 0) - 20;
  const plot = { x: px(left), y: px(top), w: px(Math.max(10, width - left - pad - 8)), h: px(Math.max(10, bottom - top)) };
  scene.plot = plot;
  const yOf = (v: number) => px(plot.y + plot.h - ((v - yScale.lo) / (yScale.hi - yScale.lo)) * plot.h);
  scene.yTicks = yScale.ticks.map((t, i) => ({ y: yOf(t), text: yTexts[i] }));
  if (yScale.lo <= 0 && yScale.hi >= 0) scene.zero = yOf(0);
  if (chart.xTitle) scene.xTitle = { x: px(plot.x + plot.w / 2), y: px(height - pad - 2), text: short(chart.xTitle, Math.max(8, Math.floor(plot.w / charWidth))) };
  if (chart.yTitle) scene.yTitle = { x: px(pad + 10), y: px(plot.y + plot.h / 2), text: short(chart.yTitle, Math.max(8, Math.floor(plot.h / charWidth))) };

  if (scatter) {
    const xs = chart.series.flatMap((s) => s.points.map((p) => p[0]));
    const xScale = niceScale(Math.min(...xs), Math.max(...xs), Math.max(2, Math.min(6, Math.floor(plot.w / 80))));
    const xOf = (v: number) => px(plot.x + ((v - xScale.lo) / (xScale.hi - xScale.lo)) * plot.w);
    const reach = Math.max(Math.abs(xScale.lo), Math.abs(xScale.hi));
    scene.xTicks = xScale.ticks.map((t) => ({ x: xOf(t), text: tickText(t, xScale.step, reach, locale) }));
    scene.xLines = xScale.ticks.map(xOf);
    chart.series.forEach((s, tone) => {
      for (const [vx, vy] of s.points) {
        const x = xOf(vx);
        const y = yOf(vy);
        scene.dots.push({ tone, x, y });
        scene.targets.push(
          target({
            shape: "circle",
            x,
            y,
            r: 10,
            at: { x, y },
            title: s.name,
            rows: [
              { tone: -1, name: chart.xTitle || "x", value: formatNumber(vx, locale) },
              { tone: -1, name: chart.yTitle || "y", value: formatNumber(vy, locale) },
            ],
          }),
        );
      }
    });
    return scene;
  }

  // Bar, line and area share the horizontal axis: one band to a label.
  const n = chart.labels.length;
  const band = plot.w / n;
  const centre = (i: number) => px(plot.x + band * (i + 0.5));
  const ticks = chart.labels.map((l) => short(l, 16));
  const every = Math.max(1, Math.ceil((Math.max(...ticks.map(widthOf)) + 10) / band));
  scene.xTicks = ticks.flatMap((text, i) => (i % every === 0 ? [{ x: centre(i), text }] : []));
  const base = scene.zero >= 0 ? scene.zero : yOf(yScale.lo);

  if (chart.type === "bar") {
    const k = chart.series.length;
    const gap = 2;
    const w = Math.max(1, Math.min(24, (band * 0.72 - gap * (k - 1)) / k));
    const group = k * w + gap * (k - 1);
    chart.series.forEach((s, tone) => {
      s.values.forEach((v, i) => {
        if (v === 0) return;
        const x = px(centre(i) - group / 2 + tone * (w + gap));
        const end = yOf(v);
        const r = px(Math.min(4, w / 2, Math.abs(end - base)));
        const x2 = px(x + w);
        // Round at the value's end, square where the bar stands on the line.
        const turn = v > 0 ? r : -r;
        scene.bars.push({
          tone,
          d: `M${x},${base} V${px(end + turn)} Q${x},${end} ${px(x + r)},${end} H${px(x2 - r)} Q${x2},${end} ${x2},${px(end + turn)} V${base} Z`,
        });
      });
    });
  } else {
    const markers = n === 1 || band >= 16;
    chart.series.forEach((s, tone) => {
      const points = s.values.map((v, i) => ({ x: centre(i), y: yOf(v) }));
      const path = points.map((p, i) => `${i === 0 ? "M" : "L"}${p.x},${p.y}`).join(" ");
      if (chart.type === "area") scene.areas.push({ tone, d: `${path} L${points[n - 1].x},${base} L${points[0].x},${base} Z` });
      scene.lines.push({ tone, d: path });
      if (markers) for (const p of points) scene.dots.push({ tone, x: p.x, y: p.y });
    });
  }

  for (let i = 0; i < n; i++) {
    scene.targets.push(
      target({
        shape: "rect",
        x: px(plot.x + band * i),
        y: plot.y,
        w: px(band),
        h: plot.h,
        at: { x: centre(i), y: Math.min(...chart.series.map((s) => yOf(s.values[i]))) },
        guide: chart.type === "bar" ? -1 : centre(i),
        title: chart.labels[i],
        rows: chart.series.map((s, tone) => ({ tone, name: s.name, value: formatNumber(s.values[i], locale) })),
      }),
    );
  }
  return scene;
}

/** The note beside a mark: where its box goes so it stays inside the drawing, and its lines. */
export type Tip = { x: number; y: number; w: number; h: number; title: string; rows: (Row & { y: number; text: string })[] };

export function tipFor(t: Target, scene: Scene): Tip {
  const rows = t.rows.map((r) => ({ ...r, text: `${short(r.name, 24)}: ${r.value}` }));
  const title = short(t.title, 32);
  const w = Math.min(scene.width - 8, Math.max(widthOf(title) * 1.08, ...rows.map((r) => widthOf(r.text) + (r.tone >= 0 ? 16 : 0))) + 20);
  const h = 12 + 18 * (rows.length + (title === "" ? 0 : 1));
  let x = t.at.x + 12;
  if (x + w > scene.width - 4) x = t.at.x - 12 - w;
  x = Math.max(4, Math.min(x, scene.width - w - 4));
  let y = t.at.y - h - 10;
  if (y < 4) y = Math.min(t.at.y + 12, scene.height - h - 4);
  y = Math.max(4, y);
  const first = y + 6 + 13 + (title === "" ? 0 : 18);
  return { x: px(x), y: px(y), w: px(w), h, title, rows: rows.map((r, i) => ({ ...r, y: px(first + i * 18) })) };
}

// ---------------------------------------------------------------------------
// The same chart in words, and as a table.
// ---------------------------------------------------------------------------

const typeNames: Record<ChartType, string> = { bar: "Bar chart", line: "Line chart", area: "Area chart", pie: "Pie chart", scatter: "Scatter chart" };

/** What kind of chart it is, in two words. */
export function chartKind(type: ChartType): string {
  return typeNames[type];
}

function count(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

/** A sentence or two that say what the chart shows, for a reader who cannot see it. */
export function chartSummary(chart: Chart, locale?: string): string {
  const f = (v: number) => formatNumber(v, locale);
  if (chart.type === "pie") {
    const one = chart.series[0];
    const whole = one.values.reduce((a, b) => a + b, 0);
    const parts = chart.labels.map((l, i) => `${l} ${formatShare(one.values[i], whole)}`);
    return `${typeNames.pie} of ${one.name}, ${count(chart.labels.length, "slice", "slices")}: ${parts.join(", ")}.`;
  }
  const axes = [chart.xTitle && `${chart.xTitle} across`, chart.yTitle && `${chart.yTitle} up`].filter(Boolean).join(", ");
  const head = `${typeNames[chart.type]}${axes ? ` with ${axes}` : ""}.`;
  if (chart.type === "scatter") {
    const each = chart.series.map((s) => {
      const xs = s.points.map((p) => p[0]);
      const ys = s.points.map((p) => p[1]);
      return `${s.name}, ${count(s.points.length, "point", "points")}, across from ${f(Math.min(...xs))} to ${f(Math.max(...xs))} and up from ${f(Math.min(...ys))} to ${f(Math.max(...ys))}`;
    });
    return `${head} ${each.join("; ")}.`;
  }
  const each = chart.series.map((s) => {
    const lo = Math.min(...s.values);
    const hi = Math.max(...s.values);
    if (lo === hi) return `${s.name} is ${f(lo)} throughout`;
    return `${s.name} runs from ${f(lo)} at ${chart.labels[s.values.indexOf(lo)]} to ${f(hi)} at ${chart.labels[s.values.indexOf(hi)]}`;
  });
  return `${head} ${count(chart.series.length, "series", "series")} over ${count(chart.labels.length, "label", "labels")}, ${chart.labels[0]} to ${chart.labels[chart.labels.length - 1]}: ${each.join("; ")}.`;
}

/** The chart's data as a table: a row of headings and rows of text. */
export function chartTable(chart: Chart, locale?: string): { head: string[]; rows: string[][] } {
  const f = (v: number) => formatNumber(v, locale);
  if (chart.type === "scatter") {
    return {
      head: ["Series", chart.xTitle || "x", chart.yTitle || "y"],
      rows: chart.series.flatMap((s) => s.points.map((p) => [s.name, f(p[0]), f(p[1])])),
    };
  }
  if (chart.type === "pie") {
    const one = chart.series[0];
    const whole = one.values.reduce((a, b) => a + b, 0);
    return {
      head: [chart.xTitle || "Slice", one.name, "Share"],
      rows: chart.labels.map((l, i) => [l, f(one.values[i]), formatShare(one.values[i], whole)]),
    };
  }
  return {
    head: [chart.xTitle || "", ...chart.series.map((s) => s.name)],
    rows: chart.labels.map((l, i) => [l, ...chart.series.map((s) => f(s.values[i]))]),
  };
}
