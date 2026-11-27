// The proof of P9-04's chart format: what is a chart and what is refused,
// and what the page builds for each type. Run by Node's own test runner
// (`npm test` in web/app, and the `inline visuals` check in tools/verify),
// decision 0501. The numbers are invented.

import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { test } from "node:test";
import { chartSummary, chartTable, chartTypes, formatShare, layoutChart, niceScale, parseChart, tipFor, type Chart, type ChartLimits } from "../src/chart.ts";

const limits: ChartLimits = { maxSeries: 6, maxPoints: 200, labelMaxChars: 80 };

function chart(o: unknown): Chart {
  const got = parseChart(JSON.stringify(o), limits);
  if (!got.ok) throw new Error(got.reason);
  return got.chart;
}

function refused(o: unknown, with_: ChartLimits = limits): string {
  const got = parseChart(typeof o === "string" ? o : JSON.stringify(o), with_);
  assert.equal(got.ok, false, "the block was read as a chart");
  return got.ok ? "" : got.reason;
}

const ferry = {
  type: "bar",
  title: "Ferry passengers by month",
  x: { title: "Month", labels: ["Jan", "Feb", "Mar", "Apr"] },
  y: { title: "Passengers" },
  series: [
    { name: "North pier", values: [120, 150, 210, 260] },
    { name: "Gull Island", values: [80, 95, 140, 135] },
  ],
};

test("a chart of each type is read", () => {
  for (const type of ["bar", "line", "area"]) {
    const c = chart({ ...ferry, type });
    assert.equal(c.type, type);
    assert.equal(c.title, "Ferry passengers by month");
    assert.deepEqual([c.xTitle, c.yTitle], ["Month", "Passengers"]);
    assert.deepEqual(c.labels, ["Jan", "Feb", "Mar", "Apr"]);
    assert.deepEqual(c.series[1], { name: "Gull Island", values: [80, 95, 140, 135], points: [] });
  }
  const pie = chart({ type: "pie", title: "How people reach the harbour", x: { labels: ["Coach", "Car", "On foot"] }, series: [{ name: "Visitors", values: [46, 30, 24] }] });
  assert.deepEqual(pie.labels, ["Coach", "Car", "On foot"]);
  const scatter = chart({ type: "scatter", title: "Tide and wind", x: { title: "Wind" }, y: { title: "Tide" }, series: [{ name: "Readings", points: [[1, 2.5], [3, 4]] }] });
  assert.deepEqual(scatter.series[0].points, [[1, 2.5], [3, 4]]);
  assert.deepEqual(scatter.labels, []);
  assert.deepEqual([...chartTypes], ["bar", "line", "area", "pie", "scatter"]);
});

test("labels that are numbers are read as their digits, and space is tidied", () => {
  const c = chart({ ...ferry, title: "  Ferry \n passengers ", x: { labels: [2021, 2022, 2023, 2024] } });
  assert.deepEqual(c.labels, ["2021", "2022", "2023", "2024"]);
  assert.equal(c.title, "Ferry passengers");
  assert.equal(c.xTitle, "");
});

test("keys the format does not name are ignored", () => {
  const c = chart({ ...ferry, colours: ["red"], onclick: "alert(1)", series: ferry.series.map((s) => ({ ...s, color: "<script>" })) });
  assert.deepEqual(Object.keys(c).sort(), ["labels", "series", "title", "type", "xTitle", "yTitle"]);
  assert.deepEqual(Object.keys(c.series[0]).sort(), ["name", "points", "values"]);
});

