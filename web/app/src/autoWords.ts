// The words of Auto on the screen (P14-04): the reply's footnote, the
// notice, the picker's sentences and when "Try a stronger model" is offered.
// Kept apart from the screens and with no imports, so Node's own test
// runner runs it as it is (test/autoWords.test.ts, decision 0501).
//
// Provisional on the chat mockups (P1-04): there is no mockup of Auto yet.

/** How one turn on Auto was routed, as the server sends it (api.ts TurnRoute). */
export type RouteWords = {
  auto: boolean;
  model: string;
  label?: string;
  step?: number;
  steps?: number;
  effort?: string;
  reason?: string;
  notice?: string;
};

/** The name a pick of Auto is kept under. No registry name has a colon in it. */
export const autoPick = ":auto";

/** The chip's and the menu's name for Auto. */
export const autoLabel = "Auto";

/** The line under Auto in the picker. */
export function autoLine(steps: number): string {
  if (steps <= 1) return "Keystone picks the effort for each message.";
  return `Keystone picks the model and the effort for each message, from ${steps} models.`;
}

/** The reply's footnote: the model, the effort, the step. Empty off Auto. */
export function routeLine(r: RouteWords | undefined): string {
  if (!r || !r.auto) return "";
  const parts = [`Auto: ${r.label || r.model}`];
  if (r.effort) parts.push(`effort ${r.effort}`);
  if (r.step && r.steps) parts.push(`step ${r.step} of ${r.steps}`);
  return parts.join(" · ");
}

/** Why the router chose it: Keystone's own line, never a model's. */
export function routeReason(r: RouteWords | undefined): string {
  return r?.auto && r.reason ? r.reason : "";
}

/** The notice a reply carries when the local machine was not answering. */
export function routeNotice(r: RouteWords | undefined): string {
  if (r?.notice === "local_unavailable") return "The local machine is not answering, so a model that is not on it wrote this reply.";
  return "";
}

/** Whether a reply offers "Try a stronger model": on Auto, below the top step. */
export function strongerOffered(r: RouteWords | undefined): boolean {
  return r !== undefined && r.auto && (r.step ?? 0) > 0 && (r.steps ?? 0) > (r.step ?? 0);
}

/**
 * The ladder's names from the router.ladder setting, for the new chat
 * screen, which has no chat to ask: names separated by commas, each with an
 * optional size limit after <=, which is dropped here.
 */
export function ladderNames(setting: string): string[] {
  const out: string[] = [];
  for (const part of setting.split(",")) {
    const name = part.split("<=")[0].trim();
    if (name !== "" && !out.includes(name)) out.push(name);
  }
  return out;
}

/** The sentence at the foot of the picker about Auto, or empty when another sentence says it. */
export function autoFoot(s: { auto: boolean; pickedAuto: boolean; pickedModel: boolean; pending: boolean; isNew: boolean; lastStep?: string }): string {
  if (s.pending && s.pickedAuto) return s.isNew ? "The new chat starts on Auto." : "Sent with your next message: the chat goes on Auto.";
  if (s.pending && s.pickedModel && s.auto) return "Sent with your next message. It takes the chat off Auto.";
  if (s.auto && !s.pickedModel) {
    const last = s.lastStep ? ` The last message used ${s.lastStep}.` : "";
    return `Each message is routed afresh, and its reply says which model wrote it and why.${last}`;
  }
  return "";
}

/** What a refused "Try a stronger model" says. */
export function strongerProblem(code: string, said: string): string {
  if (said) return said.charAt(0).toUpperCase() + said.slice(1) + (said.endsWith(".") ? "" : ".");
  if (code === "no_stronger") return "No stronger model may write this reply.";
  if (code === "not_auto") return "Only a reply written on Auto can be written again by a stronger model.";
  return "A stronger model could not be asked.";
}
