// Auto on the screen (P14-04), apart from the picker: the reply's footnote,
// "Try a stronger model" beside Copy, and the verdict in the turn inspector.
// Provisional on the chat mockups (P1-04): there is no mockup of Auto yet,
// so it is drawn from the tokens like the reply's other actions. The words
// are in autoWords.ts.

import type { TurnRoute, Verdict } from "./api";
import { routeLine, routeNotice, routeReason, strongerOffered } from "./autoWords";
import { Icon } from "./Icon";
import { isActive, stronger, useLive } from "./turns";
import "./auto.css";

/** The footnote under a reply on Auto: which model wrote it, at what effort, and why. Nothing off Auto. */
export function RouteFootnote({ route }: { route: TurnRoute | undefined }) {
  const line = routeLine(route);
  if (line === "") return null;
  const reason = routeReason(route);
  const notice = routeNotice(route);
  return (
    <div className="reply-route">
      {notice && (
        <p className="reply-route-notice" role="note">
          <Icon name="alert" size={14} />
          <span>{notice}</span>
        </p>
      )}
      <p className="reply-route-line" title={reason || undefined}>
        <span>{line}</span>
        {reason && <span className="reply-route-reason">{reason}</span>}
      </p>
    </div>
  );
}

/** Try a stronger model: the reply written again one step up, on a new branch. Offered on Auto below the top step. */
export function StrongerButton({ chatId, turnId, route }: { chatId: string; turnId: string; route: TurnRoute | undefined }) {
  const busy = isActive(useLive(chatId));
  if (!strongerOffered(route)) return null;
  return (
    <button
      type="button"
      className="copy-button"
      aria-label="Try a stronger model"
      title="Write this reply again with the next model up the ladder. The new reply is a branch beside this one."
      disabled={busy}
      onClick={() => stronger(chatId, turnId)}
    >
      <Icon name="send" size={14} />
      <span className="action-label">Try a stronger model</span>
    </button>
  );
}

const rulesOnlyWhy: Record<string, string> = {
  one_candidate: "one step could take the message",
  no_decision_model: "no decision model is named",
  decision_model_unusable: "the decision model cannot be used",
  held: "the decision model may not read this chat",
  local_unavailable: "the local machine was not answering",
  timeout: "the decision took too long",
  error: "the decision failed",
  stronger_asked: "a stronger model was asked for",
};

/** The router's record of how the turn was routed, in the turn inspector. */
export function VerdictSection({ verdict }: { verdict: Verdict | undefined }) {
  if (!verdict) return null;
  const v = verdict;
  const rows: [string, string][] = [
    ["Chosen", `${v.step}, effort ${v.effort}`],
    ["Why", v.reason],
  ];
  if (v.rules_only) rows.push(["Decided by", `the rules alone: ${rulesOnlyWhy[v.rules_only_why ?? ""] ?? v.rules_only_why ?? ""}`]);
  else rows.push(["Decided by", `${v.decision_model ?? "the decision model"}, in ${v.latency_ms} ms`]);
  if (v.step_answered) rows.push(["The verdict", `${v.step_answered} at ${(v.probability ?? 0).toFixed(2)}, effort ${v.effort_answered ?? ""}${v.moved_up ? "; unsure, so moved up a step" : ""}`]);
  const shown = v.shown;
  rows.push([
    v.decision_model && !v.rules_only ? "It was shown" : "The facts",
    `${v.decision_model && !v.rules_only ? "the message's text and " : ""}${shown.tokens.toLocaleString()} estimated tokens, ${shown.tools} tools offered, ${shown.images ? "an image" : "no image"}, ${shown.files ? "a file" : "no file"}, ${shown.turns} earlier turns${shown.previous_step ? `, the last on ${shown.previous_step}` : ""}`,
  ]);
  if (v.notice === "local_unavailable" || v.local_unavailable) rows.push(["Notice", "the local machine was not answering"]);
  if (v.stronger_asked) rows.push(["Afterwards", "you asked for a stronger model"]);
  return (
    <section className="inspector-section" aria-label="How Auto chose the model">
      <h3 className="inspector-heading">How Auto chose the model</h3>
      <dl className="verdict-list">
        {rows.map(([k, val]) => (
          <div key={k} className="verdict-row">
            <dt>{k}</dt>
            <dd>{val}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}
