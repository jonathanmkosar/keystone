// The admin's router policy on the screen (P14-05): which models the person
// may pick by hand, whether they may take a chat off Auto, and what a
// refused message says. The server enforces the policy (internal/turn,
// policy.go) and this only reflects it. Kept apart from the screens and with
// no imports, so Node's own test runner runs it as it is
// (test/policyWords.test.ts, decision 0501).
//
// Provisional on the owner's answers on admin and views (A1 to A5) and the
// chat mockups (P1-04): there is no mockup of a greyed model or of a chat
// held on Auto.

/** What the policy needs of a model choice (api.ts ModelChoice). */
export type PolicyChoice = {
  models: { name: string }[];
  ladder?: unknown[];
  may_switch_off?: boolean;
  allowed?: string[];
};

/** The line under a model the person may not pick by hand. */
export const notAllowedWhy = "Your admin has not allowed this model to be picked by hand.";

/** The line in the picker when the person may not take a chat off Auto. */
export const autoRequiredNote = "Your admin keeps your chats on Auto, so Keystone picks the model for each message.";

/** Whether the person may pick a model by hand. A choice with no allowed list allows every model. */
export function allowedByHand(choice: PolicyChoice | undefined, name: string): boolean {
  if (!choice || !Array.isArray(choice.allowed)) return true;
  return choice.allowed.includes(name);
}

/** Whether the person must stay on Auto: they may not switch it off, and Auto is offered. */
export function autoRequired(choice: PolicyChoice | undefined): boolean {
  return choice?.may_switch_off === false && (choice.ladder?.length ?? 0) > 0;
}

/**
 * The names in router.allowed_models, for the new chat screen, which has no
 * chat to ask: names separated by commas. Empty means every model.
 */
export function allowedNames(setting: string): string[] {
  const out: string[] = [];
  for (const part of setting.split(",")) {
    const name = part.trim();
    if (name !== "" && !out.includes(name)) out.push(name);
  }
  return out;
}

/**
 * The policy of a new chat, as the server works it out for a chat: the
 * allowed list among the models the chat may use (every one when the setting
 * is empty), and whether the person may switch Auto off (always, with no
 * ladder).
 */
export function newChatPolicy(models: string[], allowedSetting: string, maySwitchOffSetting: string, ladderSteps: number): { allowed: string[]; may_switch_off: boolean } {
  const names = allowedNames(allowedSetting);
  const allowed = names.length === 0 ? models.slice() : models.filter((m) => names.includes(m));
  return { allowed, may_switch_off: maySwitchOffSetting !== "false" || ladderSteps === 0 };
}

/** What a message refused by the admin's policy says, or empty for another refusal. */
export function policyProblem(code: string, said: string): string {
  if (code !== "model_not_allowed" && code !== "auto_required") return "";
  if (said) return said.charAt(0).toUpperCase() + said.slice(1) + (said.endsWith(".") ? "" : ".");
  if (code === "auto_required") return "Your admin keeps your chats on Auto, so the message was not sent with a model picked by hand. Send it on Auto.";
  return "Your admin has not allowed that model to be picked by hand, so the message was not sent. Pick another model.";
}
