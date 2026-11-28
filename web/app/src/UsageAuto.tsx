// The Auto card of Settings → Usage (P14-07): what the period's replies on
// Auto cost with the router's decisions, against the same tokens priced at
// the top step of the person's ladder, and how often a stronger model was
// asked for. docs/architecture/auto-in-usage.md describes it. Provisional
// on the settings mockups (P1-07): there is no draft of this card, so it
// follows the spend card above it (design/screens/Operations.html), in the
// tokens. The bars are inline SVG, as the rest of the screen's (decision
// 0432).

import type { UsageAuto } from "./usageCalls";
import "./usage.css";
import "./usage-auto.css";

/** An amount in millionths of a dollar, to the cent. */
function dollars(micros: number): string {
  if (micros > 0 && micros < 5000) return "under $0.01";
  return "$" + (micros / 1e6).toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

function plural(n: number, one: string, many: string): string {
  return `${n.toLocaleString("en-US")} ${n === 1 ? one : many}`;
}

function sentence(text: string): string {
  const t = text.trim();
  if (t === "") return t;
  const first = t[0].toUpperCase() + t.slice(1);
  return /[.!?]$/.test(first) ? first : first + ".";
}

/** One line of the comparison: a name, a bar of its share of the larger amount, and the amount. */
function Line({ name, micros, top, tone }: { name: string; micros: number; top: number; tone: string }) {
  const share = top > 0 ? Math.max(0, Math.min(1, micros / top)) : 0;
  const width = share * 1000;
  return (
    <div className="use-auto-line">
      <span className="use-auto-name">{name}</span>
      <svg className="use-bar" viewBox="0 0 1000 10" preserveAspectRatio="none" aria-hidden="true">
        <rect className="use-bar-track" x="0" y="0" width="1000" height="10" />
        {width > 0 && <rect className={`use-bar-fill is-${tone}`} x="0" y="0" width={Math.max(width, 6)} height="10" />}
      </svg>
      <span className="use-auto-amount">{dollars(micros)}</span>
    </div>
  );
}

/** Whether a report has anything on Auto to show. */
export function hasAuto(auto: UsageAuto | undefined): auto is UsageAuto {
  return auto !== undefined && (auto.turns > 0 || auto.replies.calls > 0 || auto.decisions.calls > 0);
}

/** The Auto card. when is "this month" or "in this period". */
export function AutoCard({ auto, when }: { auto: UsageAuto; when: string }) {
  const top = auto.top_step;
  const priced = !top.problem;
  const saved = auto.saved_micros;
  const pill = !priced ? null : saved > 0 ? { text: `${dollars(saved)} saved`, tone: "ok" } : saved < 0 ? { text: `${dollars(-saved)} more`, tone: "warn" } : null;
  const most = Math.max(auto.cost_micros, top.cost_micros, 1);
  const share = auto.turns > 0 ? Math.round((auto.stronger_asked / auto.turns) * 1000) / 10 : 0;
  const local = auto.replies.local.calls;
  return (
    <div className="set-card use-cap use-auto" aria-label="Auto">
      <div className="use-cap-head">
        <p className="use-over">On Auto {when}</p>
        {pill && <span className={`set-pill use-pill is-${pill.tone}`}>{pill.text}</span>}
      </div>
      <p className="use-big">
        {dollars(auto.cost_micros)}
        {priced && <span className="use-big-of"> against {dollars(top.cost_micros)} at the top step</span>}
      </p>
      {priced && (
        <div className="use-auto-lines" role="img" aria-label={`Auto cost ${dollars(auto.cost_micros)}; the same tokens at ${top.label} would have cost ${dollars(top.cost_micros)}.`}>
          <Line name="Auto" micros={auto.cost_micros} top={most} tone="accent" />
          <Line name={top.label ?? top.name ?? "Top step"} micros={top.cost_micros} top={most} tone="ok" />
        </div>
      )}
      <p className="set-hint">
        {plural(auto.turns, "reply", "replies")} on Auto. Their {plural(auto.replies.calls, "call", "calls")} cost {dollars(auto.replies.cost_micros)}
        {local > 0 ? `, ${local.toLocaleString("en-US")} of them on a local model at no cost` : ""}, and the router's{" "}
        {plural(auto.decisions.calls, "decision", "decisions")} {dollars(auto.decisions.cost_micros)}.{" "}
        {priced
          ? `The same tokens at ${top.label ?? top.name}, the top step of your ladder, would have cost ${dollars(top.cost_micros)}, at its price today.`
          : sentence(top.problem ?? "")}
      </p>
      <div className="use-reserve">
        <p className="set-hint">
          {auto.stronger_asked > 0
            ? `A stronger model was asked for on ${plural(auto.stronger_asked, "reply", "replies")} of ${auto.turns.toLocaleString("en-US")} (${share}%).`
            : "A stronger model was not asked for."}
          {auto.rewrites > 0 ? ` ${plural(auto.rewrites, "reply was", "replies were")} written again a step up, counted above.` : ""}
        </p>
      </div>
    </div>
  );
}
