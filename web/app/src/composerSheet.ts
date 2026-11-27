// The words of the phone's sheets (decision 0590): what each row behind the
// composer's + and the header's ... says of its control's state, and the
// model's short name on the composer's chip. Kept apart from the screens and
// with no imports, so Node's own test runner runs it as it is
// (test/composerSheet.test.ts, decision 0501).

/** The widths at and under which the phone's layout is drawn: the sidebar's drawer, the + sheet and the ... menu. */
export const phoneQuery = "(max-width: 760px)";

/**
 * A model's label made short for the composer's chip: what is in brackets
 * at its end, such as the machine it runs on, goes, and so does anything
 * after a middle dot. The whole label is kept when nothing would be left.
 */
export function shortModelName(label: string): string {
  const whole = label.trim();
  let s = whole.split(" · ")[0].trim();
  for (;;) {
    const cut = s.replace(/\s*[([][^()[\]]*[)\]]\s*$/, "").trim();
    if (cut === s) break;
    s = cut;
  }
  return s === "" ? whole : s;
}

/** What the box says on a phone, where the screen has no room for a longer line (P10-07). */
export const phonePlaceholder = "Message Keystone";

/** The note that sat under the composer; on a phone it is at the foot of the model's sheet (P10-07). */
export const footnote = "Keystone can make mistakes. Check what matters against its sources.";

/** What the phone's composer holds and does: enough to say how it is drawn. */
export type ComposerState = {
  /** The focus is in the composer: its box, its buttons, or a sheet it opened. */
  focused: boolean;
  text: string;
  /** The files to go with the message. */
  files: number;
  /** Dictation is listening or finishing (P10-01). */
  dictating?: boolean;
};

/**
 * The phone's composer is one pill row while it is idle, and the card with
 * the box over its row once it is focused or holds anything (P10-07,
 * decision 0610). Text that is only spaces still counts: the person typed it.
 */
export function composerExpanded(s: ComposerState): boolean {
  return s.focused || s.text !== "" || s.files > 0 || s.dictating === true;
}

/** A person's first name, for the phone's one-line greeting: the first word of their name. */
export function firstName(name: string): string {
  const first = name.trim().split(/\s+/)[0] ?? "";
  return first;
}

/** The phone's greeting on the new chat screen: "Good afternoon, Test", or no name when there is none. */
export function phoneGreeting(part: string, name: string): string {
  const first = firstName(name);
  return first === "" ? `Good ${part}` : `Good ${part}, ${first}`;
}

/**
 * Whether a person's own message shows its actions. On a phone they wait
 * for a tap on the message, except that a pinned one keeps showing its pin
 * (decision 0612); elsewhere they are there, shown on hover by the stylesheet.
 */
export function saidActionsShown(s: { phone: boolean; tapped: boolean; pinned: boolean }): boolean {
  return !s.phone || s.tapped || s.pinned;
}

/** The line a row is read out by: its name and its state, "Style: Concise". */
export function rowLabel(name: string, state: string): string {
  return state === "" ? name : `${name}: ${state}`;
}

/** The files the message will carry. */
export function filesState(count: number): string {
  if (count <= 0) return "";
  return count === 1 ? "1 ready" : `${count} ready`;
}

/** The chats attached to this one, against the most it may have. */
export function attachedState(count: number, max: number): string {
  if (max > 0 && count >= max) return `${count} of ${max}, the most`;
  if (count <= 0) return "none";
  return `${count} attached`;
}

/** A switch's state, as the Research control's. */
export function switchState(on: boolean): string {
  return on ? "on" : "off";
}

/** The connectors on in this chat. */
export function connectorsState(on: number, total: number): string {
  if (total <= 0) return "none";
  return on === total ? `${on} on` : `${on} of ${total} on`;
}

/** The style replies are written in. */
export function styleState(name: string | undefined): string {
  return name && name.trim() !== "" ? name.trim() : "none";
}

/** The project the chat is in. */
export function projectState(name: string | undefined | null): string {
  return name && name.trim() !== "" ? name.trim() : "none";
}