test("what is not a chart is refused with a plain sentence", () => {
  assert.equal(refused("{ not json"), "It is not JSON.");
  assert.equal(refused("[1, 2]"), "It is not a JSON object.");
  assert.equal(refused('"bar"'), "It is not a JSON object.");
  assert.match(refused({ ...ferry, type: "radar" }), /Its type is "radar"; a chart is one of bar, line, area, pie, scatter\./);
  assert.match(refused({ ...ferry, type: undefined }), /Its type is missing/);
  assert.equal(refused({ ...ferry, title: undefined }), "It has no title.");
  assert.equal(refused({ ...ferry, title: "   " }), "Its title is empty.");
  assert.match(refused({ ...ferry, title: { a: 1 } }), /Its title is .* not text\./);
  assert.match(refused({ ...ferry, x: "Month" }), /Its x is "Month", not an object/);
  assert.match(refused({ ...ferry, x: { title: "Month" } }), /It has no x\.labels/);
  assert.equal(refused({ ...ferry, x: { labels: [] } }), "Its x.labels are empty.");
  assert.equal(refused({ ...ferry, series: undefined }), "It has no series.");
  assert.equal(refused({ ...ferry, series: [] }), "Its series are empty.");
  assert.match(refused({ ...ferry, series: ["North pier"] }), /Series 1 is "North pier", not an object/);
  assert.equal(refused({ ...ferry, series: [{ values: [1, 2, 3, 4] }] }), "It has no series 1's name.");
  assert.equal(refused({ ...ferry, series: [ferry.series[0], ferry.series[0]] }), 'Two series are named "North pier".');
  assert.equal(refused({ ...ferry, series: [{ name: "North pier", values: [1, 2, 3] }] }), 'The series "North pier" has 3 values for 4 labels.');
  assert.equal(refused({ ...ferry, series: [{ name: "North pier" }] }), 'The series "North pier" has no values.');
});

test("every value is a finite number", () => {
  assert.match(refused({ ...ferry, series: [{ name: "North pier", values: [1, "2", 3, 4] }] }), /Value 2 of "North pier" is "2", not a number\./);
  assert.match(refused({ ...ferry, series: [{ name: "North pier", values: [1, 2, null, 4] }] }), /Value 3 of "North pier" is null, not a number\./);
  // JSON reads 1e999 as infinity.
  assert.match(refused('{"type":"line","title":"t","x":{"labels":["a"]},"series":[{"name":"s","values":[1e999]}]}'), /Value 1 of "s" is .*not a number\./);
  assert.match(refused({ type: "scatter", title: "t", series: [{ name: "s", points: [[1, 2], [3]] }] }), /Point 2 of "s" is \[3\], not an \[x, y\] pair\./);
  assert.match(refused({ type: "scatter", title: "t", series: [{ name: "s", points: [[1, "2"]] }] }), /Point 1 of "s" is "2", not a number\./);
  assert.equal(refused({ type: "scatter", title: "t", series: [{ name: "s", values: [1] }] }), 'The series "s" has no points, the list of [x, y] pairs.');
});

test("a pie has one series, nothing below zero, and something to show", () => {
  const pie = { type: "pie", title: "t", x: { labels: ["a", "b"] } };
  assert.equal(refused({ ...pie, series: [{ name: "one", values: [1, 2] }, { name: "two", values: [1, 2] }] }), "A pie has one series, and this has 2.");
  assert.equal(refused({ ...pie, series: [{ name: "one", values: [1, -2] }] }), "A pie cannot show a value below zero.");
  assert.equal(refused({ ...pie, series: [{ name: "one", values: [0, 0] }] }), "A pie's values add up to nothing.");
});

test("the settings bound a chart: series, slices, points and the length of a label", () => {
  const small: ChartLimits = { maxSeries: 2, maxPoints: 3, labelMaxChars: 10 };
  assert.equal(refused({ ...ferry, title: "Short" }, small), "It has 4 labels; the most is 3.");
  const three = { type: "line", title: "Short", x: { labels: ["a", "b", "c"] }, series: [1, 2, 3].map((n) => ({ name: "s" + n, values: [1, 2, 3] })) };
  assert.equal(refused(three, small), "It has 3 series; the most is 2.");
  assert.equal(refused({ ...three, series: three.series.slice(0, 2), title: "A title that is long" }, small), "Its title is longer than 10 characters.");
  assert.equal(refused({ type: "pie", title: "Short", x: { labels: ["a", "b", "c"] }, series: [{ name: "s", values: [1, 2, 3] }] }, small), "It has 3 slices; the most is 2.");
  assert.equal(refused({ type: "scatter", title: "Short", series: [{ name: "s", points: [[1, 1], [2, 2], [3, 3], [4, 4]] }] }, small), 'The series "s" has 4 points; the most is 3.');
  assert.equal(parseChart(JSON.stringify({ ...three, series: three.series.slice(0, 2) }), small).ok, true);
});

