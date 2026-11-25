// Turns a chat's messages, and a reply still arriving, into what the
// conversation draws. The record holds a tool's results in a message with
// the role user, and a reply that used tools is several assistant messages.
// On the screen that is one reply: its steps, then its text.

import type { Block, Message } from "./api";
import type { Item, Part, Step } from "./Conversation";
import { isActive, thoughtFor, type Live } from "./turns";

type Reply = Extract<Item, { kind: "reply" }>;
type Result = { text: string; failed: boolean };

function textOf(blocks: Block[] | undefined): string {
  return (blocks ?? [])
    .filter((b) => b.type === "text" && b.text)
    .map((b) => b.text)
    .join("\n\n");
}

function inputOf(input: unknown): string {
  try {
    return JSON.stringify(input ?? {});
  } catch {
    return "";
  }
}

function addStep(reply: Reply, step: Step) {
  const last = reply.parts.at(-1);
  if (last && last.kind === "steps") last.steps.push(step);
  else reply.parts.push({ kind: "steps", steps: [step] });
}

// Why a stored reply ends where it does, when that is not the model
// finishing. The words stay with the message after the page is reloaded.
const endings: Record<string, Part> = {
  stopped: { kind: "note", text: "You stopped this reply." },
  failed: { kind: "note", text: "This reply broke off before it was finished." },
  max_tokens: { kind: "note", text: "This reply reached the model's length limit." },
  refused: { kind: "note", text: "The model declined to answer." },
};

/** The conversation's items, oldest first. `live` is the turn the page is following in this chat. */
export function layout(record: Message[], live: Live | undefined): Item[] {
  // The record, then what the turn has written that the record does not hold yet.
  const have = new Set(record.map((m) => m.id));
  const messages = live ? record.concat(live.written.filter((m) => !have.has(m.id))) : record;

  const results = new Map<string, Result>();
  for (const m of messages) {
    for (const b of m.blocks) {
      if (b.type === "tool_result" && b.id) results.set(b.id, { text: textOf(b.content), failed: b.is_error === true });
    }
  }
  if (live) for (const [id, r] of Object.entries(live.results)) results.set(id, r);

  const active = isActive(live);
  const items: Item[] = [];
  let reply: Reply | undefined;
  let anchor = "0"; // the message a reply answers, which names the reply on the screen

  const open = (): Reply => {
    if (!reply) {
      reply = { kind: "reply", key: "r" + anchor, parts: [], streaming: false };
      items.push(reply);
    }
    return reply;
  };

  const toolStep = (id: string, name: string, input: string): Step => {
    const r = results.get(id);
    return { kind: "tool", id, name, input, running: active && !r, result: r?.text, failed: r?.failed };
  };

  for (const m of messages) {
    if (m.role === "user") {
      const shown = m.blocks.filter((b) => b.type !== "tool_result");
      if (shown.length === 0) continue; // tool results only: part of the reply around it
      const files = shown.filter((b) => b.type === "image" || b.type === "file").map((b) => b.name || (b.type === "image" ? "Image" : "File"));
      items.push({ kind: "user", key: "m" + m.id, text: textOf(shown), files });
      anchor = String(m.id);
      reply = undefined;
      continue;
    }
    if (m.stop_reason === "task") {
      // A background task's note (P8-01): Keystone's words, as a reply of its own.
      items.push({ kind: "reply", key: "t" + m.id, parts: [{ kind: "task", text: textOf(m.blocks), messageId: m.id, chatId: m.chat_id }], streaming: false });
      reply = undefined;
      continue;
    }
    const into = open();
    if (m.stop_reason === "step_limit") {
      // Keystone's note at the turn's step limit, not the model's words.
      into.parts.push({ kind: "limit", text: textOf(m.blocks) });
      continue;
    }
    m.blocks.forEach((b, i) => {
      if (b.type === "thinking") addStep(into, { kind: "thinking", text: b.text ?? "", running: false, seconds: thoughtFor(m.id, i) });
      else if (b.type === "tool_call") addStep(into, toolStep(b.id ?? "", b.name ?? "a tool", inputOf(b.input)));
      else if (b.type === "text" && b.text) into.parts.push({ kind: "text", text: b.text });
    });
    const ending = m.stop_reason ? endings[m.stop_reason] : undefined;
    if (ending) into.parts.push(ending);
  }

  if (!live) return items;

  if (live.said !== "") {
    items.push({ kind: "user", key: "said", text: live.said, files: [] });
    anchor = "said";
    reply = undefined;
  }

  const drafted = live.draft.filter(Boolean);
  if (drafted.length === 0 && !active && live.problem === "") return items;

  const into = open();
  for (const b of drafted) {
    if (b.type === "thinking") addStep(into, { kind: "thinking", text: b.text, running: b.open && active, seconds: b.seconds });
    else if (b.type === "tool_call") addStep(into, toolStep(b.id, b.name, b.input));
    else if (b.text) into.parts.push({ kind: "text", text: b.text });
  }
  into.streaming = active;
  if (live.state === "waiting") into.parts.push({ kind: "note", text: "Waiting for the reply before this one to finish." });
  if (live.state === "reconnecting") into.parts.push({ kind: "note", text: "The connection dropped. Connecting again." });
  if (live.state === "stopped" && drafted.length > 0) into.parts.push(endings.stopped);
  if (live.problem !== "") into.parts.push({ kind: "error", text: live.problem });
  return items;
}