test("a scale has round ends and round steps", () => {
  assert.deepEqual(niceScale(0, 260).ticks, [0, 50, 100, 150, 200, 250, 300]);
  assert.deepEqual(niceScale(-30, 45).ticks, [-40, -20, 0, 20, 40, 60]);
  assert.deepEqual(niceScale(0.12, 0.47).ticks, [0.1, 0.2, 0.3, 0.4, 0.5]);
  const flat = niceScale(7, 7);
  assert.ok(flat.lo < 7 && flat.hi > 7, "one value has room either side");
  assert.ok(niceScale(0, 0).hi > 0);
});

test("a bar chart is built as bars that stand on zero", () => {
  const s = layoutChart(chart(ferry), 640, 320, "en-US");
  assert.equal(s.type, "bar");
  assert.equal(s.bars.length, 8);
  assert.deepEqual(s.legend.map((l) => [l.tone, l.text]), [[0, "North pier"], [1, "Gull Island"]]);
  assert.deepEqual(s.yTicks.map((t) => t.text), ["0", "50", "100", "150", "200", "250", "300"]);
  assert.deepEqual(s.xTicks.map((t) => t.text), ["Jan", "Feb", "Mar", "Apr"]);
  assert.equal(s.xTitle?.text, "Month");
  assert.equal(s.yTitle?.text, "Passengers");
  assert.equal(s.zero, s.yTicks[0].y, "zero is the lowest line");
  for (const b of s.bars) {
    assert.ok(b.d.startsWith("M") && b.d.endsWith("Z"));
    assert.ok(b.d.includes(`,${s.zero} V`), "a bar starts on the zero line");
    assert.doesNotMatch(b.d, /NaN|Infinity|undefined/);
  }
  assert.equal(s.targets.length, 4, "one place to rest on for each label");
  assert.equal(s.targets[2].title, "Mar");
  assert.deepEqual(s.targets[2].rows, [
    { tone: 0, name: "North pier", value: "210" },
    { tone: 1, name: "Gull Island", value: "140" },
  ]);
  assert.equal(s.targets[0].guide, -1);
  // Everything is inside the box.
  for (const t of s.targets) assert.ok(t.x >= 0 && t.x + t.w <= 640.5 && t.y >= 0 && t.y + t.h <= 320);
});

test("a bar below zero hangs from the zero line, and a bar of nothing is not drawn", () => {
  const s = layoutChart(chart({ type: "bar", title: "Change", x: { labels: ["a", "b", "c"] }, series: [{ name: "Change", values: [30, -20, 0] }] }), 480, 300, "en-US");
  assert.equal(s.bars.length, 2);
  assert.equal(s.legend.length, 0, "one series needs no legend");
  assert.ok(s.zero > s.plot.y && s.zero < s.plot.y + s.plot.h);
});

test("a line chart is built as one line a series, and an area as a line and its wash", () => {
  const line = layoutChart(chart({ ...ferry, type: "line" }), 640, 320, "en-US");
  assert.equal(line.lines.length, 2);
  assert.equal(line.areas.length, 0);
  assert.equal(line.bars.length, 0);
  assert.equal(line.dots.length, 8);
  assert.match(line.lines[0].d, /^M[\d.]+,[\d.]+( L[\d.]+,[\d.]+){3}$/);
  assert.ok(line.targets[1].guide > 0, "a line has a guide at each label");
  // A line's scale need not start at zero.
  assert.deepEqual(line.yTicks.map((t) => t.text), ["50", "100", "150", "200", "250", "300"]);

  const area = layoutChart(chart({ ...ferry, type: "area" }), 640, 320, "en-US");
  assert.equal(area.areas.length, 2);
  assert.equal(area.lines.length, 2);
  assert.ok(area.areas[0].d.endsWith("Z"));
  assert.equal(area.yTicks[0].text, "0", "an area grows from zero");
});

test("a pie is built as slices that make the whole, with each share in the legend", () => {
  const s = layoutChart(chart({ type: "pie", title: "How people reach the harbour", x: { labels: ["Coach", "Car", "On foot", "Nobody"] }, series: [{ name: "Visitors", values: [46, 30, 24, 0] }] }), 640, 320, "en-US");
  assert.equal(s.slices.length, 3, "a slice of nothing is not drawn");
  assert.deepEqual(s.legend.map((l) => l.text), ["Coach 46%", "Car 30%", "On foot 24%", "Nobody 0%"]);
  assert.deepEqual(s.targets[0].rows, [{ tone: 0, name: "Visitors", value: "46 (46%)" }]);
  for (const sl of s.slices) assert.match(sl.d, /^M[\d.]+,[\d.]+ L[\d.]+,[\d.]+ A[\d.]+,[\d.]+ 0 [01] 1 [\d.]+,[\d.]+ Z$/);
  const whole = layoutChart(chart({ type: "pie", title: "All", x: { labels: ["All"] }, series: [{ name: "Visitors", values: [5] }] }), 300, 300, "en-US");
  assert.equal((whole.slices[0].d.match(/A/g) ?? []).length, 2, "the whole pie is two half circles");
  assert.equal(formatShare(1, 300), "0.3%");
});

test("a scatter chart is built as points on two scales", () => {
  const s = layoutChart(chart({ type: "scatter", title: "Tide and wind", x: { title: "Wind" }, y: { title: "Tide" }, series: [{ name: "Morning", points: [[2, 1.5], [8, 3.5]] }, { name: "Evening", points: [[5, 2]] }] }), 640, 320, "en-US");
  assert.equal(s.dots.length, 3);
  assert.equal(s.targets.length, 3);
  assert.equal(s.targets[0].shape, "circle");
  assert.deepEqual(s.targets[2], { ...s.targets[2], title: "Evening", rows: [{ tone: -1, name: "Wind", value: "5" }, { tone: -1, name: "Tide", value: "2" }] });
  assert.ok(s.xTicks.length >= 2 && s.xLines.length === s.xTicks.length);
  for (const d of s.dots) assert.ok(d.x >= s.plot.x && d.x <= s.plot.x + s.plot.w && d.y >= s.plot.y && d.y <= s.plot.y + s.plot.h);
});

test("at phone width the labels thin out and nothing leaves the box", () => {
  const months = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];
  const c = chart({ type: "line", title: "A year", x: { labels: months }, series: [{ name: "North pier", values: months.map((_, i) => 12000 + i * 900) }, { name: "Gull Island", values: months.map((_, i) => 9000 + i * 400) }] });
  const s = layoutChart(c, 311, 320, "en-US");
  assert.ok(s.xTicks.length < 12 && s.xTicks.length >= 2, `${s.xTicks.length} labels drawn`);
  assert.equal(s.yTicks[0].text, "5K", "an axis that reaches ten thousand is compact");
  assert.ok(s.plot.x + s.plot.w <= 311);
  for (const l of s.legend) assert.ok(l.x >= 0 && l.x < 311);
  const tip = tipFor(s.targets[11], s);
  assert.ok(tip.x >= 0 && tip.x + tip.w <= 311 && tip.y >= 0 && tip.y + tip.h <= 320, "the note stays inside the drawing");
  assert.equal(tip.title, "December");
  assert.deepEqual(tip.rows.map((r) => r.text), ["North pier: 21,900", "Gull Island: 13,400"]);
});

test("the summary and the table say what the chart shows", () => {
  const c = chart(ferry);
  assert.equal(
    chartSummary(c, "en-US"),
    "Bar chart with Month across, Passengers up. 2 series over 4 labels, Jan to Apr: North pier runs from 120 at Jan to 260 at Apr; Gull Island runs from 80 at Jan to 140 at Mar.",
  );
  assert.deepEqual(chartTable(c, "en-US"), {
    head: ["Month", "North pier", "Gull Island"],
    rows: [["Jan", "120", "80"], ["Feb", "150", "95"], ["Mar", "210", "140"], ["Apr", "260", "135"]],
  });
  const pie = chart({ type: "pie", title: "How people reach the harbour", x: { labels: ["Coach", "Car"] }, series: [{ name: "Visitors", values: [3, 1] }] });
  assert.equal(chartSummary(pie, "en-US"), "Pie chart of Visitors, 2 slices: Coach 75%, Car 25%.");
  assert.deepEqual(chartTable(pie, "en-US").rows, [["Coach", "3", "75%"], ["Car", "1", "25%"]]);
  const scatter = chart({ type: "scatter", title: "Tide and wind", series: [{ name: "Morning", points: [[2, 1.5], [8, 3.5]] }] });
  assert.equal(chartSummary(scatter, "en-US"), "Scatter chart. Morning, 2 points, across from 2 to 8 and up from 1.5 to 3.5.");
  assert.deepEqual(chartTable(scatter, "en-US"), { head: ["Series", "x", "y"], rows: [["Morning", "2", "1.5"], ["Morning", "8", "3.5"]] });
});

test("nothing a chart holds reaches the page as more than text", () => {
  const nasty = '<img src=x onerror="alert(1)">';
  const c = chart({ type: "bar", title: nasty, x: { title: nasty, labels: [nasty] }, y: { title: nasty }, series: [{ name: nasty, values: [1] }, { name: "two", values: [2] }] });
  const s = layoutChart(c, 640, 320, "en-US");
  // The layout's own strings are paths and numbers; the model's are kept as
  // plain text, which the page writes as text nodes and never as markup.
  for (const shape of [...s.bars, ...s.lines, ...s.areas, ...s.slices]) assert.match(shape.d, /^[MLHVQAZ0-9 .,-]+$/);
  for (const text of [...s.legend.map((l) => l.text), ...s.xTicks.map((t) => t.text), s.xTitle?.text ?? "", s.yTitle?.text ?? ""]) assert.equal(typeof text, "string");
  assert.ok(s.yTicks.every((t) => /^[\d.,KMB-]+$/.test(t.text)));
});

test("the test tenant's recorded chart is one the page draws", () => {
  const dir = new URL("../../../fixtures/test-tenant/turns/", import.meta.url);
  const shipped: ChartLimits = { maxSeries: 6, maxPoints: 200, labelMaxChars: 80 };
  let charts = 0;
  let diagrams = 0;
  for (const name of readdirSync(dir)) {
    if (!name.endsWith(".json")) continue;
    const texts: string[] = [];
    JSON.stringify(JSON.parse(readFileSync(new URL(name, dir), "utf8")), (_, v) => {
      if (typeof v === "string" && v.includes("```")) texts.push(v);
      return v;
    });
    for (const text of texts) {
      for (const m of text.matchAll(/```(chart|mermaid)\n([\s\S]*?)\n```/g)) {
        if (m[1] === "mermaid") {
          diagrams++;
          continue;
        }
        // A recorded reply may hold a chart that is wrong on purpose; it says so.
        const got = parseChart(m[2], shipped);
        if (text.includes("not a chart Keystone can draw")) {
          if (!got.ok) continue;
        }
        assert.equal(got.ok, true, `${name}: ${got.ok ? "" : got.reason}`);
        if (got.ok) {
          charts++;
          const s = layoutChart(got.chart, 640, 320, "en-US");
          assert.ok(s.bars.length + s.lines.length + s.slices.length + s.dots.length > 0);
        }
      }
    }
  }
  assert.ok(charts >= 2, `${charts} recorded charts`);
  assert.ok(diagrams >= 1, `${diagrams} recorded diagrams`);
});
